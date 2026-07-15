// Package auth authenticates the AI agent to the MCP server (the client-side
// link). It supports shared static bearer tokens and OIDC/OAuth 2.1 access
// tokens (Authentik / Keycloak), independently or together. Authorization to the
// cluster remains a separate concern (ServiceAccount + Kubernetes RBAC): this
// only decides who may call the MCP at all.
package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/config"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/metrics"
)

// Middleware wraps an http.Handler with authentication.
type Middleware func(http.Handler) http.Handler

// Built holds the results of Build: the middleware to wrap /mcp, and (in OIDC
// mode) an optional handler for the protected-resource-metadata endpoint.
type Built struct {
	Middleware      Middleware
	MetadataPath    string       // "" when no metadata endpoint should be served
	MetadataHandler http.Handler // nil when MetadataPath == ""
	Description     string       // human-readable summary for startup logging (no secrets)
}

// resourceMetadataPath is the well-known path defined by RFC 9728.
const resourceMetadataPath = "/.well-known/oauth-protected-resource"

// Build constructs the authentication middleware from config. When auth is
// disabled it returns a pass-through middleware so callers need no branching.
// The context is used for OIDC issuer discovery; callers may attach a custom
// HTTP client via oidc.ClientContext (e.g. tests).
func Build(ctx context.Context, cfg config.Auth) (*Built, error) {
	if !cfg.Enabled {
		return &Built{Middleware: passthrough, Description: "disabled"}, nil
	}

	var verifiers []namedVerifier
	desc := ""

	if cfg.Static.Enabled {
		sv, err := newStaticVerifier(cfg.Static)
		if err != nil {
			return nil, err
		}
		verifiers = append(verifiers, namedVerifier{"static", sv.verify})
		desc = "static"
	}

	var opts auth.RequireBearerTokenOptions
	var metaHandler http.Handler
	metaPath := ""

	if cfg.OIDC.Enabled {
		ov, err := newOIDCVerifier(ctx, cfg.OIDC)
		if err != nil {
			return nil, err
		}
		verifiers = append(verifiers, namedVerifier{"oidc", ov.verify})
		opts.Scopes = cfg.OIDC.RequiredScopes
		if desc == "" {
			desc = "oidc"
		} else {
			desc += "+oidc"
		}
		if cfg.OIDC.ServeResourceMetadata() {
			metaPath = resourceMetadataPath
			opts.ResourceMetadataURL = resourceMetadataPath
			md := &oauthex.ProtectedResourceMetadata{
				Resource:               cfg.OIDC.Audience,
				AuthorizationServers:   []string{cfg.OIDC.Issuer},
				BearerMethodsSupported: []string{"header"},
				ScopesSupported:        cfg.OIDC.RequiredScopes,
				ResourceName:           "kubernetes-mcp",
			}
			metaHandler = auth.ProtectedResourceMetadataHandler(md)
		}
	}

	if len(verifiers) == 0 {
		return nil, fmt.Errorf("auth enabled but no verifier could be built")
	}

	mw := auth.RequireBearerToken(chain(verifiers), &opts)
	return &Built{
		Middleware:      Middleware(mw),
		MetadataPath:    metaPath,
		MetadataHandler: metaHandler,
		Description:     desc,
	}, nil
}

func passthrough(next http.Handler) http.Handler { return next }

// namedVerifier pairs a verifier with a method name for metrics.
type namedVerifier struct {
	name   string
	verify auth.TokenVerifier
}

// chain returns a verifier that accepts a token if any of the given verifiers
// accepts it, recording the auth outcome. It returns the last error otherwise,
// so the message reflects the most specific (typically OIDC) failure.
func chain(verifiers []namedVerifier) auth.TokenVerifier {
	return func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		var lastErr error
		for _, v := range verifiers {
			info, err := v.verify(ctx, token, req)
			if err == nil {
				metrics.RecordAuth(v.name, "allow")
				return info, nil
			}
			lastErr = err
		}
		metrics.RecordAuth("none", "deny")
		if lastErr == nil {
			lastErr = auth.ErrInvalidToken
		}
		return nil, lastErr
	}
}
