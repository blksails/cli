package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// safeName 校验派生名称对文件系统与 dokku 都安全：仅含小写字母、数字、连字符。
var safeName = regexp.MustCompile(`^[a-z0-9-]+$`)

func TestDeriveKeyName_StableAndSafe(t *testing.T) {
	got := deriveKeyName("Alice@Corp.com", "app.internal")

	// 确定性：同输入多次调用结果一致。
	if again := deriveKeyName("Alice@Corp.com", "app.internal"); again != got {
		t.Fatalf("deriveKeyName not deterministic: %q vs %q", got, again)
	}

	// 安全性：无空格、无大写、无非法字符。
	if !safeName.MatchString(got) {
		t.Fatalf("deriveKeyName produced unsafe name %q (want only [a-z0-9-])", got)
	}
	if strings.ToLower(got) != got {
		t.Fatalf("deriveKeyName should be lowercase, got %q", got)
	}
	if strings.Contains(got, " ") {
		t.Fatalf("deriveKeyName should not contain spaces, got %q", got)
	}

	// 应携带邮箱 local-part 与 host 的可辨识信息。
	if !strings.Contains(got, "alice") {
		t.Errorf("deriveKeyName %q should include email local-part 'alice'", got)
	}
	if !strings.Contains(got, "app-internal") {
		t.Errorf("deriveKeyName %q should include sanitized host 'app-internal'", got)
	}
}

func TestDeriveKeyName_DifferentInputsDiffer(t *testing.T) {
	a := deriveKeyName("alice@corp.com", "host1")
	b := deriveKeyName("bob@corp.com", "host1")
	c := deriveKeyName("alice@corp.com", "host2")

	if a == b {
		t.Errorf("different emails should produce different names, both %q", a)
	}
	if a == c {
		t.Errorf("different hosts should produce different names, both %q", a)
	}
}

func TestPrivateKeyPath_UnderKeysDir(t *testing.T) {
	got, err := privateKeyPath("App.Internal")
	if err != nil {
		t.Fatalf("privateKeyPath returned error: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir: %v", err)
	}
	wantDir := filepath.Join(home, ".local", "bk", "keys")
	if filepath.Dir(got) != wantDir {
		t.Errorf("privateKeyPath dir = %q, want %q", filepath.Dir(got), wantDir)
	}

	base := filepath.Base(got)
	if !strings.HasSuffix(base, ".key") {
		t.Errorf("privateKeyPath %q should end in .key", got)
	}

	// 文件名（去掉 .key 后缀）须是经过清洗的安全片段。
	stem := strings.TrimSuffix(base, ".key")
	if !safeName.MatchString(stem) {
		t.Errorf("privateKeyPath stem %q is not filesystem-safe", stem)
	}
}

func TestSSHKeyCmdRegistered(t *testing.T) {
	if sshKeyCmd.Use != "ssh-key" {
		t.Errorf("sshKeyCmd.Use = %q, want %q", sshKeyCmd.Use, "ssh-key")
	}
	var found bool
	for _, c := range rootCmd.Commands() {
		if c == sshKeyCmd {
			found = true
			break
		}
	}
	if !found {
		t.Error("sshKeyCmd is not registered on rootCmd")
	}
	// 确保是 cobra 命令（编译期保证，运行期再确认非 nil）。
	var _ *cobra.Command = sshKeyCmd
}
