package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (s *Server) registerReadTools(m *mcp.Server) {
	addTool(m, s, "clusters_list", "List the Kubernetes clusters this MCP instance manages, with reachability and read-only status.", s.clustersList)
	addTool(m, s, "namespaces_list", "List namespaces in a cluster.", s.namespacesList)
	addTool(m, s, "resources_list", "List objects of any Kubernetes kind (built-in or CRD) by apiVersion+kind, optionally filtered by namespace and selectors.", s.resourcesList)
	addTool(m, s, "resources_get", "Get a single Kubernetes object by apiVersion+kind+name (secrets are returned with values redacted).", s.resourcesGet)
	addTool(m, s, "pods_list", "List pods in a namespace (or all namespaces) with phase and node.", s.podsList)
	addTool(m, s, "pods_log", "Fetch logs from a pod container.", s.podsLog)
	addTool(m, s, "events_list", "List events in a namespace (or all namespaces), sorted by time.", s.eventsList)
	addTool(m, s, "nodes_list", "List cluster nodes with readiness.", s.nodesList)
}

func (s *Server) clustersList(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	var b strings.Builder
	b.WriteString("Managed clusters:\n")
	for _, cl := range s.reg.All() {
		marker := ""
		if cl.Name == s.reg.DefaultName() {
			marker = " (default)"
		}
		status := "reachable"
		if ver, err := cl.Ping(ctx); err != nil {
			status = "UNREACHABLE: " + err.Error()
		} else {
			status = "reachable, server " + ver
		}
		fmt.Fprintf(&b, "- %s%s readOnly=%t — %s\n", cl.Name, marker, cl.ReadOnly, status)
	}
	return textResult(b.String()), nil, nil
}

func (s *Server) namespacesList(ctx context.Context, _ *mcp.CallToolRequest, in clusterParam) (*mcp.CallToolResult, any, error) {
	cl, err := s.resolveCluster(in.Cluster)
	if err != nil {
		return errorResult(err), nil, nil
	}
	nsList, err := cl.Typed.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return errorResult(err), nil, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d namespace(s):\n", len(nsList.Items))
	for _, ns := range nsList.Items {
		fmt.Fprintf(&b, "- %s (%s)\n", ns.Name, ns.Status.Phase)
	}
	return textResult(b.String()), nil, nil
}

func (s *Server) resourcesList(ctx context.Context, _ *mcp.CallToolRequest, in listResourcesParam) (*mcp.CallToolResult, any, error) {
	cl, err := s.resolveCluster(in.Cluster)
	if err != nil {
		return errorResult(err), nil, nil
	}
	m, err := k8s.ResolveMapping(cl.Mapper(), in.APIVersion, in.Kind)
	if err != nil {
		return errorResult(err), nil, nil
	}
	ns := resolveNamespace(cl, in.Namespace)
	list, err := k8s.List(ctx, cl.Dynamic, m, ns, listOptions(in.LabelSelector, in.FieldSelector))
	if err != nil {
		return errorResult(err), nil, nil
	}
	return textResult(listSummary(in.Kind, list)), nil, nil
}

func (s *Server) resourcesGet(ctx context.Context, _ *mcp.CallToolRequest, in resourceRef) (*mcp.CallToolResult, any, error) {
	cl, err := s.resolveCluster(in.Cluster)
	if err != nil {
		return errorResult(err), nil, nil
	}
	m, err := k8s.ResolveMapping(cl.Mapper(), in.APIVersion, in.Kind)
	if err != nil {
		return errorResult(err), nil, nil
	}
	ns := resolveNamespace(cl, in.Namespace)
	obj, err := k8s.Get(ctx, cl.Dynamic, m, ns, in.Name)
	if err != nil {
		return errorResult(err), nil, nil
	}
	return textResult(objectJSON(obj)), nil, nil
}

func (s *Server) podsList(ctx context.Context, _ *mcp.CallToolRequest, in struct {
	namespaceParam
	LabelSelector string `json:"labelSelector,omitempty" jsonschema:"optional label selector"`
}) (*mcp.CallToolResult, any, error) {
	cl, err := s.resolveCluster(in.Cluster)
	if err != nil {
		return errorResult(err), nil, nil
	}
	ns := resolveNamespace(cl, in.Namespace)
	pods, err := cl.Typed.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: in.LabelSelector})
	if err != nil {
		return errorResult(err), nil, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d pod(s):\n", len(pods.Items))
	for _, p := range pods.Items {
		ready := 0
		for _, cs := range p.Status.ContainerStatuses {
			if cs.Ready {
				ready++
			}
		}
		fmt.Fprintf(&b, "- %s/%s phase=%s ready=%d/%d node=%s\n",
			p.Namespace, p.Name, p.Status.Phase, ready, len(p.Spec.Containers), p.Spec.NodeName)
	}
	return textResult(b.String()), nil, nil
}

func (s *Server) podsLog(ctx context.Context, _ *mcp.CallToolRequest, in struct {
	clusterParam
	Namespace string `json:"namespace" jsonschema:"pod namespace"`
	Name      string `json:"name" jsonschema:"pod name"`
	Container string `json:"container,omitempty" jsonschema:"container name (defaults to the first container)"`
	TailLines int64  `json:"tailLines,omitempty" jsonschema:"number of lines from the end of the logs to show"`
	Previous  bool   `json:"previous,omitempty" jsonschema:"return logs from the previous terminated container instance"`
}) (*mcp.CallToolResult, any, error) {
	cl, err := s.resolveCluster(in.Cluster)
	if err != nil {
		return errorResult(err), nil, nil
	}
	opts := &corev1.PodLogOptions{Container: in.Container, Previous: in.Previous}
	if in.TailLines > 0 {
		opts.TailLines = &in.TailLines
	}
	stream, err := cl.Typed.CoreV1().Pods(in.Namespace).GetLogs(in.Name, opts).Stream(ctx)
	if err != nil {
		return errorResult(err), nil, nil
	}
	defer stream.Close()
	var sb strings.Builder
	buf := make([]byte, 32*1024)
	for {
		n, rerr := stream.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if rerr != nil {
			break
		}
	}
	out := sb.String()
	if out == "" {
		out = "(no log output)"
	}
	return textResult(out), nil, nil
}

func (s *Server) eventsList(ctx context.Context, _ *mcp.CallToolRequest, in namespaceParam) (*mcp.CallToolResult, any, error) {
	cl, err := s.resolveCluster(in.Cluster)
	if err != nil {
		return errorResult(err), nil, nil
	}
	m, err := k8s.ResolveMapping(cl.Mapper(), "v1", "Event")
	if err != nil {
		return errorResult(err), nil, nil
	}
	ns := resolveNamespace(cl, in.Namespace)
	list, err := k8s.List(ctx, cl.Dynamic, m, ns, metav1.ListOptions{})
	if err != nil {
		return errorResult(err), nil, nil
	}
	return textResult(eventsSummary(list)), nil, nil
}

func (s *Server) nodesList(ctx context.Context, _ *mcp.CallToolRequest, in clusterParam) (*mcp.CallToolResult, any, error) {
	cl, err := s.resolveCluster(in.Cluster)
	if err != nil {
		return errorResult(err), nil, nil
	}
	m, err := k8s.ResolveMapping(cl.Mapper(), "v1", "Node")
	if err != nil {
		return errorResult(err), nil, nil
	}
	list, err := k8s.List(ctx, cl.Dynamic, m, "", metav1.ListOptions{})
	if err != nil {
		return errorResult(err), nil, nil
	}
	return textResult(listSummary("Node", list)), nil, nil
}
