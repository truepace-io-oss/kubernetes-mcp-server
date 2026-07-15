// Package config loads and validates the kubernetes-mcp server configuration:
// the listen address, logging, a global read-only kill-switch, and the registry
// of Kubernetes clusters this instance manages. Authentication to every cluster
// is expressed purely as ServiceAccount credentials (in-cluster token, an
// explicit token+CA, or a kubeconfig context) — the server contains no auth
// logic of its own; Kubernetes RBAC is the only authorization gate.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

// Config is the top-level server configuration.
type Config struct {
	ListenAddr     string          `json:"listenAddr"`
	LogLevel       string          `json:"logLevel"`
	ReadOnly       bool            `json:"readOnly"`
	DefaultCluster string          `json:"defaultCluster"`
	Clusters       []ClusterConfig `json:"clusters"`
	Auth           Auth            `json:"auth"`
}

// Auth configures how the AI agent authenticates to this MCP server (the
// client-side link). It is independent of cluster authentication: this decides
// who may talk to the MCP, while ServiceAccount tokens + RBAC decide what the MCP
// may do in a cluster. When disabled, the transport is unauthenticated and must
// be protected by the deployment (e.g. an internal ingress).
type Auth struct {
	Enabled bool       `json:"enabled"`
	Static  AuthStatic `json:"static"`
	OIDC    AuthOIDC   `json:"oidc"`
}

// AuthStatic configures one or more shared bearer tokens.
type AuthStatic struct {
	Enabled bool        `json:"enabled"`
	Tokens  []AuthToken `json:"tokens"`
}

// AuthToken is a single shared bearer token. Exactly one of Token/TokenFile.
type AuthToken struct {
	Name      string `json:"name"`
	Token     string `json:"token,omitempty"`     // inline, discouraged
	TokenFile string `json:"tokenFile,omitempty"` // preferred (ESO / rotatable)
}

// AuthOIDC configures the MCP as an OAuth 2.1 resource server validating JWT
// access tokens from an OIDC provider (Authentik / Keycloak).
type AuthOIDC struct {
	Enabled        bool     `json:"enabled"`
	Issuer         string   `json:"issuer"`
	Audience       string   `json:"audience"`
	JWKSURL        string   `json:"jwksUrl,omitempty"`
	RequiredScopes []string `json:"requiredScopes,omitempty"`
	RequiredGroups []string `json:"requiredGroups,omitempty"`
	GroupsClaim    string   `json:"groupsClaim,omitempty"`
	UsernameClaim  string   `json:"usernameClaim,omitempty"`
	// ResourceMetadata controls serving /.well-known/oauth-protected-resource.
	// Defaults to true (enabled) when unset.
	ResourceMetadata *bool `json:"resourceMetadata,omitempty"`
}

// ServeResourceMetadata reports whether the protected-resource-metadata endpoint
// should be served (defaults to true).
func (o AuthOIDC) ServeResourceMetadata() bool {
	return o.ResourceMetadata == nil || *o.ResourceMetadata
}

// ClusterConfig describes how to reach and authenticate to one cluster.
// Exactly one authentication mode must be selected:
//   - InCluster: use the pod's projected ServiceAccount token (the cluster the
//     server runs in).
//   - Server + CA + Token: an explicit remote cluster reached with a SA token.
//   - KubeconfigFile + Context: a context from a mounted kubeconfig.
type ClusterConfig struct {
	Name string `json:"name"`

	// Mode (a): in-cluster ServiceAccount.
	InCluster bool `json:"inCluster,omitempty"`

	// Mode (b): explicit remote cluster.
	Server                   string `json:"server,omitempty"`
	CertificateAuthorityFile string `json:"certificateAuthorityFile,omitempty"`
	CertificateAuthorityData string `json:"certificateAuthorityData,omitempty"` // base64 (PEM) as in kubeconfig
	TokenFile                string `json:"tokenFile,omitempty"`
	Token                    string `json:"token,omitempty"` // inline, discouraged
	InsecureSkipTLSVerify    bool   `json:"insecureSkipTLSVerify,omitempty"`

	// Mode (c): kubeconfig context.
	KubeconfigFile string `json:"kubeconfigFile,omitempty"`
	Context        string `json:"context,omitempty"`

	// Behaviour.
	ReadOnly         bool   `json:"readOnly,omitempty"`
	DefaultNamespace string `json:"defaultNamespace,omitempty"`
}

// authMode is an internal enum of the selected authentication mode.
type authMode int

const (
	authNone authMode = iota
	authInCluster
	authExplicit
	authKubeconfig
)

