package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedSessionExpiry persists a session for profile with an explicit ExpiresAt
// (unix seconds) so whoami tests can control valid/expired determination
// independently of wall-clock time. It reuses the production persist path.
func seedSessionExpiry(t *testing.T, path, profile, email, access, refresh string, expiresAt int64) {
	t.Helper()
	s := fakeSession(email, access, refresh)
	s.ExpiresAt = expiresAt
	if err := persistLogin(path, profile, s); err != nil {
		t.Fatalf("seed session for %s: %v", profile, err)
	}
}

// TestRunWhoami_ValidSession proves that a session whose ExpiresAt is in the
// future (relative to the injected now) is reported as valid: the output shows
// the profile name, the user email, a 有效 marker and the expiry time, and never
// leaks the access or refresh token (Requirements 6.1, 6.2, 6.5).
func TestRunWhoami_ValidSession(t *testing.T) {
	const (
		access  = "validAccessTokenSECRET1234567890"
		refresh = "validRefreshTokenSECRET0987654321"
		email   = "alice@example.com"
		profile = "production"
	)
	path := filepath.Join(t.TempDir(), "auth.json")
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Hour).Unix()
	seedSessionExpiry(t, path, profile, email, access, refresh, expiresAt)

	var out bytes.Buffer
	if err := runWhoami(&out, path, profile, now); err != nil {
		t.Fatalf("runWhoami returned error: %v", err)
	}

	msg := out.String()
	if !strings.Contains(msg, profile) {
		t.Errorf("whoami output should contain profile %q, got %q", profile, msg)
	}
	if !strings.Contains(msg, email) {
		t.Errorf("whoami output should contain email %q, got %q", email, msg)
	}
	if !strings.Contains(msg, "有效") {
		t.Errorf("whoami output for a valid session should mark it 有效, got %q", msg)
	}
	if strings.Contains(msg, "已过期") {
		t.Errorf("whoami output must not mark a valid session 已过期, got %q", msg)
	}
	assertNoTokenLeak(t, msg, access, refresh)
}

// TestRunWhoami_ExpiredSession proves that a session whose ExpiresAt is in the
// past (relative to the injected now) is reported as expired: the output carries
// the 已过期 marker plus a re-login/refresh hint, and never leaks the tokens
// (Requirements 6.3, 6.5).
func TestRunWhoami_ExpiredSession(t *testing.T) {
	const (
		access  = "expiredAccessTokenSECRET1234567890"
		refresh = "expiredRefreshTokenSECRET0987654321"
		email   = "bob@example.com"
		profile = "staging"
	)
	path := filepath.Join(t.TempDir(), "auth.json")
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(-time.Hour).Unix()
	seedSessionExpiry(t, path, profile, email, access, refresh, expiresAt)

	var out bytes.Buffer
	if err := runWhoami(&out, path, profile, now); err != nil {
		t.Fatalf("runWhoami returned error: %v", err)
	}

	msg := out.String()
	if !strings.Contains(msg, "已过期") {
		t.Errorf("whoami output for an expired session should mark it 已过期, got %q", msg)
	}
	if !strings.Contains(msg, "login") {
		t.Errorf("whoami output for an expired session should hint re-login/refresh, got %q", msg)
	}
	if strings.Contains(msg, "有效") {
		t.Errorf("whoami output must not mark an expired session 有效, got %q", msg)
	}
	assertNoTokenLeak(t, msg, access, refresh)
}

// TestRunWhoami_NoEntry proves that whoami on a profile with no saved session
// reports 未登录 and guides the user to log in, without erroring (Requirement
// 6.4).
func TestRunWhoami_NoEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	// Seed an unrelated profile so the file exists but the target is absent.
	seedSessionExpiry(t, path, "other", "other@example.com", "a", "b", 9999999999)

	var out bytes.Buffer
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	if err := runWhoami(&out, path, "ghost", now); err != nil {
		t.Fatalf("runWhoami on absent profile must not error, got: %v", err)
	}

	msg := out.String()
	if !strings.Contains(msg, "未登录") {
		t.Errorf("whoami output for an absent profile should say 未登录, got %q", msg)
	}
	if !strings.Contains(msg, "login") {
		t.Errorf("whoami output for an absent profile should guide the user to log in, got %q", msg)
	}
	if strings.Contains(msg, "有效") || strings.Contains(msg, "已过期") {
		t.Errorf("whoami output for an absent profile must not claim a session state, got %q", msg)
	}
}

// TestRunWhoami_MissingFile proves that whoami when no auth.json exists at all
// is a friendly 未登录 result with a nil error (Requirement 6.4).
func TestRunWhoami_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope", "auth.json")

	var out bytes.Buffer
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	if err := runWhoami(&out, path, "default", now); err != nil {
		t.Fatalf("runWhoami with missing auth.json must not error, got: %v", err)
	}
	if msg := out.String(); !strings.Contains(msg, "未登录") {
		t.Errorf("expected 未登录 when no auth file exists, got %q", msg)
	}
}

// assertNoTokenLeak fails the test if any token string appears verbatim in the
// whoami output, enforcing Requirement 6.5 (no access/refresh token in output).
func assertNoTokenLeak(t *testing.T, msg, access, refresh string) {
	t.Helper()
	for _, secret := range []string{access, refresh} {
		if strings.Contains(msg, secret) {
			t.Errorf("whoami output leaked a token %q: %q", secret, msg)
		}
	}
}
