package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/auth"
)

// seedSession persists a session for the given profile so logout tests have a
// known starting state. It reuses the production persist path.
func seedSession(t *testing.T, path, profile, email, access string) {
	t.Helper()
	if err := persistLogin(path, profile, fakeSession(email, access, access+"R")); err != nil {
		t.Fatalf("seed session for %s: %v", profile, err)
	}
}

// TestRunLogout_RemovesOnlyActiveProfile proves that logging out profile A
// removes only A's entry from auth.json while profile B is preserved, matching
// the completion criterion: after logout `bk auth list` no longer shows A but
// keeps B (Requirements 4.1, 4.2).
func TestRunLogout_RemovesOnlyActiveProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	seedSession(t, path, "alpha", "alpha@example.com", "alphaAccess")
	seedSession(t, path, "beta", "beta@example.com", "betaAccess")

	var out bytes.Buffer
	if err := runLogout(&out, path, "alpha"); err != nil {
		t.Fatalf("runLogout(alpha) returned error: %v", err)
	}

	configs, err := auth.LoadAuthConfig(path)
	if err != nil {
		t.Fatalf("load auth config after logout: %v", err)
	}
	for _, c := range configs {
		if c.Profile == "alpha" {
			t.Fatalf("profile alpha should be removed after logout, still present")
		}
	}
	betaFound := false
	for _, c := range configs {
		if c.Profile == "beta" {
			betaFound = true
			if c.Session.AccessToken != "betaAccess" {
				t.Fatalf("beta session mutated: got %q want betaAccess", c.Session.AccessToken)
			}
		}
	}
	if !betaFound {
		t.Fatalf("profile beta must be preserved after logging out alpha")
	}
	if len(configs) != 1 {
		t.Fatalf("expected exactly 1 remaining profile (beta), got %d", len(configs))
	}

	if msg := out.String(); !strings.Contains(msg, "已登出") || !strings.Contains(msg, "alpha") {
		t.Fatalf("expected success confirmation mentioning alpha, got %q", msg)
	}
}

// TestRunLogout_AbsentProfileIsFriendlyNoOp proves that logging out a profile
// with no saved session leaves the file unchanged and returns a nil error (exit
// 0), emitting a friendly "not logged in" message (Requirement 4.3).
func TestRunLogout_AbsentProfileIsFriendlyNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	seedSession(t, path, "alpha", "alpha@example.com", "alphaAccess")

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read auth.json before: %v", err)
	}

	var out bytes.Buffer
	if err := runLogout(&out, path, "ghost"); err != nil {
		t.Fatalf("logout of absent profile must NOT error, got: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read auth.json after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("auth.json changed when logging out an absent profile:\nbefore=%q\nafter=%q", before, after)
	}

	msg := out.String()
	if !strings.Contains(msg, "ghost") || !strings.Contains(msg, "未登录") {
		t.Fatalf("expected friendly not-logged-in message for ghost, got %q", msg)
	}
	if strings.Contains(msg, "已登出") {
		t.Fatalf("must not claim a logout happened for an absent profile, got %q", msg)
	}
}

// TestRunLogout_MissingFileIsFriendlyNoOp proves that logging out when no
// auth.json exists at all is a friendly no-op with a nil error (Requirement 4.3).
func TestRunLogout_MissingFileIsFriendlyNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist", "auth.json")

	var out bytes.Buffer
	if err := runLogout(&out, path, "default"); err != nil {
		t.Fatalf("logout with missing auth.json must NOT error, got: %v", err)
	}
	if msg := out.String(); !strings.Contains(msg, "未登录") {
		t.Fatalf("expected not-logged-in message when no auth file exists, got %q", msg)
	}
}

// TestRemoveAuthConfig_RemovesTargetKeepsOthers proves the disk-level remove
// helper drops only the target profile and rewrites the rest (Requirement 4.2).
func TestRemoveAuthConfig_RemovesTargetKeepsOthers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	seedSession(t, path, "alpha", "a@example.com", "aAccess")
	seedSession(t, path, "beta", "b@example.com", "bAccess")

	if err := auth.RemoveAuthConfig(path, "alpha"); err != nil {
		t.Fatalf("RemoveAuthConfig(alpha): %v", err)
	}
	configs, err := auth.LoadAuthConfig(path)
	if err != nil {
		t.Fatalf("load after remove: %v", err)
	}
	if len(configs) != 1 || configs[0].Profile != "beta" {
		t.Fatalf("expected only beta to remain, got %+v", configs)
	}
}

// TestRemoveAuthConfig_AbsentIsIdempotent proves removing a profile that does
// not exist leaves the file's logical content unchanged and returns nil
// (Requirement 4.3).
func TestRemoveAuthConfig_AbsentIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	seedSession(t, path, "alpha", "a@example.com", "aAccess")

	if err := auth.RemoveAuthConfig(path, "ghost"); err != nil {
		t.Fatalf("RemoveAuthConfig(ghost) should be a no-op, got: %v", err)
	}
	configs, err := auth.LoadAuthConfig(path)
	if err != nil {
		t.Fatalf("load after no-op remove: %v", err)
	}
	if len(configs) != 1 || configs[0].Profile != "alpha" {
		t.Fatalf("absent-profile removal must keep alpha intact, got %+v", configs)
	}
}

// TestRemoveAuthConfig_MissingFileIsNil proves removing from a non-existent
// auth.json returns nil without creating spurious state (Requirement 4.3).
func TestRemoveAuthConfig_MissingFileIsNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope", "auth.json")
	if err := auth.RemoveAuthConfig(path, "default"); err != nil {
		t.Fatalf("RemoveAuthConfig on missing file must return nil, got: %v", err)
	}
}
