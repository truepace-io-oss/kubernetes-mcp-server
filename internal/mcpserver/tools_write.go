package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/k8s"
	"k8s.io/apimachinery/pkg/types"
)

func (s *Server) registerWriteTools(m *mcp.Server) {
	mcp.AddTool(m, &mcp.Tool{
		Name:        "resources_apply",
		Description: "Server-side apply a YAML/JSON manifest to a cluster (create or update). Blocked when the instance or cluster is read-only; ultimately governed by RBAC.",
	}, s.resourcesApply)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "resources_delete",
		Description: "Delete a single Kubernetes object by apiVersion+kind+name. Blocked when read-only; governed by RBAC.",
	}, s.resourcesDelete)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "deployment_scale",
		Description: "Scale a Deployment to a given replica count. Blocked when read-only; governed by RBAC.",
	}, s.deploymentScale)

	mcp.AddTool(m, &mcp.Tool{
		Name:        "rollout_restart",
		Description: "Trigger a rolling restart of a Deployment, StatefulSet or DaemonSet by patching its pod template annotation. Blocked when read-only; governed by RBAC.",
	}, s.rolloutRestart)
}

func (s *Server) resourcesApply(ctx context.Context, _ *mcp.CallToolRequest, in struct {
	clusterParam
	Manifest string `json:"manifest" jsonschema:"the resource manifest as YAML or JSON (must include apiVersion, kind, metadata.name)"`
}) (*mcp.CallToolResult, any, error) {
	cl, err := s.resolveCluster(in.Cluster)
	if err != nil {
		return errorResult(err), nil, nil
	}
	if err := s.assertWritable(cl); err != nil {
		return errorResult(err), nil, nil
	}
	obj, err := k8s.Apply(ctx, cl.Dynamic, cl.Mapper(), []byte(in.Manifest), cl.DefaultNamespace)
	if err != nil {
		return errorResult(err), nil, nil
	}
	loc := obj.GetName()
	if ns := obj.GetNamespace(); ns != "" {
		loc = ns + "/" + obj.GetName()
	}
	return textResult(fmt.Sprintf("applied %s %s", obj.GetKind(), loc)), nil, nil
}

func (s *Server) resourcesDelete(ctx context.Context, _ *mcp.CallToolRequest, in resourceRef) (*mcp.CallToolResult, any, error) {
	cl, err := s.resolveCluster(in.Cluster)
	if err != nil {
		return errorResult(err), nil, nil
	}
	if err := s.assertWritable(cl); err != nil {
		return errorResult(err), nil, nil
	}
	m, err := k8s.ResolveMapping(cl.Mapper(), in.APIVersion, in.Kind)
	if err != nil {
		return errorResult(err), nil, nil
	}
	ns := resolveNamespace(cl, in.Namespace)
	if err := k8s.Delete(ctx, cl.Dynamic, m, ns, in.Name); err != nil {
		return errorResult(err), nil, nil
	}
	return textResult(fmt.Sprintf("deleted %s %s", in.Kind, in.Name)), nil, nil
}

func (s *Server) deploymentScale(ctx context.Context, _ *mcp.CallToolRequest, in struct {
	clusterParam
	Namespace string `json:"namespace" jsonschema:"deployment namespace"`
	Name      string `json:"name" jsonschema:"deployment name"`
	Replicas  int32  `json:"replicas" jsonschema:"desired replica count"`
}) (*mcp.CallToolResult, any, error) {
	cl, err := s.resolveCluster(in.Cluster)
	if err != nil {
		return errorResult(err), nil, nil
	}
	if err := s.assertWritable(cl); err != nil {
		return errorResult(err), nil, nil
	}
	m, err := k8s.ResolveMapping(cl.Mapper(), "apps/v1", "Deployment")
	if err != nil {
		return errorResult(err), nil, nil
	}
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, in.Replicas)
	if _, err := k8s.Patch(ctx, cl.Dynamic, m, in.Namespace, in.Name, types.MergePatchType, []byte(patch)); err != nil {
		return errorResult(err), nil, nil
	}
	return textResult(fmt.Sprintf("scaled Deployment %s/%s to %d replicas", in.Namespace, in.Name, in.Replicas)), nil, nil
}

func (s *Server) rolloutRestart(ctx context.Context, _ *mcp.CallToolRequest, in struct {
	clusterParam
	Kind      string `json:"kind" jsonschema:"workload kind: Deployment, StatefulSet or DaemonSet"`
	Namespace string `json:"namespace" jsonschema:"workload namespace"`
	Name      string `json:"name" jsonschema:"workload name"`
}) (*mcp.CallToolResult, any, error) {
	cl, err := s.resolveCluster(in.Cluster)
	if err != nil {
		return errorResult(err), nil, nil
	}
	if err := s.assertWritable(cl); err != nil {
		return errorResult(err), nil, nil
	}
	switch in.Kind {
	case "Deployment", "StatefulSet", "DaemonSet":
	default:
		return errorResult(fmt.Errorf("rollout_restart supports Deployment, StatefulSet, DaemonSet; got %q", in.Kind)), nil, nil
	}
	m, err := k8s.ResolveMapping(cl.Mapper(), "apps/v1", in.Kind)
	if err != nil {
		return errorResult(err), nil, nil
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`, stamp)
	if _, err := k8s.Patch(ctx, cl.Dynamic, m, in.Namespace, in.Name, types.StrategicMergePatchType, []byte(patch)); err != nil {
		return errorResult(err), nil, nil
	}
	return textResult(fmt.Sprintf("restarted %s %s/%s at %s", in.Kind, in.Namespace, in.Name, stamp)), nil, nil
}
