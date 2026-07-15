package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/config"
)

// mockIssuer is an httptest OIDC provider: it serves discovery + JWKS and mints
// RS256 tokens signed with its key, so the verifier can be exercised without a
// real Authentik/Keycloak.
type mockIssuer struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	iss    string
	signer jose.Signer
}

func newMockIssuer(t *testing.T) *mockIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	m := &mockIssuer{key: key, kid: "test-key"}

	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", m.kid),
	)
	if err != nil {
		t.Fatal(err)
	}
	m.signer = sig

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 m.iss,
			"jwks_uri":               m.iss + "/jwks",
			"authorization_endpoint": m.iss + "/authorize",
			"token_endpoint":         m.iss + "/token",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: key.Public(), KeyID: m.kid, Algorithm: "RS256", Use: "sig",
		}}}
		_ = json.NewEncoder(w).Encode(jwks)
	})
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	m.iss = m.srv.URL
	return m
}

func (m *mockIssuer) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	raw, err := jwt.Signed(m.signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestOIDCVerifier(t *testing.T) {
	m := newMockIssuer(t)
	const aud = "https://kmcp.example.com"

	v, err := newOIDCVerifier(context.Background(), config.AuthOIDC{
		Enabled:        true,
		Issuer:         m.iss,
		Audience:       aud,
		RequiredScopes: []string{"mcp.access"},
		RequiredGroups: []string{"k8s-admins"},
		GroupsClaim:    "groups",
		UsernameClaim:  "preferred_username",
	})
	if err != nil {
		t.Fatalf("build verifier: %v", err)
	}

	base := func() map[string]any {
		return map[string]any{
			"iss":                m.iss,
			"aud":                aud,
			"sub":                "user-123",
			"preferred_username": "alice",
			"exp":                time.Now().Add(time.Hour).Unix(),
			"iat":                time.Now().Unix(),
			"scope":              "openid mcp.access",
			"groups":             []string{"k8s-admins", "devs"},
		}
	}

	t.Run("valid", func(t *testing.T) {
		info, err := v.verify(context.Background(), m.mint(t, base()), nil)
		if err != nil {
			t.Fatalf("valid token rejected: %v", err)
		}
		if info.UserID != "alice" {
			t.Fatalf("username claim not mapped: %q", info.UserID)
		}
	})

	t.Run("wrong audience", func(t *testing.T) {
		c := base()
		c["aud"] = "https://someone-else"
		if _, err := v.verify(context.Background(), m.mint(t, c), nil); err == nil {
			t.Fatal("expected audience rejection")
		}
	})

	t.Run("expired", func(t *testing.T) {
		c := base()
		c["exp"] = time.Now().Add(-time.Minute).Unix()
		if _, err := v.verify(context.Background(), m.mint(t, c), nil); err == nil {
			t.Fatal("expected expiry rejection")
		}
	})

	t.Run("missing scope", func(t *testing.T) {
		c := base()
		c["scope"] = "openid"
		if _, err := v.verify(context.Background(), m.mint(t, c), nil); err == nil {
			t.Fatal("expected missing-scope rejection")
		}
	})

	t.Run("missing group", func(t *testing.T) {
		c := base()
		c["groups"] = []string{"devs"}
		if _, err := v.verify(context.Background(), m.mint(t, c), nil); err == nil {
			t.Fatal("expected missing-group rejection")
		}
	})
}
