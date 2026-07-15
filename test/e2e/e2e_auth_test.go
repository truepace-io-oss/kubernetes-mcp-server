package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/auth"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/clusters"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/config"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/mcpserver"
)

// bearerRT injects an Authorization header on every request so the MCP client
// can present a token over the streamable-HTTP transport.
type bearerRT struct {
	token string
	base  http.RoundTripper
}

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.base.RoundTrip(r)
}

// startAuthMCP serves an MCP instance (targeting envtest with a reader token)
// wrapped in the given auth config, mirroring main.go's wiring. Returns the base
// URL.
func startAuthMCP(t *testing.T, ctx context.Context, clusterToken string, authCfg config.Auth) string {
	t.Helper()
	cfg := &config.Config{
		LogLevel:       "error",
		DefaultCluster: "envtest",
		Clusters: []config.ClusterConfig{{
			Name:                     "envtest",
			Server:                   adminCfg.Host,
			CertificateAuthorityData: caData(t),
			Token:                    clusterToken,
		}},
		Auth: authCfg,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	reg, err := clusters.Build(cfg)
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	authn, err := auth.Build(ctx, cfg.Auth)
	if err != nil {
		t.Fatalf("build auth: %v", err)
	}
	mcpSrv := mcpserver.New(reg, cfg).MCPServer()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil)

	mux := http.NewServeMux()
	mux.Handle("/mcp", authn.Middleware(handler))
	if authn.MetadataHandler != nil {
		mux.Handle(authn.MetadataPath, authn.MetadataHandler)
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL
}

// connectWithToken attempts an MCP session presenting the given bearer token
// (empty = none). Returns the session or the connect error.
func connectWithToken(t *testing.T, baseURL, token string) (*mcp.ClientSession, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "auth-e2e", Version: "0"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   baseURL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRT{token: token, base: http.DefaultTransport}},
	}, nil)
}

// readerToken provisions a read-only SA in envtest and returns its token.
func readerToken(t *testing.T, ns, sa, binding string) string {
	cs := adminClient(t)
	ensureNamespace(t, cs, ns)
	createReaderClusterRole(t, cs, "e2e-reader")
	tok := mintToken(t, cs, ns, sa)
	grantClusterRole(t, cs, sa, ns, "e2e-reader", binding)
	return tok
}

func TestE2EAuthStatic(t *testing.T) {
	clusterTok := readerToken(t, "e2e-auth-static", "mcp-sa", "e2e-auth-static-binding")
	base := startAuthMCP(t, context.Background(), clusterTok, config.Auth{
		Enabled: true,
		Static: config.AuthStatic{
			Enabled: true,
			Tokens:  []config.AuthToken{{Name: "e2e", Token: "the-secret"}},
		},
	})

	// No token → the initialize handshake is rejected (401).
	if _, err := connectWithToken(t, base, ""); err == nil {
		t.Fatal("expected connect to fail without a token")
	}
	// Wrong token → rejected.
	if _, err := connectWithToken(t, base, "nope"); err == nil {
		t.Fatal("expected connect to fail with a wrong token")
	}
	// Correct token → session works and a tool call succeeds.
	sess, err := connectWithToken(t, base, "the-secret")
	if err != nil {
		t.Fatalf("connect with valid token: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	out, isErr := callText(t, sess, "namespaces_list", map[string]any{})
	if isErr || !strings.Contains(out, "e2e-auth-static") {
		t.Fatalf("authenticated tool call failed: err=%v out=%s", isErr, out)
	}
}

func TestE2EAuthOIDC(t *testing.T) {
	iss := newE2EMockIssuer(t)
	const aud = "https://kmcp.e2e"
	clusterTok := readerToken(t, "e2e-auth-oidc", "mcp-sa2", "e2e-auth-oidc-binding")

	// go-oidc must trust the mock issuer's self-signed TLS cert during discovery.
	ctx := oidc.ClientContext(context.Background(), iss.client)
	base := startAuthMCP(t, ctx, clusterTok, config.Auth{
		Enabled: true,
		OIDC: config.AuthOIDC{
			Enabled:  true,
			Issuer:   iss.url,
			Audience: aud,
		},
	})

	// The protected-resource metadata endpoint advertises the issuer.
	resp, err := http.Get(base + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatalf("metadata GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), iss.url) || !strings.Contains(string(body), aud) {
		t.Fatalf("metadata missing issuer/audience: %s", body)
	}

	// No token → rejected.
	if _, err := connectWithToken(t, base, ""); err == nil {
		t.Fatal("expected OIDC connect to fail without a token")
	}

	// Valid JWT → session works.
	tok := iss.mint(t, map[string]any{
		"iss": iss.url, "aud": aud, "sub": "u1",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
	})
	sess, err := connectWithToken(t, base, tok)
	if err != nil {
		t.Fatalf("connect with valid JWT: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	if out, isErr := callText(t, sess, "namespaces_list", map[string]any{}); isErr || !strings.Contains(out, "e2e-auth-oidc") {
		t.Fatalf("OIDC tool call failed: err=%v out=%s", isErr, out)
	}

	// Wrong-audience JWT → rejected.
	bad := iss.mint(t, map[string]any{
		"iss": iss.url, "aud": "https://other", "sub": "u1",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
	})
	if _, err := connectWithToken(t, base, bad); err == nil {
		t.Fatal("expected rejection of wrong-audience token")
	}
}

// --- minimal mock OIDC issuer for E2E ---

type e2eMockIssuer struct {
	url    string
	signer jose.Signer
	client *http.Client
}

func newE2EMockIssuer(t *testing.T) *e2eMockIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid = "e2e"
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid))
	if err != nil {
		t.Fatal(err)
	}
	m := &e2eMockIssuer{signer: sig}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": m.url, "jwks_uri": m.url + "/jwks",
			"authorization_endpoint": m.url + "/a", "token_endpoint": m.url + "/t",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: key.Public(), KeyID: kid, Algorithm: "RS256", Use: "sig",
		}}})
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	m.url = srv.URL
	m.client = srv.Client()
	return m
}

func (m *e2eMockIssuer) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	raw, err := jwt.Signed(m.signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
