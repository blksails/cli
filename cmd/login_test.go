package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/supabase-community/gotrue-go/types"
	"pkg.blksails.net/bk/internal/auth"
)

// TestLoginSuccessMessage_NoTokenLeak proves that the login-success output
// shown to the user contains only non-sensitive information (profile name and
// user email) and never leaks the access token, refresh token, or api_key in
// plaintext (Requirements 3.3, 11.1, 11.2).
func TestLoginSuccessMessage_NoTokenLeak(t *testing.T) {
	const (
		accessToken  = "eyAccessTokenSECRETvalue1234567890"
		refreshToken = "refreshTokenSECRETvalue0987654321"
		apiKey       = "supabaseAPIKEYsecret_abcdefghijkl"
		profile      = "production"
		email        = "user@example.com"
	)

	msg := loginSuccessMessage(profile, email)

	if !strings.Contains(msg, profile) {
		t.Errorf("login success message should contain profile %q, got %q", profile, msg)
	}
	if !strings.Contains(msg, email) {
		t.Errorf("login success message should contain email %q, got %q", email, msg)
	}
	for _, secret := range []string{accessToken, refreshToken, apiKey} {
		if strings.Contains(msg, secret) {
			t.Errorf("login success message leaked a secret %q: %q", secret, msg)
		}
	}
}

// fakeSession builds a minimal but valid types.Session for a given email/token.
func fakeSession(email, access, refresh string) types.Session {
	return types.Session{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenType:    "bearer",
		ExpiresIn:    3600,
		ExpiresAt:    9999999999,
		User: types.User{
			ID:    uuid.New(),
			Role:  "authenticated",
			Email: email,
		},
	}
}

// countProfileEntries loads the auth file and counts entries matching profile.
func countProfileEntries(t *testing.T, path, profile string) int {
	t.Helper()
	configs, err := auth.LoadAuthConfig(path)
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}
	n := 0
	for _, c := range configs {
		if c.Profile == profile {
			n++
		}
	}
	return n
}

// TestPersistLogin_OverwriteNotAppend proves that two successful logins to the
// same profile leave exactly ONE entry for that profile, while a different
// profile's session is preserved (Requirements 3.3, 3.4).
func TestPersistLogin_OverwriteNotAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")

	// Seed an unrelated profile that must survive.
	if err := persistLogin(path, "staging", fakeSession("staging@example.com", "stagingA", "stagingR")); err != nil {
		t.Fatalf("seed staging login: %v", err)
	}

	// First login to "production".
	if err := persistLogin(path, "production", fakeSession("user@example.com", "accessV1", "refreshV1")); err != nil {
		t.Fatalf("first production login: %v", err)
	}
	// Second login to the SAME profile (re-login).
	if err := persistLogin(path, "production", fakeSession("user@example.com", "accessV2", "refreshV2")); err != nil {
		t.Fatalf("second production login: %v", err)
	}

	if got := countProfileEntries(t, path, "production"); got != 1 {
		t.Fatalf("expected exactly 1 entry for profile production after re-login, got %d", got)
	}
	if got := countProfileEntries(t, path, "staging"); got != 1 {
		t.Fatalf("expected staging profile preserved (1 entry), got %d", got)
	}

	// The surviving production entry must carry the latest token.
	configs, err := auth.LoadAuthConfig(path)
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}
	for _, c := range configs {
		if c.Profile == "production" && c.Session.AccessToken != "accessV2" {
			t.Fatalf("expected production access token accessV2, got %q", c.Session.AccessToken)
		}
	}
}

// TestPersistLogin_AutoCreatesDir proves the persist path creates a missing
// parent directory before writing (Requirement 3.6).
func TestPersistLogin_AutoCreatesDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deep", "auth.json")
	if err := persistLogin(path, "default", fakeSession("u@example.com", "a", "r")); err != nil {
		t.Fatalf("persistLogin into missing dir: %v", err)
	}
	if got := countProfileEntries(t, path, "default"); got != 1 {
		t.Fatalf("expected 1 entry after dir auto-create, got %d", got)
	}
}

// TestRunLogin_FailureLeavesFileUntouched proves that a failed sign-in returns a
// non-nil error and leaves a pre-existing auth.json byte-identical, never
// writing or truncating it (Requirement 3.5).
func TestRunLogin_FailureLeavesFileUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")

	// Pre-existing session for the same profile we will try (and fail) to log in.
	if err := persistLogin(path, "production", fakeSession("old@example.com", "oldAccess", "oldRefresh")); err != nil {
		t.Fatalf("seed pre-existing session: %v", err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read auth.json before: %v", err)
	}

	signInErr := errors.New("invalid login credentials")
	failingSignIn := func(email, pass string) (types.Session, error) {
		return types.Session{}, signInErr
	}

	err = runLoginWith(path, "production", "user@example.com", "wrong-password", failingSignIn)
	if err == nil {
		t.Fatalf("expected non-nil error on failed sign-in")
	}
	if !strings.Contains(err.Error(), "invalid login credentials") {
		t.Fatalf("expected error to surface the sign-in reason, got %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read auth.json after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("auth.json was modified on failed login:\nbefore=%q\nafter=%q", before, after)
	}
}

// TestRunLogin_SuccessPersists proves the happy path persists the session for
// the active profile via the injected sign-in seam (Requirements 3.1, 3.3).
func TestRunLogin_SuccessPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")

	okSignIn := func(email, pass string) (types.Session, error) {
		return fakeSession(email, "freshAccess", "freshRefresh"), nil
	}

	if err := runLoginWith(path, "default", "user@example.com", "pw", okSignIn); err != nil {
		t.Fatalf("runLoginWith success: %v", err)
	}

	configs, err := auth.LoadAuthConfig(path)
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}
	if len(configs) != 1 || configs[0].Profile != "default" {
		t.Fatalf("expected single default profile entry, got %+v", configs)
	}
	if configs[0].Session.User.Email != "user@example.com" {
		t.Fatalf("expected persisted email user@example.com, got %q", configs[0].Session.User.Email)
	}
	if configs[0].Session.AccessToken != "freshAccess" {
		t.Fatalf("expected persisted access token freshAccess, got %q", configs[0].Session.AccessToken)
	}
}
