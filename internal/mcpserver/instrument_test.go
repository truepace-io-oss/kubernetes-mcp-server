package mcpserver

import (
	"errors"
	"testing"
)

func TestClassifyResult(t *testing.T) {
	cases := []struct {
		name string
		res  string
		err  error
		want string
	}{
		{"ok", "", nil, "ok"},
		{"protocol error", "", errors.New("boom"), "error"},
		{"forbidden", "pods is forbidden: User cannot list", nil, "forbidden"},
		{"blocked global", "this MCP instance is configured read-only", nil, "blocked"},
		{"blocked cluster", "writes are disabled for cluster \"x\" (readOnly)", nil, "blocked"},
		{"other tool error", "not found", nil, "error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var res = textResult(c.res)
			if c.res == "" && c.err == nil {
				res = textResult("all good") // non-error result
			} else if c.res != "" {
				res = errorResult(errors.New(c.res)) // IsError with the text
			} else {
				res = nil
			}
			if got := classifyResult(res, c.err); got != c.want {
				t.Fatalf("classify(%q,%v) = %q, want %q", c.res, c.err, got, c.want)
			}
		})
	}
}

func TestClusterLabel(t *testing.T) {
	s := buildTestServer(t, false) // registry default cluster = "test"
	if got := s.clusterLabel(clusterParam{Cluster: "home"}); got != "home" {
		t.Fatalf("explicit cluster label = %q", got)
	}
	if got := s.clusterLabel(clusterParam{}); got != "test" {
		t.Fatalf("empty cluster should map to default, got %q", got)
	}
	if got := s.clusterLabel(struct{}{}); got != "-" {
		t.Fatalf("no-cluster input should map to '-', got %q", got)
	}
}
