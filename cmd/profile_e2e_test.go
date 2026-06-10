package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pkg.blksails.net/bk/internal/auth"
)

// TestMultiProfile_EndToEnd exercises the full login → whoami → list → logout
// flow for two profiles ("alpha" and "beta") against ONE shared temp auth.json,
// driving each step through the same testable seams the production commands use
// (persistLogin, runWhoami, runListAuth, runLogout). It proves the multi-profile
// guarantees of Requirement 7:
//   - 7.1: a single auth.json holds multiple named profiles concurrently and the
//     list output marks the currently active profile.
//   - 7.2: a --profile-scoped command operates on exactly that profile's session.
//   - 7.3: operating on one profile never mutates another profile's session data.
//   - 7.4: referencing an absent profile reports 未登录 / login guidance.
//
// It uses real, distinct, non-empty tokens for the two sessions so the isolation
// and no-token-leak assertions are non-vacuous: alpha's secrets must never appear
// in beta's output (and vice versa), and no token may appear in any whoami/list
// output at all.
func TestMultiProfile_EndToEnd(t *testing.T) {
	const (
		alphaProfile = "alpha"
		alphaEmail   = "alpha@example.com"
		alphaAccess  = "alphaAccessTokenSECRET_0000000001"
		alphaRefresh = "alphaRefreshTokenSECRET_000000002"

		betaProfile = "beta"
		betaEmail   = "beta@example.com"
		betaAccess  = "betaAccessTokenSECRET_00000000003"
		betaRefresh = "betaRefreshTokenSECRET_0000000004"
	)

	// ONE shared auth.json drives the whole end-to-end flow.
	path := filepath.Join(t.TempDir(), "auth.json")
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	futureExpiry := now.Add(time.Hour).Unix()

	// --- Step 1: "login" alpha and beta via the real persist seam ----------
	seedSessionExpiry(t, path, alphaProfile, alphaEmail, alphaAccess, alphaRefresh, futureExpiry)
	seedSessionExpiry(t, path, betaProfile, betaEmail, betaAccess, betaRefresh, futureExpiry)

	// --- Step 2: whoami isolation (7.2 / 7.3) ------------------------------
	// whoami --profile alpha shows alpha's identity + 有效, and NOT beta's email.
	var alphaWho bytes.Buffer
	if err := runWhoami(&alphaWho, path, alphaProfile, now); err != nil {
		t.Fatalf("runWhoami(alpha): %v", err)
	}
	alphaMsg := alphaWho.String()
	if !strings.Contains(alphaMsg, alphaEmail) {
		t.Errorf("whoami(alpha) should show %q, got %q", alphaEmail, alphaMsg)
	}
	if !strings.Contains(alphaMsg, "有效") {
		t.Errorf("whoami(alpha) should mark a future-expiry session 有效, got %q", alphaMsg)
	}
	if strings.Contains(alphaMsg, betaEmail) {
		t.Errorf("whoami(alpha) leaked beta's email %q: %q", betaEmail, alphaMsg)
	}
	assertNoTokenLeak(t, alphaMsg, alphaAccess, alphaRefresh)
	assertNoTokenLeak(t, alphaMsg, betaAccess, betaRefresh)

	// whoami --profile beta shows beta's identity, and NOT alpha's email.
	var betaWho bytes.Buffer
	if err := runWhoami(&betaWho, path, betaProfile, now); err != nil {
		t.Fatalf("runWhoami(beta): %v", err)
	}
	betaMsg := betaWho.String()
	if !strings.Contains(betaMsg, betaEmail) {
		t.Errorf("whoami(beta) should show %q, got %q", betaEmail, betaMsg)
	}
	if strings.Contains(betaMsg, alphaEmail) {
		t.Errorf("whoami(beta) leaked alpha's email %q: %q", alphaEmail, betaMsg)
	}
	assertNoTokenLeak(t, betaMsg, alphaAccess, alphaRefresh)
	assertNoTokenLeak(t, betaMsg, betaAccess, betaRefresh)

	// --- Step 3: list current-profile marking (7.1) ------------------------
	// With activeProfile=alpha, both profiles listed; alpha row marked current,
	// beta not.
	var listAlpha bytes.Buffer
	if err := runListAuth(&listAlpha, path, alphaProfile); err != nil {
		t.Fatalf("runListAuth(active=alpha): %v", err)
	}
	la := listAlpha.String()
	if !strings.Contains(la, alphaEmail) || !strings.Contains(la, betaEmail) {
		t.Errorf("list should contain both profiles, got %q", la)
	}
	if mark := currentMarkerFor(t, la, alphaProfile); mark != "*" {
		t.Errorf("list(active=alpha): alpha row should be marked current, got marker %q in %q", mark, la)
	}
	if mark := currentMarkerFor(t, la, betaProfile); mark == "*" {
		t.Errorf("list(active=alpha): beta row must NOT be marked current, got %q", la)
	}
	assertNoTokenLeak(t, la, alphaAccess, alphaRefresh)
	assertNoTokenLeak(t, la, betaAccess, betaRefresh)

	// With activeProfile=beta, marking flips: beta current, alpha not.
	var listBeta bytes.Buffer
	if err := runListAuth(&listBeta, path, betaProfile); err != nil {
		t.Fatalf("runListAuth(active=beta): %v", err)
	}
	lb := listBeta.String()
	if mark := currentMarkerFor(t, lb, betaProfile); mark != "*" {
		t.Errorf("list(active=beta): beta row should be marked current, got marker %q in %q", mark, lb)
	}
	if mark := currentMarkerFor(t, lb, alphaProfile); mark == "*" {
		t.Errorf("list(active=beta): alpha row must NOT be marked current, got %q", lb)
	}

	// --- Step 4: logout alpha leaves beta intact (7.3 / 7.4) ---------------
	var logoutOut bytes.Buffer
	if err := runLogout(&logoutOut, path, alphaProfile); err != nil {
		t.Fatalf("runLogout(alpha): %v", err)
	}
	if !strings.Contains(logoutOut.String(), alphaProfile) {
		t.Errorf("logout(alpha) should confirm the profile, got %q", logoutOut.String())
	}

	// auth.json now has beta only; alpha gone; beta's session byte-for-byte intact.
	configs, err := auth.LoadAuthConfig(path)
	if err != nil {
		t.Fatalf("load auth config after logout: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("after logout(alpha) expected exactly 1 profile remaining, got %d: %+v", len(configs), configs)
	}
	betaCfg := configs[0]
	if betaCfg.Profile != betaProfile {
		t.Fatalf("remaining profile should be beta, got %q", betaCfg.Profile)
	}
	if betaCfg.Session.User.Email != betaEmail {
		t.Errorf("beta email mutated by alpha logout: got %q", betaCfg.Session.User.Email)
	}
	if betaCfg.Session.AccessToken != betaAccess || betaCfg.Session.RefreshToken != betaRefresh {
		t.Errorf("beta tokens mutated by alpha logout: access=%q refresh=%q",
			betaCfg.Session.AccessToken, betaCfg.Session.RefreshToken)
	}
	if betaCfg.Session.ExpiresAt != futureExpiry {
		t.Errorf("beta expiry mutated by alpha logout: got %d want %d", betaCfg.Session.ExpiresAt, futureExpiry)
	}

	// whoami(alpha) now reports 未登录 with login guidance (7.4); beta preserved.
	var alphaWhoAfter bytes.Buffer
	if err := runWhoami(&alphaWhoAfter, path, alphaProfile, now); err != nil {
		t.Fatalf("runWhoami(alpha) after logout: %v", err)
	}
	awa := alphaWhoAfter.String()
	if !strings.Contains(awa, "未登录") || !strings.Contains(awa, "login") {
		t.Errorf("whoami(alpha) after logout should report 未登录 + login guidance, got %q", awa)
	}

	var betaWhoAfter bytes.Buffer
	if err := runWhoami(&betaWhoAfter, path, betaProfile, now); err != nil {
		t.Fatalf("runWhoami(beta) after alpha logout: %v", err)
	}
	bwa := betaWhoAfter.String()
	if !strings.Contains(bwa, betaEmail) || !strings.Contains(bwa, "有效") {
		t.Errorf("whoami(beta) should remain valid after alpha logout, got %q", bwa)
	}

	// list after logout: alpha absent, beta present and (active=beta) marked current.
	var listAfter bytes.Buffer
	if err := runListAuth(&listAfter, path, betaProfile); err != nil {
		t.Fatalf("runListAuth after logout: %v", err)
	}
	lAfter := listAfter.String()
	if strings.Contains(lAfter, alphaEmail) {
		t.Errorf("list after alpha logout must not contain alpha %q, got %q", alphaEmail, lAfter)
	}
	if !strings.Contains(lAfter, betaEmail) {
		t.Errorf("list after alpha logout should still contain beta %q, got %q", betaEmail, lAfter)
	}
	assertNoTokenLeak(t, lAfter, alphaAccess, alphaRefresh)
	assertNoTokenLeak(t, lAfter, betaAccess, betaRefresh)
}

// currentMarkerFor returns the leading "Current" column value for the row that
// mentions profile in the runListAuth table output. The table is tabwriter-
// padded, so the row is located by substring and the marker is taken as the
// first non-space token before the profile name on that line ("*" when active,
// "" otherwise). It returns "" when no row mentions the profile.
func currentMarkerFor(t *testing.T, table, profile string) string {
	t.Helper()
	for _, line := range strings.Split(table, "\n") {
		if !strings.Contains(line, profile) {
			continue
		}
		// Skip the header row.
		if strings.Contains(line, "Profile") && strings.Contains(line, "Email") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "*") {
			return "*"
		}
		return ""
	}
	return ""
}
