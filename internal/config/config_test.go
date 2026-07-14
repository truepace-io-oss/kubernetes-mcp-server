package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValidMultiCluster(t *testing.T) {
	p := writeTemp(t, `
listenAddr: "127.0.0.1:9999"
logLevel: "debug"
defaultCluster: local
clusters:
  - name: local
    inCluster: true
  - name: customer-a
    server: https://api.example.com:6443
    certificateAuthorityData: Zm9v
    tokenFile: /etc/kmcp/token
    readOnly: true
  - name: homelab
    kubeconfigFile: /etc/kmcp/kubeconfig
    context: homelab
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:9999" || cfg.LogLevel != "debug" {
		t.Fatalf("scalars not parsed: %+v", cfg)
	}
	if len(cfg.Clusters) != 3 || cfg.DefaultCluster != "local" {
		t.Fatalf("clusters not parsed: %+v", cfg)
	}
	if m, err := cfg.Clusters[1].Mode(); err != nil || m != authExplicit {
		t.Fatalf("customer-a mode = %v, %v", m, err)
	}
}

func TestSingleClusterDefaultInferred(t *testing.T) {
	p := writeTemp(t, `
clusters:
  - name: only
    inCluster: true
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultCluster != "only" {
		t.Fatalf("default not inferred: %q", cfg.DefaultCluster)
	}
	if cfg.ListenAddr != "0.0.0.0:9090" || cfg.LogLevel != "info" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]string{
		"no clusters": `defaultCluster: x`,
		"bad loglevel": `
logLevel: verbose
clusters: [{name: a, inCluster: true}]`,
		"dup name": `
defaultCluster: a
clusters:
  - {name: a, inCluster: true}
  - {name: a, inCluster: true}`,
		"no auth mode": `
clusters: [{name: a}]`,
		"multiple auth modes": `
clusters: [{name: a, inCluster: true, server: https://x:6443, tokenFile: /t, certificateAuthorityData: zz}]`,
		"explicit without ca": `
clusters: [{name: a, server: https://x:6443, tokenFile: /t}]`,
		"explicit without token": `
clusters: [{name: a, server: https://x:6443, certificateAuthorityData: zz}]`,
		"kubeconfig without context": `
clusters: [{name: a, kubeconfigFile: /kc}]`,
		"bad name": `
clusters: [{name: "Bad_Name", inCluster: true}]`,
		"default not present": `
defaultCluster: nope
clusters:
  - {name: a, inCluster: true}
  - {name: b, inCluster: true}`,
		"insecure with ca": `
clusters: [{name: a, server: https://x:6443, tokenFile: /t, insecureSkipTLSVerify: true, certificateAuthorityData: zz}]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := writeTemp(t, body)
			if _, err := Load(p); err == nil {
				t.Fatalf("expected validation error for %q", name)
			}
		})
	}
}

func TestEnvOverrides(t *testing.T) {
	p := writeTemp(t, `
listenAddr: "0.0.0.0:8080"
logLevel: info
readOnly: false
defaultCluster: local
clusters: [{name: local, inCluster: true}]`)
	t.Setenv("KMCP_LISTEN_ADDR", "0.0.0.0:1234")
	t.Setenv("KMCP_LOG_LEVEL", "warn")
	t.Setenv("KMCP_READ_ONLY", "true")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "0.0.0.0:1234" || cfg.LogLevel != "warn" || !cfg.ReadOnly {
		t.Fatalf("env overrides not applied: %+v", cfg)
	}
}

func TestWarnings(t *testing.T) {
	cfg := &Config{
		LogLevel:       "info",
		DefaultCluster: "a",
		Clusters: []ClusterConfig{{
			Name:                     "a",
			Server:                   "https://x:6443",
			Token:                    "inline",
			TokenFile:                "/t",
			CertificateAuthorityData: "zz",
		}},
	}
	if got := cfg.Warnings(); len(got) < 2 {
		t.Fatalf("expected warnings for inline token + precedence, got %v", got)
	}
}
