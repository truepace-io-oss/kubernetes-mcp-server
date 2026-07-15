// Package mcpserver builds the MCP server: it registers the Kubernetes tools and
// routes each call to the right cluster in the registry. It contains no
// authentication logic — every request is executed with the target cluster's
// ServiceAccount credentials and authorized by Kubernetes RBAC.
package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/clusters"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/config"
)

// Version is injected by main via SetVersion; kept here so the MCP Implementation
// can report it.
var serverVersion = "dev"

// SetVersion sets the version advertised by the MCP server.
func SetVersion(v string) {
	if v != "" {
		serverVersion = v
	}
}

// Server holds the shared dependencies for all tool handlers.
type Server struct {
	reg      *clusters.Registry
	readOnly bool // global kill-switch
}

// New builds a Server from the registry and config.
func New(reg *clusters.Registry, cfg *config.Config) *Server {
	return &Server{reg: reg, readOnly: cfg.ReadOnly}
}

// MCPServer constructs an *mcp.Server with all tools registered. The streamable
// HTTP handler calls this (via a closure) per session.
func (s *Server) MCPServer() *mcp.Server {
	m := mcp.NewServer(&mcp.Implementation{
		Name:    "kubernetes-mcp",
		Version: serverVersion,
	}, nil)

	s.registerReadTools(m)
	s.registerWriteTools(m)
	return m
}
