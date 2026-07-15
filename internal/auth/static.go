package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/config"
)

// staticVerifier checks a presented token against a set of configured shared
// tokens using a constant-time comparison. File-backed tokens are re-read on
// each verification so rotation (ESO / projected files) is picked up without a
// restart.
type staticVerifier struct {
	mu     sync.RWMutex
	inline map[string]string // name -> token (from config)
	files  map[string]string // name -> file path
}

func newStaticVerifier(cfg config.AuthStatic) (*staticVerifier, error) {
	v := &staticVerifier{inline: map[string]string{}, files: map[string]string{}}
	for _, t := range cfg.Tokens {
		switch {
		case t.TokenFile != "":
			// Validate readability early for a clear startup error.
			if _, err := os.ReadFile(t.TokenFile); err != nil {
				return nil, fmt.Errorf("auth.static token %q: read tokenFile: %w", t.Name, err)
			}
			v.files[t.Name] = t.TokenFile
		case t.Token != "":
			v.inline[t.Name] = t.Token
		}
	}
	return v, nil
}

// verify implements auth.TokenVerifier.
func (v *staticVerifier) verify(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	for name, secret := range v.inline {
		if constantEq(token, secret) {
			return &auth.TokenInfo{UserID: "static:" + name, Expiration: staticExpiry()}, nil
		}
	}
	for name, path := range v.files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // a transiently unreadable file must not panic; just skip
		}
		if constantEq(token, strings.TrimSpace(string(data))) {
			return &auth.TokenInfo{UserID: "static:" + name, Expiration: staticExpiry()}, nil
		}
	}
	return nil, auth.ErrInvalidToken
}

// staticExpiry returns a far-future expiration. Static tokens do not expire by
// design (rotate the secret instead); the SDK middleware requires a non-zero,
// future Expiration, so we supply a rolling one.
func staticExpiry() time.Time { return time.Now().Add(24 * time.Hour) }

// constantEq compares two strings in constant time (guards against length-based
// early exit by comparing lengths without branching on the secret).
func constantEq(a, b string) bool {
	if len(a) != len(b) {
		// Still do a comparison to keep timing roughly uniform.
		subtle.ConstantTimeCompare([]byte(a), []byte(a))
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
