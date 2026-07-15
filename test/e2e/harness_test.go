// Package e2e drives the kubernetes-mcp server end-to-end against a real
// Kubernetes API server (provided by envtest) using the official MCP client over
// the streamable-HTTP transport. It proves the central-auth model: the server
// authenticates with a ServiceAccount token and Kubernetes RBAC alone decides
// what each token may do.
package e2e

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
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
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// env holds the shared envtest environment for the whole package.
var (
	testEnv  *envtest.Environment
	adminCfg *rest.Config
)

func TestMain(m *testing.M) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		// Fail loudly rather than silently skipping: the Makefile / CI must set
		// KUBEBUILDER_ASSETS (via `setup-envtest use`).
		println("KUBEBUILDER_ASSETS is not set; run via `make test-e2e` (installs envtest binaries via setup-envtest)")
		os.Exit(1)
	}
	testEnv = &envtest.Environment{}
	cfg, err := testEnv.Start()
	if err != nil {
		println("failed to start envtest:", err.Error())
		os.Exit(1)
	}
	adminCfg = cfg
	code := m.Run()
	_ = testEnv.Stop()
	os.Exit(code)
}

func adminClient(t *testing.T) *kubernetes.Clientset {
	t.Helper()
	cs, err := kubernetes.NewForConfig(adminCfg)
	if err != nil {
		t.Fatalf("admin client: %v", err)
	}
	return cs
}

// mintToken creates a ServiceAccount (if missing) and returns a short-lived
// bound token via the TokenRequest API — exactly how an operator would provision
// credentials for the MCP.
func mintToken(t *testing.T, cs *kubernetes.Clientset, ns, sa string) string {
	t.Helper()
	ctx := context.Background()
	_, _ = cs.CoreV1().ServiceAccounts(ns).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: sa, Namespace: ns},
	}, metav1.CreateOptions{})
	exp := int64(3600)
	tr, err := cs.CoreV1().ServiceAccounts(ns).CreateToken(ctx, sa, &authnv1.TokenRequest{
		Spec: authnv1.TokenRequestSpec{ExpirationSeconds: &exp},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("mint token for %s/%s: %v", ns, sa, err)
	}
	return tr.Status.Token
}

func ensureNamespace(t *testing.T, cs *kubernetes.Clientset, name string) {
	t.Helper()
	_, err := cs.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}, metav1.CreateOptions{})
	if err != nil && !isAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", name, err)
	}
}

func isAlreadyExists(err error) bool {
	return err != nil && (containsAny(err.Error(), "already exists"))
}

func containsAny(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// caData returns the API server CA the admin config trusts, base64-encoded for
// ClusterConfig.CertificateAuthorityData.
func caData(t *testing.T) string {
	t.Helper()
	if len(adminCfg.CAData) == 0 {
		t.Fatal("envtest admin config has no CAData")
	}
	return base64.StdEncoding.EncodeToString(adminCfg.CAData)
}

// startMCP builds and serves a kubernetes-mcp instance for one token-scoped
// cluster, returning a connected MCP client session. Everything is torn down via
// t.Cleanup.
func startMCP(t *testing.T, clusterName, token string, clusterReadOnly, globalReadOnly bool) *mcp.ClientSession {
	t.Helper()

	cc := config.ClusterConfig{
		Name:                     clusterName,
		Server:                   adminCfg.Host,
		CertificateAuthorityData: caData(t),
		Token:                    token,
		ReadOnly:                 clusterReadOnly,
	}
	cfg := &config.Config{
		LogLevel:       "error",
		ReadOnly:       globalReadOnly,
		DefaultCluster: clusterName,
		Clusters:       []config.ClusterConfig{cc},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	reg, err := clusters.Build(cfg)
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	srv := mcpserver.New(reg, cfg)
	mcpSrv := srv.MCPServer()

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-test", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// callText calls a tool and returns (text, isError).
func callText(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var out string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			out += tc.Text
		}
	}
	return out, res.IsError
}

// grantClusterRole binds a ClusterRole to a ServiceAccount cluster-wide.
func grantClusterRole(t *testing.T, cs *kubernetes.Clientset, sa, ns, clusterRole, bindingName string) {
	t.Helper()
	_, err := cs.RbacV1().ClusterRoleBindings().Create(context.Background(), &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: bindingName},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: clusterRole},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: sa, Namespace: ns}},
	}, metav1.CreateOptions{})
	if err != nil && !isAlreadyExists(err) {
		t.Fatalf("bind %s: %v", clusterRole, err)
	}
}