// Mode reports which authentication mode this cluster uses and whether the
// selection is unambiguous.
func (c ClusterConfig) Mode() (authMode, error) {
	var modes []authMode
	if c.InCluster {
		modes = append(modes, authInCluster)
	}
	if c.Server != "" {
		modes = append(modes, authExplicit)
	}
	if c.KubeconfigFile != "" {
		modes = append(modes, authKubeconfig)
	}
	switch len(modes) {
	case 0:
		return authNone, fmt.Errorf("cluster %q: no authentication mode set (need one of inCluster / server / kubeconfigFile)", c.Name)
	case 1:
		return modes[0], nil
	default:
		return authNone, fmt.Errorf("cluster %q: multiple authentication modes set; choose exactly one of inCluster / server / kubeconfigFile", c.Name)
	}
}

var nameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Load reads config from path (if non-empty), applies KMCP_* environment
// overrides, fills defaults and validates the result.
func Load(path string) (*Config, error) {
	cfg := &Config{}
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config %q: %w", path, err)
		}
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("parse config %q: %w", path, err)
		}
	}
	cfg.applyEnv()
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyEnv() {
	if v := os.Getenv("KMCP_LISTEN_ADDR"); v != "" {
		c.ListenAddr = v
	}
	if v := os.Getenv("KMCP_LOG_LEVEL"); v != "" {
		c.LogLevel = v
	}
	if v := os.Getenv("KMCP_DEFAULT_CLUSTER"); v != "" {
		c.DefaultCluster = v
	}
	if v := os.Getenv("KMCP_READ_ONLY"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.ReadOnly = b
		}
	}
	if v := os.Getenv("KMCP_AUTH_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.Auth.Enabled = b
		}
	}
	if v := os.Getenv("KMCP_AUTH_STATIC_TOKEN"); v != "" {
		c.Auth.Static.Enabled = true
		c.Auth.Static.Tokens = append(c.Auth.Static.Tokens, AuthToken{Name: "env", Token: v})
	}
	if v := os.Getenv("KMCP_AUTH_OIDC_ISSUER"); v != "" {
		c.Auth.OIDC.Enabled = true
		c.Auth.OIDC.Issuer = v
	}
	if v := os.Getenv("KMCP_AUTH_OIDC_AUDIENCE"); v != "" {
		c.Auth.OIDC.Audience = v
	}
}

func (c *Config) applyDefaults() {
	if c.ListenAddr == "" {
		c.ListenAddr = "0.0.0.0:9090"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	// If exactly one cluster is defined and no default is set, use it.
	if c.DefaultCluster == "" && len(c.Clusters) == 1 {
		c.DefaultCluster = c.Clusters[0].Name
	}
	if c.Auth.OIDC.GroupsClaim == "" {
		c.Auth.OIDC.GroupsClaim = "groups"
	}
	if c.Auth.OIDC.UsernameClaim == "" {
		c.Auth.OIDC.UsernameClaim = "preferred_username"
	}
}

// Validate enforces the invariants documented on Config/ClusterConfig.
// It returns the first violation found. Non-fatal normalisations (file-vs-inline
// precedence) are handled at credential-build time, not here.
func (c *Config) Validate() error {
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid logLevel %q (want debug|info|warn|error)", c.LogLevel)
	}
	if len(c.Clusters) == 0 {
		return fmt.Errorf("no clusters configured")
	}

	seen := map[string]bool{}
	for _, cl := range c.Clusters {
		if cl.Name == "" {
			return fmt.Errorf("cluster with empty name")
		}
		if !nameRe.MatchString(cl.Name) {
			return fmt.Errorf("cluster %q: name must be a DNS label (lowercase alphanumeric and '-')", cl.Name)
		}
		if seen[cl.Name] {
			return fmt.Errorf("duplicate cluster name %q", cl.Name)
		}
		seen[cl.Name] = true

		mode, err := cl.Mode()
		if err != nil {
			return err
		}
		switch mode {
		case authExplicit:
			if cl.CertificateAuthorityFile == "" && cl.CertificateAuthorityData == "" && !cl.InsecureSkipTLSVerify {
				return fmt.Errorf("cluster %q: server set but no certificateAuthorityFile/Data and insecureSkipTLSVerify is false", cl.Name)
			}
			if cl.TokenFile == "" && cl.Token == "" {
				return fmt.Errorf("cluster %q: server set but no tokenFile/token provided", cl.Name)
			}
			if cl.InsecureSkipTLSVerify && (cl.CertificateAuthorityFile != "" || cl.CertificateAuthorityData != "") {
				return fmt.Errorf("cluster %q: insecureSkipTLSVerify must not be combined with a CA", cl.Name)
			}
		case authKubeconfig:
			if cl.Context == "" {
				return fmt.Errorf("cluster %q: kubeconfigFile set but no context selected", cl.Name)
			}
		}
	}

	if c.DefaultCluster == "" {
		return fmt.Errorf("defaultCluster must be set when more than one cluster is configured")
	}
	if !seen[c.DefaultCluster] {
		return fmt.Errorf("defaultCluster %q is not one of the configured clusters", c.DefaultCluster)
	}

	if err := c.Auth.validate(); err != nil {
		return err
	}
	return nil
}

