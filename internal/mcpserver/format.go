package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// textResult wraps a plain string into an MCP tool result.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// errorResult wraps an error as an MCP *tool* error (IsError), so the model sees
// the message (including Kubernetes 403/404 text) instead of a transport failure.
func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}

// redactSecret blanks the values inside a Secret's data/stringData, keeping keys
// visible. Reading Secret values is a common exfiltration vector; the MCP never
// returns them by default.
func redactSecret(obj *unstructured.Unstructured) {
	if obj.GetKind() != "Secret" {
		return
	}
	for _, field := range []string{"data", "stringData"} {
		m, found, _ := unstructured.NestedMap(obj.Object, field)
		if !found {
			continue
		}
		for k := range m {
			m[k] = "<redacted>"
		}
		_ = unstructured.SetNestedMap(obj.Object, m, field)
	}
}

// objectJSON renders a single object as pretty JSON (secrets redacted). HTML
// escaping is disabled so values like "<redacted>" stay readable.
func objectJSON(obj *unstructured.Unstructured) string {
	redactSecret(obj)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(obj.Object); err != nil {
		return fmt.Sprintf("<error rendering object: %v>", err)
	}
	return strings.TrimRight(buf.String(), "\n")
}

// listSummary renders a resource list as a compact, LLM-friendly table plus a
// count. Each Secret in the list is redacted.
func listSummary(kind string, list *unstructured.UnstructuredList) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s item(s):\n", len(list.Items), kind)
	for i := range list.Items {
		it := &list.Items[i]
		redactSecret(it)
		ns := it.GetNamespace()
		loc := it.GetName()
		if ns != "" {
			loc = ns + "/" + it.GetName()
		}
		fmt.Fprintf(&b, "- %s", loc)
		if extra := summaryExtra(it); extra != "" {
			fmt.Fprintf(&b, "  (%s)", extra)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// summaryExtra adds a few useful, kind-specific fields to a list line.
func summaryExtra(it *unstructured.Unstructured) string {
	var parts []string
	switch it.GetKind() {
	case "Pod":
		if phase, ok, _ := unstructured.NestedString(it.Object, "status", "phase"); ok {
			parts = append(parts, "phase="+phase)
		}
		if node, ok, _ := unstructured.NestedString(it.Object, "spec", "nodeName"); ok && node != "" {
			parts = append(parts, "node="+node)
		}
	case "Deployment", "StatefulSet", "ReplicaSet":
		ready, _, _ := unstructured.NestedInt64(it.Object, "status", "readyReplicas")
		desired, _, _ := unstructured.NestedInt64(it.Object, "spec", "replicas")
		parts = append(parts, fmt.Sprintf("ready=%d/%d", ready, desired))
	case "Node":
		if conds, ok, _ := unstructured.NestedSlice(it.Object, "status", "conditions"); ok {
			for _, c := range conds {
				cm, _ := c.(map[string]any)
				if cm["type"] == "Ready" {
					parts = append(parts, "Ready="+fmt.Sprint(cm["status"]))
				}
			}
		}
	}
	return strings.Join(parts, " ")
}

// eventsSummary renders events sorted by last timestamp.
func eventsSummary(list *unstructured.UnstructuredList) string {
	type ev struct{ ts, line string }
	rows := make([]ev, 0, len(list.Items))
	for i := range list.Items {
		it := &list.Items[i]
		reason, _, _ := unstructured.NestedString(it.Object, "reason")
		msg, _, _ := unstructured.NestedString(it.Object, "message")
		etype, _, _ := unstructured.NestedString(it.Object, "type")
		last, _, _ := unstructured.NestedString(it.Object, "lastTimestamp")
		objName, _, _ := unstructured.NestedString(it.Object, "involvedObject", "name")
		rows = append(rows, ev{ts: last, line: fmt.Sprintf("[%s] %s %s: %s (%s)", etype, last, reason, msg, objName)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ts < rows[j].ts })
	var b strings.Builder
	fmt.Fprintf(&b, "%d event(s):\n", len(rows))
	for _, r := range rows {
		b.WriteString("- " + r.line + "\n")
	}
	return b.String()
}

// listOptions builds metav1.ListOptions from selector strings.
func listOptions(labelSelector, fieldSelector string) metav1.ListOptions {
	return metav1.ListOptions{LabelSelector: labelSelector, FieldSelector: fieldSelector}
}
