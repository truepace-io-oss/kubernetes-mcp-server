package auth

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/config"
)

func TestStaticVerifierInlineAndFile(t *testing.T) {
	dir := t.TempDir()
	tokFile := filepath.Join(dir, "ci")
	if err := os.WriteFile(tokFile, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := newStaticVerifier(config.AuthStatic{
		Enabled: true,
		Tokens: []config.AuthToken{
			{Name: "laptop", Token: "inline-token"},
			{Name: "ci", TokenFile: tokFile},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Inline token accepted; trailing newline in file trimmed.
	if info, err := v.verify(context.Background(), "inline-token", nil); err != nil || info.UserID != "static:laptop" {
		t.Fatalf("inline token: info=%v err=%v", info, err)
	}
	if info, err := v.verify(context.Background(), "file-token", nil); err != nil || info.UserID != "static:ci" {
		t.Fatalf("file token: info=%v err=%v", info, err)
	}
	// Wrong token rejected.
	if _, err := v.verify(context.Background(), "nope", nil); err == nil {
		t.Fatal("expected rejection of unknown token")
	}

	// Rotation: rewrite the file; new value accepted, old rejected.
	if err := os.WriteFile(tokFile, []byte("rotated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := v.verify(context.Background(), "rotated", nil); err != nil {
		t.Fatalf("rotated token should be accepted: %v", err)
	}
	if _, err := v.verify(context.Background(), "file-token", nil); err == nil {
		t.Fatal("old file token should be rejected after rotation")
	}
}

func TestStaticVerifierMissingFile(t *testing.T) {
	_, err := newStaticVerifier(config.AuthStatic{
		Enabled: true,
		Tokens:  []config.AuthToken{{Name: "x", TokenFile: "/no/such/file"}},
	})
	if err == nil {
		t.Fatal("expected error for unreadable tokenFile")
	}
}

func TestBuildDisabledIsPassthrough(t *testing.T) {
	b, err := Build(context.Background(), config.Auth{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if b.MetadataHandler != nil || b.Description != "disabled" {
		t.Fatalf("expected disabled passthrough, got %+v", b)
	}
}