func (a Auth) validate() error {
	if !a.Enabled {
		return nil
	}
	if !a.Static.Enabled && !a.OIDC.Enabled {
		return fmt.Errorf("auth.enabled is true but neither auth.static nor auth.oidc is enabled")
	}
	if a.Static.Enabled {
		if len(a.Static.Tokens) == 0 {
			return fmt.Errorf("auth.static.enabled is true but no tokens configured")
		}
		names := map[string]bool{}
		for i, t := range a.Static.Tokens {
			if t.Name == "" {
				return fmt.Errorf("auth.static.tokens[%d]: name is required", i)
			}
			if names[t.Name] {
				return fmt.Errorf("auth.static.tokens: duplicate name %q", t.Name)
			}
			names[t.Name] = true
			if (t.Token == "") == (t.TokenFile == "") {
				return fmt.Errorf("auth.static.tokens[%q]: set exactly one of token or tokenFile", t.Name)
			}
		}
	}
	if a.OIDC.Enabled {
		if a.OIDC.Issuer == "" {
			return fmt.Errorf("auth.oidc.enabled is true but issuer is empty")
		}
		if !strings.HasPrefix(a.OIDC.Issuer, "https://") {
			return fmt.Errorf("auth.oidc.issuer must be an https URL")
		}
		if a.OIDC.Audience == "" {
			return fmt.Errorf("auth.oidc.enabled is true but audience is empty")
		}
	}
	return nil
}

// Warnings returns non-fatal advisories (loud but not blocking), e.g. inline
// secrets or insecure TLS. Callers log these at startup.
func (c *Config) Warnings() []string {
	var w []string
	for _, cl := range c.Clusters {
		if cl.Token != "" && cl.TokenFile != "" {
			w = append(w, fmt.Sprintf("cluster %q: both token and tokenFile set; tokenFile takes precedence", cl.Name))
		}
		if cl.CertificateAuthorityData != "" && cl.CertificateAuthorityFile != "" {
			w = append(w, fmt.Sprintf("cluster %q: both certificateAuthorityData and certificateAuthorityFile set; file takes precedence", cl.Name))
		}
		if cl.Token != "" {
			w = append(w, fmt.Sprintf("cluster %q: inline token is discouraged; prefer tokenFile (ESO/projected token, auto-reloaded)", cl.Name))
		}
		if cl.InsecureSkipTLSVerify {
			w = append(w, fmt.Sprintf("cluster %q: insecureSkipTLSVerify=true — TLS verification disabled, do not use in production", cl.Name))
		}
	}
	if c.Auth.Static.Enabled {
		for _, t := range c.Auth.Static.Tokens {
			if t.Token != "" {
				w = append(w, fmt.Sprintf("auth.static.tokens[%q]: inline token is discouraged; prefer tokenFile (ESO/rotatable)", t.Name))
			}
		}
	}
	if c.Auth.Enabled {
		w = append(w, "auth is enabled — ensure the transport is TLS-protected (internal ingress or server TLS); bearer tokens over plaintext are insecure")
	}
	return w
}

// ClusterNames returns the configured cluster names in order.
func (c *Config) ClusterNames() []string {
	names := make([]string, 0, len(c.Clusters))
	for _, cl := range c.Clusters {
		names = append(names, cl.Name)
	}
	return names
}

// String redacts secrets for safe logging.
func (c ClusterConfig) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "name=%s", c.Name)
	switch {
	case c.InCluster:
		b.WriteString(" mode=in-cluster")
	case c.Server != "":
		fmt.Fprintf(&b, " mode=explicit server=%s", c.Server)
	case c.KubeconfigFile != "":
		fmt.Fprintf(&b, " mode=kubeconfig context=%s", c.Context)
	}
	fmt.Fprintf(&b, " readOnly=%t", c.ReadOnly)
	return b.String()
}
