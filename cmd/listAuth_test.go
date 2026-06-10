package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/auth"
)

// TestRunListAuth_MarksActiveProfile proves that with multiple saved profiles
// the active profile's row carries a recognizable marker while every other
// profile's row does not (Requirement 5.1, 5.2).
func TestRunListAuth_MarksActiveProfile(t *testing.T) {
	const (
		activeProfile = "production"
		activeEmail   = "alice@example.com"
		otherProfile  = "staging"
		otherEmail    = "bob@example.com"
	)
	path := filepath.Join(t.TempDir(), "auth.json")
	seedSessionExpiry(t, path, activeProfile, activeEmail, "accA", "refA", 9999999999)
	seedSessionExpiry(t, path, otherProfile, otherEmail, "accB", "refB", 9999999999)

	var out bytes.Buffer
	if err := runListAuth(&out, path, activeProfile); err != nil {
		t.Fatalf("runListAuth returned error: %v", err)
	}

	msg := out.String()
	if !strings.Contains(msg, activeProfile) || !strings.Contains(msg, otherProfile) {
		t.Fatalf("list output should contain both profiles, got %q", msg)
	}

	activeLine := lineContaining(t, msg, activeProfile)
	otherLine := lineContaining(t, msg, otherProfile)

	if !strings.Contains(activeLine, "*") {
		t.Errorf("active profile row should carry a marker, got %q", activeLine)
	}
	if strings.Contains(otherLine, "*") {
		t.Errorf("non-active profile row should not carry the marker, got %q", otherLine)
	}
}

// TestRunListAuth_EmptyConfig proves that an empty auth.json (no profiles) yields
// a friendly empty-list message and a nil error, not a failure (Requirement 5.3).
func TestRunListAuth_EmptyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	// Seed then remove to leave a valid-but-empty config file ([]).
	seedSessionExpiry(t, path, "temp", "temp@example.com", "a", "b", 9999999999)
	if err := auth.RemoveAuthConfig(path, "temp"); err != nil {
		t.Fatalf("prepare empty config: %v", err)
	}

	var out bytes.Buffer
	if err := runListAuth(&out, path, "default"); err != nil {
		t.Fatalf("runListAuth on empty config must not error, got: %v", err)
	}
	if msg := out.String(); !strings.Contains(msg, "暂无") || !strings.Contains(msg, "login") {
		t.Errorf("expected friendly empty-list message guiding login, got %q", msg)
	}
}

// TestRunListAuth_MissingFile proves that a missing auth.json is handled as an
// empty list with a friendly message and a nil error (Requirement 5.3).
func TestRunListAuth_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope", "auth.json")

	var out bytes.Buffer
	if err := runListAuth(&out, path, "default"); err != nil {
		t.Fatalf("runListAuth with missing auth.json must not error, got: %v", err)
	}
	if msg := out.String(); !strings.Contains(msg, "暂无") {
		t.Errorf("expected friendly empty-list message when no auth file exists, got %q", msg)
	}
}

// TestRunListAuth_NoTokenLeak proves that the list output never contains the
// access or refresh token of any profile, even though the fixtures store
// non-empty tokens (Requirement 5.4).
func TestRunListAuth_NoTokenLeak(t *testing.T) {
	const (
		access  = "listAccessTokenSECRET1234567890"
		refresh = "listRefreshTokenSECRET0987654321"
	)
	path := filepath.Join(t.TempDir(), "auth.json")
	seedSessionExpiry(t, path, "production", "alice@example.com", access, refresh, 9999999999)

	var out bytes.Buffer
	if err := runListAuth(&out, path, "production"); err != nil {
		t.Fatalf("runListAuth returned error: %v", err)
	}

	msg := out.String()
	for _, secret := range []string{access, refresh} {
		if strings.Contains(msg, secret) {
			t.Errorf("list output leaked a token %q: %q", secret, msg)
		}
	}
}

// lineContaining returns the first line of msg that contains needle, failing the
// test if none does.
func lineContaining(t *testing.T, msg, needle string) string {
	t.Helper()
	for _, line := range strings.Split(msg, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no line containing %q in %q", needle, msg)
	return ""
}
