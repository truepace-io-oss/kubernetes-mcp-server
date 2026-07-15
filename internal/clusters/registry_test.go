package clusters

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/config"
)

// fakeAPIServer returns a TLS test server that answers the /version endpoint,
// plus the base64-encoded PEM of its own CA so a cluster can be configured to
// trust it.
func fakeAPIServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			_ = json.NewEncoder(w).Encode(map[string]string{"gitVersion": "v1.34.0-test"})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	caPEM := pemForCert(t, srv)
	return srv, base64.StdEncoding.EncodeToString(caPEM)
}

func TestBuildAndAccessors(t *testing.T) {
	srv, caB64 := fakeAPIServer(t)

	cfg := &config.Config{
		LogLevel:       "info",
		DefaultCluster: "primary",
		Clusters: []config.ClusterConfig{
			{Name: "primary", Server: srv.URL, CertificateAuthorityData: caB64, Token: "t", ReadOnly: false},
			{Name: "secondary", Server: srv.URL, CertificateAuthorityData: caB64, Token: "t", ReadOnly: true},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}

	reg, err := Build(cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if got := reg.Names(); len(got) != 2 {
		t.Fatalf("names = %v", got)
	}
	if reg.DefaultName() != "primary" || reg.Default().Name != "primary" {
		t.Fatalf("default wrong: %q", reg.DefaultName())
	}

	// Empty name resolves to the default.
	def, err := reg.Get("")
	if err != nil || def.Name != "primary" {
		t.Fatalf("Get(\"\") = %v, %v", def, err)
	}

	sec, err := reg.Get("secondary")
	if err != nil {
		t.Fatalf("Get(secondary): %v", err)
	}
	if !sec.ReadOnly {
		t.Fatalf("secondary should be read-only")
	}

	if _, err := reg.Get("nope"); err == nil {
		t.Fatalf("expected error for unknown cluster")
	}

	// Ping hits the fake /version endpoint over TLS with the CA we trust.
	ver, err := reg.Default().Ping(context.Background())
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if ver != "v1.34.0-test" {
		t.Fatalf("version = %q", ver)
	}
}
