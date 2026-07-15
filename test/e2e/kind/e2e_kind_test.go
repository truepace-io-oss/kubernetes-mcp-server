//go:build e2e_kind

// Package kindtest runs the kubernetes-mcp server against a real kind cluster
// with running workloads, so tools that need live pods (logs) are exercised —
// something envtest (which never schedules pods) cannot cover.
//
// Prerequisite: a kind cluster and KUBECONFIG pointing at it, e.g.
//
//	export KUBECONFIG="$(test/e2e/kind/setup-kind.sh)"
//	go test -tags e2e_kind ./test/e2e/kind/... -v
package kindtest

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/clusters"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/config"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/mcpserver"
	authnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const marker = "E2E_KIND_MARKER_42"

func adminConfig(t *testing.T) (*kubernetes.Clientset, string, []byte) {
	t.Helper()
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("KUBECONFIG not set; run test/e2e/kind/setup-kind.sh first")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("load kubeconfig: %v", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ca := cfg.CAData
	if len(ca) == 0 && cfg.CAFile != "" {
		ca, _ = os.ReadFile(cfg.CAFile)
	}
	return cs, cfg.Host, ca
}

func TestE2EKindPodLogs(t *testing.T) {
	cs, host, ca := adminConfig(t)
	ctx := context.Background()
	ns := "kmcp-kind"

	_, _ = cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})

	// A pod that emits a known marker line, then idles.
	_, _ = cs.CoreV1().Pods(ns).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "logger", Namespace: ns},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "app",
				Image:   "busybox:1.36",
				Command: []string{"sh", "-c", "echo " + marker + "; sleep 3600"},
			}},
		},
	}, metav1.CreateOptions{})

	// Wait until the pod is Running.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		p, err := cs.CoreV1().Pods(ns).Get(ctx, "logger", metav1.GetOptions{})
		if err == nil && p.Status.Phase == corev1.PodRunning {
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Provision MCP credentials: reader ClusterRole + SA + binding + token.
	_, _ = cs.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "kmcp-kind-reader"},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"pods", "pods/log", "namespaces"}, Verbs: []string{"get", "list", "watch"}},
		},
	}, metav1.CreateOptions{})
	_, _ = cs.CoreV1().ServiceAccounts(ns).Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "mcp", Namespace: ns}}, metav1.CreateOptions{})
	_, _ = cs.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "kmcp-kind-binding"},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "kmcp-kind-reader"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "mcp", Namespace: ns}},
	}, metav1.CreateOptions{})
	exp := int64(3600)
	tr, err := cs.CoreV1().ServiceAccounts(ns).CreateToken(ctx, "mcp", &authnv1.TokenRequest{Spec: authnv1.TokenRequestSpec{ExpirationSeconds: &exp}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}

	// Build + serve the MCP against the kind cluster using the SA token.
	cc := config.ClusterConfig{
		Name:                     "kind",
		Server:                   host,
		CertificateAuthorityData: base64.StdEncoding.EncodeToString(ca),
		Token:                    tr.Status.Token,
	}
	cfg := &config.Config{LogLevel: "error", DefaultCluster: "kind", Clusters: []config.ClusterConfig{cc}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	reg, err := clusters.Build(cfg)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	mcpSrv := mcpserver.New(reg, cfg).MCPServer()
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "kind-e2e", Version: "0"}, nil)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	sess, err := client.Connect(cctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	call := func(name string, args map[string]any) string {
		res, err := sess.CallTool(cctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("call %s: %v", name, err)
		}
		var out string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				out += tc.Text
			}
		}
		if res.IsError {
			t.Fatalf("%s returned tool error: %s", name, out)
		}
		return out
	}

	if out := call("pods_list", map[string]any{"namespace": ns}); !strings.Contains(out, "logger") {
		t.Fatalf("pods_list missing logger pod: %s", out)
	}
	if out := call("pods_log", map[string]any{"namespace": ns, "name": "logger"}); !strings.Contains(out, marker) {
		t.Fatalf("pods_log missing marker: %s", out)
	}
}
