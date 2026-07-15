package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/coreos/go-oidc/v3/oidc"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/config"
)

// oidcVerifier validates OIDC/OAuth 2.1 JWT access tokens from an OIDC provider
// (Authentik / Keycloak). Signature, issuer and expiry are checked by go-oidc
// against the provider's JWKS; audience, required scopes and required groups are
// enforced on top. The verified username is surfaced as TokenInfo.UserID.
type oidcVerifier struct {
	verifier       *oidc.IDTokenVerifier
	audience       string
	requiredScopes []string
	requiredGroups []string
	groupsClaim    string
	usernameClaim  string
}

func newOIDCVerifier(ctx context.Context, cfg config.AuthOIDC) (*oidcVerifier, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("auth.oidc: discover issuer %q: %w", cfg.Issuer, err)
	}
	// ClientID here is the expected audience (RFC 8707 resource indicator).
	oidcCfg := &oidc.Config{ClientID: cfg.Audience}
	verifier := provider.VerifierContext(ctx, oidcCfg)
	if cfg.JWKSURL != "" {
		// Explicit JWKS override (e.g. when discovery is unavailable/mismatched).
		ks := oidc.NewRemoteKeySet(ctx, cfg.JWKSURL)
		verifier = oidc.NewVerifier(cfg.Issuer, ks, oidcCfg)
	}
	return &oidcVerifier{
		verifier:       verifier,
		audience:       cfg.Audience,
		requiredScopes: cfg.RequiredScopes,
		requiredGroups: cfg.RequiredGroups,
		groupsClaim:    cfg.GroupsClaim,
		usernameClaim:  cfg.UsernameClaim,
	}, nil
}

// verify implements auth.TokenVerifier.
func (v *oidcVerifier) verify(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
	idt, err := v.verifier.Verify(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("oidc token invalid: %w", err)
	}

	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		return nil, fmt.Errorf("oidc token claims: %w", err)
	}

	scopes := scopeSet(claims)
	if err := requireAll(v.requiredScopes, scopes, "scope"); err != nil {
		return nil, err
	}
	if len(v.requiredGroups) > 0 {
		groups := stringSet(claims[v.groupsClaim])
		if err := requireAll(v.requiredGroups, groups, "group"); err != nil {
			return nil, err
		}
	}

	user := idt.Subject
	if v.usernameClaim != "" {
		if s, ok := claims[v.usernameClaim].(string); ok && s != "" {
			user = s
		}
	}

	return &sdkauth.TokenInfo{
		Scopes:     keys(scopes),
		Expiration: idt.Expiry,
		UserID:     user,
		Extra:      claims,
	}, nil
}

// scopeSet extracts scopes from the "scope" (space-delimited string) or "scp"
// (array) claim, as issued by different providers.
func scopeSet(claims map[string]any) map[string]bool {
	set := map[string]bool{}
	if s, ok := claims["scope"].(string); ok {
		for _, sc := range splitSpace(s) {
			set[sc] = true
		}
	}
	for _, sc := range stringSlice(claims["scp"]) {
		set[sc] = true
	}
	return set
}

func stringSet(v any) map[string]bool {
	set := map[string]bool{}
	for _, s := range stringSlice(v) {
		set[s] = true
	}
	return set
}

func stringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{t}
	default:
		return nil
	}
}

func splitSpace(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func requireAll(required []string, present map[string]bool, kind string) error {
	for _, r := range required {
		if !present[r] {
			return fmt.Errorf("oidc token missing required %s %q", kind, r)
		}
	}
	return nil
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
