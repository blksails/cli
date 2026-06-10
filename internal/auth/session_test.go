package auth

import (
	"strings"
	"testing"
	"time"
)

// --- IsExpired / expiry boundary (Requirement 10.5, 4.x) ---

func TestIsExpiredAt_NotYetExpired(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	// expires well in the future, beyond skew
	s := Session{ExpiresAt: now.Unix() + 3600}
	if IsExpiredAt(s, now, 30*time.Second) {
		t.Fatalf("session expiring in 1h should not be expired at now")
	}
}

func TestIsExpiredAt_PastExpiry(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s := Session{ExpiresAt: now.Unix() - 3600}
	if !IsExpiredAt(s, now, 30*time.Second) {
		t.Fatalf("session that expired 1h ago should be expired")
	}
}

func TestIsExpiredAt_ExactlyAtExpiry(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	// now + skew == ExpiresAt -> treated as expired (>=)
	s := Session{ExpiresAt: now.Unix() + 30}
	if !IsExpiredAt(s, now, 30*time.Second) {
		t.Fatalf("now+skew == ExpiresAt should be considered expired (safety margin)")
	}
}

func TestIsExpiredAt_WithinSkewMargin(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	// expires 10s from now but skew is 30s -> within margin -> expired
	s := Session{ExpiresAt: now.Unix() + 10}
	if !IsExpiredAt(s, now, 30*time.Second) {
		t.Fatalf("token expiring inside the skew margin should be treated as expired")
	}
}

func TestIsExpiredAt_JustOutsideSkewMargin(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	// expires 31s from now, skew 30s -> 1s of valid headroom -> not expired
	s := Session{ExpiresAt: now.Unix() + 31}
	if IsExpiredAt(s, now, 30*time.Second) {
		t.Fatalf("token expiring just outside the skew margin should still be valid")
	}
}

func TestSession_IsExpired_MethodMatchesPureHelper(t *testing.T) {
	// A session that already expired in the past must report expired via the
	// design's method form (which reads local wall-clock time).
	past := Session{ExpiresAt: time.Now().Unix() - 3600}
	if !past.IsExpired(30 * time.Second) {
		t.Fatalf("past session should be expired via method form")
	}
	future := Session{ExpiresAt: time.Now().Unix() + 3600}
	if future.IsExpired(30 * time.Second) {
		t.Fatalf("far-future session should not be expired via method form")
	}
}

// --- RemoveProfile (Requirement 4.2, 4.3) ---

func mkConfigs() []*AuthConfig {
	return []*AuthConfig{
		{Profile: "alice", Session: Session{AccessToken: "a-token"}},
		{Profile: "bob", Session: Session{AccessToken: "b-token"}},
		{Profile: "carol", Session: Session{AccessToken: "c-token"}},
	}
}

func profiles(configs []*AuthConfig) []string {
	out := make([]string, 0, len(configs))
	for _, c := range configs {
		out = append(out, c.Profile)
	}
	return out
}

func TestRemoveProfile_RemovesTarget(t *testing.T) {
	got := RemoveProfile(mkConfigs(), "bob")
	for _, c := range got {
		if c.Profile == "bob" {
			t.Fatalf("expected bob to be removed, got profiles %v", profiles(got))
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 remaining configs, got %d: %v", len(got), profiles(got))
	}
}

func TestRemoveProfile_KeepsOthersUnchanged(t *testing.T) {
	in := mkConfigs()
	got := RemoveProfile(in, "bob")
	want := map[string]string{"alice": "a-token", "carol": "c-token"}
	if len(got) != len(want) {
		t.Fatalf("expected %d configs, got %d", len(want), len(got))
	}
	for _, c := range got {
		tok, ok := want[c.Profile]
		if !ok {
			t.Fatalf("unexpected profile retained: %s", c.Profile)
		}
		if c.Session.AccessToken != tok {
			t.Fatalf("profile %s token mutated: got %q want %q", c.Profile, c.Session.AccessToken, tok)
		}
	}
}

func TestRemoveProfile_IdempotentWhenAbsent(t *testing.T) {
	in := mkConfigs()
	got := RemoveProfile(in, "nobody")
	if len(got) != 3 {
		t.Fatalf("removing absent profile should keep all 3, got %d: %v", len(got), profiles(got))
	}
	// removing again is still a no-op
	got2 := RemoveProfile(got, "nobody")
	if len(got2) != 3 {
		t.Fatalf("second removal of absent profile should still keep all 3, got %d", len(got2))
	}
}

func TestRemoveProfile_EmptyInput(t *testing.T) {
	got := RemoveProfile(nil, "anything")
	if len(got) != 0 {
		t.Fatalf("removing from nil/empty should yield empty, got %d", len(got))
	}
}

// --- MaskToken (Requirement 11.1, 11.4) ---

func TestMaskToken_NeverLeaksFullToken(t *testing.T) {
	cases := []string{
		"a",
		"ab",
		"abcd",
		"abcdef",
		"sbp_0123456789abcdefXYZ",
		strings.Repeat("Z", 64),
	}
	for _, tok := range cases {
		masked := MaskToken(tok)
		if masked == tok {
			t.Fatalf("MaskToken(%q) returned the full token unmasked: %q", tok, masked)
		}
		if !strings.Contains(masked, "***") {
			t.Fatalf("MaskToken(%q)=%q should contain mask marker ***", tok, masked)
		}
	}
}

func TestMaskToken_ShortTokenFullyMasked(t *testing.T) {
	// Very short tokens must not reveal any original character.
	for _, tok := range []string{"a", "ab", "abc", "abcd"} {
		masked := MaskToken(tok)
		if strings.ContainsAny(masked, "abcd") {
			t.Fatalf("MaskToken(%q)=%q leaked original characters for a short token", tok, masked)
		}
	}
}

func TestMaskToken_LongTokenKeepsEdgesOnly(t *testing.T) {
	tok := "sbp_0123456789abcdefXYZ"
	masked := MaskToken(tok)
	// keeps a few leading/trailing chars but the secret middle must be gone
	if !strings.HasPrefix(masked, tok[:2]) {
		t.Fatalf("MaskToken(%q)=%q should keep leading chars", tok, masked)
	}
	if !strings.HasSuffix(masked, tok[len(tok)-2:]) {
		t.Fatalf("MaskToken(%q)=%q should keep trailing chars", tok, masked)
	}
	if strings.Contains(masked, "456789abcdef") {
		t.Fatalf("MaskToken(%q)=%q leaked the middle of the token", tok, masked)
	}
}

func TestMaskToken_EmptyInputSafe(t *testing.T) {
	if got := MaskToken(""); got != "" {
		t.Fatalf("MaskToken(\"\") should be empty, got %q", got)
	}
}
