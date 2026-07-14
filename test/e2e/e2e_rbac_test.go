package e2e

import (
	"context"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// TestE2ERBACReadOnlyTokenCannotWrite proves the central-auth claim: the same
// MCP code, given a read-only token, surfaces a Kubernetes 403 on a write —
// there is no policy in the MCP itself, only RBAC on the API server.
func TestE2ERBACReadOnlyTokenCannotWrite(t *testing.T) {
	cs := adminClient(t)
	ns := "e2e-rbac-ro"
	ensureNamespace(t, cs, ns)
	createReaderClusterRole(t, cs, "e2e-reader") // idempotent

	token := mintToken(t, cs, ns, "ro-sa")
	grantClusterRole(t, cs, "ro-sa", ns, "e2e-reader", "e2e-ro-binding")

	// Note: the MCP instance itself is NOT configured read-only, so the guard is
	// not what blocks the write — RBAC is.
	sess := startMCP(t, "envtest", token, false, false)

	// Read works.
	if out, isErr := callText(t, sess, "namespaces_list", map[string]any{}); isErr {
		t.Fatalf("read should succeed: %s", out)
	}

	// Write is denied by RBAC (Forbidden), surfaced as a tool error.
	out, isErr := callText(t, sess, "resources_apply", map[string]any{
		"cluster":  "envtest",
		"manifest": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: should-fail\n  namespace: " + ns + "\ndata:\n  x: 'val'\n",
	})
	if !isErr {
		t.Fatalf("expected RBAC-denied write, got success: %s", out)
	}
	if !strings.Contains(strings.ToLower(out), "forbidden") {
		t.Fatalf("expected Forbidden in error, got: %s", out)
	}
}

// createWriterClusterRole grants write on configmaps in addition to reads.
func createWriterClusterRole(t *testing.T, cs *kubernetes.Clientset, name string) {
	t.Helper()
	_, err := cs.RbacV1().ClusterRoles().Create(context.Background(), &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"namespaces", "configmaps"}, Verbs: []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
		},
	}, metav1.CreateOptions{})
	if err != nil && !isAlreadyExists(err) {
		t.Fatalf("create writer clusterrole: %v", err)
	}
}

// TestE2ERBACWriterTokenCanWrite proves a token with write RBAC succeeds through
// the same MCP tool.
func TestE2ERBACWriterTokenCanWrite(t *testing.T) {
	cs := adminClient(t)
	ns := "e2e-rbac-rw"
	ensureNamespace(t, cs, ns)
	createWriterClusterRole(t, cs, "e2e-writer")

	token := mintToken(t, cs, ns, "rw-sa")
	grantClusterRole(t, cs, "rw-sa", ns, "e2e-writer", "e2e-rw-binding")

	sess := startMCP(t, "envtest", token, false, false)

	out, isErr := callText(t, sess, "resources_apply", map[string]any{
		"cluster":  "envtest",
		"manifest": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: made-by-mcp\n  namespace: " + ns + "\ndata:\n  x: 'val'\n",
	})
	if isErr || !strings.Contains(out, "applied ConfigMap") {
		t.Fatalf("writer apply should succeed: err=%v out=%s", isErr, out)
	}

	// Confirm the object really exists via the admin client.
	if _, err := cs.CoreV1().ConfigMaps(ns).Get(context.Background(), "made-by-mcp", metav1.GetOptions{}); err != nil {
		t.Fatalf("configmap not created by MCP apply: %v", err)
	}
}

// TestE2EPerClusterReadOnlyGuard proves the defense-in-depth guard: even with a
// write-capable token, a cluster flagged readOnly refuses writes *before*
// hitting the API.
func TestE2EPerClusterReadOnlyGuard(t *testing.T) {
	cs := adminClient(t)
	ns := "e2e-guard"
	ensureNamespace(t, cs, ns)
	createWriterClusterRole(t, cs, "e2e-writer") // idempotent

	token := mintToken(t, cs, ns, "guard-sa")
	grantClusterRole(t, cs, "guard-sa", ns, "e2e-writer", "e2e-guard-binding")

	// clusterReadOnly = true.
	sess := startMCP(t, "envtest", token, true, false)

	out, isErr := callText(t, sess, "resources_apply", map[string]any{
		"cluster":  "envtest",
		"manifest": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: blocked\n  namespace: " + ns + "\ndata:\n  x: 'val'\n",
	})
	if !isErr || !strings.Contains(out, "readOnly") {
		t.Fatalf("expected per-cluster readOnly guard, got err=%v out=%s", isErr, out)
	}
	// And the object must not exist.
	if _, err := cs.CoreV1().ConfigMaps(ns).Get(context.Background(), "blocked", metav1.GetOptions{}); err == nil {
		t.Fatalf("guard failed: configmap was created despite readOnly")
	}
}
