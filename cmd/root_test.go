package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

// TestPersistentFlagsRegistered verifies Requirement 1.2 / 1.3:
// the four global persistent flags are registered on rootCmd and
// --profile defaults to "default".
func TestPersistentFlagsRegistered(t *testing.T) {
	for _, name := range []string{"config", "api-endpoint", "api-key", "profile"} {
		if f := rootCmd.PersistentFlags().Lookup(name); f == nil {
			t.Fatalf("persistent flag --%s is not registered on rootCmd", name)
		}
	}

	profileFlag := rootCmd.PersistentFlags().Lookup("profile")
	if profileFlag == nil {
		t.Fatalf("persistent flag --profile is not registered on rootCmd")
	}
	if profileFlag.DefValue != "default" {
		t.Fatalf("--profile default = %q, want %q", profileFlag.DefValue, "default")
	}
}

// TestLoadConfigToleratesUnknownKeys verifies Requirement 2.8 / 2.1:
// a .bs.yaml containing an unrecognized key must not break loading, and
// recognized keys (api_endpoint / api_key) must still resolve.
func TestLoadConfigToleratesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".bs.yaml")
	content := "" +
		"api_endpoint: https://example.test\n" +
		"api_key: secret-key-123\n" +
		"totally_unknown_key: some-value\n" +
		"nested:\n" +
		"  also_unknown: 42\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	v := loadConfig(cfgPath)
	if v == nil {
		t.Fatalf("loadConfig returned nil viper instance")
	}
	if got := v.GetString("api_endpoint"); got != "https://example.test" {
		t.Fatalf("api_endpoint = %q, want %q", got, "https://example.test")
	}
	if got := v.GetString("api_key"); got != "secret-key-123" {
		t.Fatalf("api_key = %q, want %q", got, "secret-key-123")
	}
}

// ensure viper import is used even if loadConfig signature changes.
var _ = viper.GetViper
