package mcpserver

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/metrics"
)

// addTool registers a tool wrapped with metrics: it times the call, resolves the
// target cluster for the label, classifies the result, and records both a
// counter and a latency observation.
func addTool[In any](m *mcp.Server, s *Server, name, description string, h mcp.ToolHandlerFor[In, any]) {
	wrapped := func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		res, out, err := h(ctx, req, in)
		metrics.RecordTool(name, s.clusterLabel(in), classifyResult(res, err), time.Since(start))
		return res, out, err
	}
	mcp.AddTool(m, &mcp.Tool{Name: name, Description: description}, wrapped)
}

// clusterLabel returns the resolved cluster name for a tool input (default when
// the caller omitted it; "-" for tools with no cluster arg like clusters_list).
func (s *Server) clusterLabel(in any) string {
	c, ok := in.(interface{ metricCluster() string })
	if !ok {
		return "-"
	}
	if name := c.metricCluster(); name != "" {
		return name
	}
	return s.reg.DefaultName()
}

// classifyResult maps a tool outcome to a bounded result label.
func classifyResult(res *mcp.CallToolResult, err error) string {
	if err != nil {
		return "error"
	}
	if res == nil || !res.IsError {
		return "ok"
	}
	low := strings.ToLower(resultText(res))
	switch {
	case strings.Contains(low, "forbidden"):
		return "forbidden"
	case strings.Contains(low, "read-only"), strings.Contains(low, "readonly"):
		return "blocked"
	default:
		return "error"
	}
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
