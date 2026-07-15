package mcpserver

import (
	"fmt"

	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/clusters"
)

// Shared input structs for the tools. Field descriptions (jsonschema tag) are
// surfaced to the LLM as the tool's parameter documentation.

type clusterParam struct {
	Cluster string `json:"cluster,omitempty" jsonschema:"the configured cluster to target; defaults to the server's default cluster when omitted"`
}

type namespaceParam struct {
	clusterParam
	Namespace string `json:"namespace,omitempty" jsonschema:"namespace to scope to; empty means all namespaces (for list operations)"`
}

type resourceRef struct {
	clusterParam
	APIVersion string `json:"apiVersion" jsonschema:"resource apiVersion, e.g. 'v1', 'apps/v1', 'networking.k8s.io/v1'"`
	Kind       string `json:"kind" jsonschema:"resource kind, e.g. 'Pod', 'Deployment', 'ConfigMap'"`
	Namespace  string `json:"namespace,omitempty" jsonschema:"namespace for namespaced resources"`
	Name       string `json:"name" jsonschema:"object name"`
}

type listResourcesParam struct {
	clusterParam
	APIVersion    string `json:"apiVersion" jsonschema:"resource apiVersion, e.g. 'v1', 'apps/v1'"`
	Kind          string `json:"kind" jsonschema:"resource kind, e.g. 'Pod', 'Deployment'"`
	Namespace     string `json:"namespace,omitempty" jsonschema:"namespace to list in; empty lists across all namespaces"`
	LabelSelector string `json:"labelSelector,omitempty" jsonschema:"optional label selector, e.g. 'app=nginx'"`
	FieldSelector string `json:"fieldSelector,omitempty" jsonschema:"optional field selector, e.g. 'status.phase=Running'"`
}

// resolveCluster picks the target cluster from the argument or the default.
func (s *Server) resolveCluster(name string) (*clusters.Cluster, error) {
	return s.reg.Get(name)
}

// resolveNamespace applies the cluster's default namespace when the caller left
// it empty; an empty result means "all namespaces".
func resolveNamespace(cl *clusters.Cluster, ns string) string {
	if ns == "" {
		return cl.DefaultNamespace
	}
	return ns
}

// assertWritable blocks mutating operations when writes are disabled globally or
// for the target cluster. RBAC on the API server is still the ultimate gate;
// this is defense-in-depth so an over-privileged token cannot be used to write
// through an instance an operator intends to be read-only.
func (s *Server) assertWritable(cl *clusters.Cluster) error {
	if s.readOnly {
		return fmt.Errorf("this MCP instance is configured read-only (writes disabled globally)")
	}
	if cl.ReadOnly {
		return fmt.Errorf("writes are disabled for cluster %q (readOnly)", cl.Name)
	}
	return nil
}
