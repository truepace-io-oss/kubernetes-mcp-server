package e2e

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// createReaderClusterRole creates a self-contained read-only ClusterRole so the
// test does not depend on the apiserver's bootstrapped "view" role.
func createReaderClusterRole(t *testing.T, cs *kubernetes.Clientset, name string) {
	t.Helper()
	_, err := cs.RbacV1().ClusterRoles().Create(context.Background(), &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"namespaces", "configmaps", "pods", "pods/log", "events", "nodes", "services"}, Verbs: []string{"get", "list", "watch"}},
			{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get", "list", "watch"}},
		},
	}, metav1.CreateOptions{})
	if err != nil && !isAlreadyExists(err) {
		t.Fatalf("create clusterrole %s: %v", name, err)
	}
}

func TestE2EReadOnlyFlow(t *testing.T) {
	cs := adminClient(t)
	ns := "e2e-read"
	ensureNamespace(t, cs, ns)

	// Seed a ConfigMap through the admin client.
	_, err := cs.CoreV1().ConfigMaps(ns).Create(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app-settings", Namespace: ns},
		Data:       map[string]string{"greeting": "hello-from-e2e"},
	}, metav1.CreateOptions{})
	if err != nil && !isAlreadyExists(err) {
		t.Fatalf("seed configmap: %v", err)
	}

	// Provision the MCP's credentials: a SA + read-only ClusterRole + binding.
	createReaderClusterRole(t, cs, "e2e-reader")
	token := mintToken(t, cs, ns, "mcp-reader")
	grantClusterRole(t, cs, "mcp-reader", ns, "e2e-reader", "e2e-reader-binding")

	sess := startMCP(t, "envtest", token, false, false)

	// clusters_list: the cluster must be reachable.
	if out, isErr := callText(t, sess, "clusters_list", map[string]any{}); isErr || !strings.Contains(out, "reachable") {
		t.Fatalf("clusters_list: err=%v out=%s", isErr, out)
	}

	// namespaces_list: our namespace shows up.
	if out, isErr := callText(t, sess, "namespaces_list", map[string]any{}); isErr || !strings.Contains(out, ns) {
		t.Fatalf("namespaces_list missing %s: err=%v out=%s", ns, isErr, out)
	}

	// resources_list ConfigMap: fetch data through the MCP end-to-end.
	out, isErr := callText(t, sess, "resources_list", map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap", "namespace": ns,
	})
	if isErr || !strings.Contains(out, "app-settings") {
		t.Fatalf("resources_list configmap: err=%v out=%s", isErr, out)
	}

	// resources_get: the object round-trips with its data.
	out, isErr = callText(t, sess, "resources_get", map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap", "namespace": ns, "name": "app-settings",
	})
	if isErr || !strings.Contains(out, "hello-from-e2e") {
		t.Fatalf("resources_get configmap: err=%v out=%s", isErr, out)
	}
}
