package cmd

import (
	"os"
	"strings"
	"testing"
)

// TestAccessKeyNoInlineSessionAssembly proves that the access-key command no
// longer assembles a session client inline (Requirement 8.1/8.2): it must route
// through the shared AuthedClient entry point instead of the previous
// loadProfileAuthConfig + DefaultClient + auth.FromAuthConfig wiring. This is a
// source-level guard for the refactor's completion condition「无内联会话拼装残留」.
func TestAccessKeyNoInlineSessionAssembly(t *testing.T) {
	src, err := os.ReadFile("accessKey.go")
	if err != nil {
		t.Fatalf("read accessKey.go: %v", err)
	}
	text := string(src)

	for _, banned := range []string{
		"loadProfileAuthConfig",
		"auth.FromAuthConfig",
		"DefaultClient(",
	} {
		if strings.Contains(text, banned) {
			t.Errorf("accessKey.go still references %q; the command must obtain its client via AuthedClient, with no inline session assembly", banned)
		}
	}

	if !strings.Contains(text, "AuthedClient(profile)") {
		t.Errorf("accessKey.go must obtain its client via AuthedClient(profile)")
	}
}
