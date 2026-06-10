package sshkeys

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestGenerateKeyPair_PublicLineParsesAndFingerprintMatches 验证：
// 公钥 authorized line 可被 ssh.ParseAuthorizedKey 解析；
// 解析出公钥的 FingerprintSHA256 等于 KeyPair.FingerprintSHA（Requirement 1.1, 1.3）。
func TestGenerateKeyPair_PublicLineParsesAndFingerprintMatches(t *testing.T) {
	const comment = "alice@example.com-host1"
	kp, err := GenerateKeyPair(comment)
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}

	if !strings.HasPrefix(kp.PublicAuthLine, "ssh-ed25519 ") {
		t.Errorf("PublicAuthLine should start with ssh-ed25519, got %q", kp.PublicAuthLine)
	}
	if !strings.HasSuffix(kp.PublicAuthLine, " "+comment) {
		t.Errorf("PublicAuthLine should end with comment %q, got %q", comment, kp.PublicAuthLine)
	}
	if strings.Contains(kp.PublicAuthLine, "\n") {
		t.Errorf("PublicAuthLine should be a single line without trailing newline, got %q", kp.PublicAuthLine)
	}

	pub, gotComment, _, _, err := ssh.ParseAuthorizedKey([]byte(kp.PublicAuthLine))
	if err != nil {
		t.Fatalf("ssh.ParseAuthorizedKey() error = %v", err)
	}
	if gotComment != comment {
		t.Errorf("parsed comment = %q, want %q", gotComment, comment)
	}

	if got := ssh.FingerprintSHA256(pub); got != kp.FingerprintSHA {
		t.Errorf("fingerprint mismatch: parsed %q vs KeyPair.FingerprintSHA %q", got, kp.FingerprintSHA)
	}
	if !strings.HasPrefix(kp.FingerprintSHA, "SHA256:") {
		t.Errorf("FingerprintSHA should start with SHA256:, got %q", kp.FingerprintSHA)
	}
}

// TestGenerateKeyPair_PrivateKeyOnlyInPrivatePEM 验证私钥仅出现在 PrivatePEM 字段，
// 不泄漏到 PublicAuthLine / FingerprintSHA（Requirement 10.1, 10.2）。
func TestGenerateKeyPair_PrivateKeyOnlyInPrivatePEM(t *testing.T) {
	kp, err := GenerateKeyPair("bob@example.com-host2")
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}

	if len(kp.PrivatePEM) == 0 {
		t.Fatal("PrivatePEM must not be empty")
	}
	if !bytes.Contains(kp.PrivatePEM, []byte("OPENSSH PRIVATE KEY")) {
		t.Errorf("PrivatePEM should be an OpenSSH PEM private key, got %q", kp.PrivatePEM)
	}

	// 私钥 PEM 的 marker 不得出现在任何非落盘返回字段中。
	if strings.Contains(kp.PublicAuthLine, "PRIVATE KEY") {
		t.Errorf("private key leaked into PublicAuthLine: %q", kp.PublicAuthLine)
	}
	if strings.Contains(kp.FingerprintSHA, "PRIVATE KEY") {
		t.Errorf("private key leaked into FingerprintSHA: %q", kp.FingerprintSHA)
	}

	// 公钥行/指纹与私钥 PEM 之间不得有任何重叠的非平凡子串（两者本应完全不同的材料）。
	if bytes.Contains(kp.PrivatePEM, []byte(kp.PublicAuthLine)) {
		t.Error("PublicAuthLine should not appear verbatim inside PrivatePEM")
	}
}

// TestWritePrivateKey_FileMode0600 验证落盘私钥文件权限为 0600，父目录自动以 0700 创建
//（Requirement 1.2, 10.1）。
func TestWritePrivateKey_FileMode0600(t *testing.T) {
	kp, err := GenerateKeyPair("carol@example.com-host3")
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "id_ed25519")

	if err := WritePrivateKey(path, kp.PrivatePEM, false); err != nil {
		t.Fatalf("WritePrivateKey() error = %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v", path, err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("private key file mode = %o, want 0600", got)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if !bytes.Equal(got, kp.PrivatePEM) {
		t.Error("written private key content does not match PrivatePEM")
	}

	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("os.Stat(parent dir) error = %v", err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("parent dir mode = %o, want 0700", got)
	}
}

// TestWritePrivateKey_ExistsNoOverwrite 验证文件已存在且 overwrite=false 时返回 ErrKeyExists
//（errors.Is 可识别），且不覆盖原内容（Requirement 1.4）。
func TestWritePrivateKey_ExistsNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")

	original := []byte("ORIGINAL-PRIVATE-KEY-DO-NOT-CLOBBER")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("seed file error = %v", err)
	}

	err := WritePrivateKey(path, []byte("NEW-PRIVATE-KEY"), false)
	if err == nil {
		t.Fatal("WritePrivateKey() expected error when file exists and overwrite=false, got nil")
	}
	if !errors.Is(err, ErrKeyExists) {
		t.Errorf("WritePrivateKey() error = %v, want errors.Is ErrKeyExists", err)
	}

	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("os.ReadFile() error = %v", readErr)
	}
	if !bytes.Equal(got, original) {
		t.Error("existing private key must not be clobbered when overwrite=false")
	}
}

// TestWritePrivateKey_OverwriteTrue 验证 overwrite=true 时覆盖已存在文件并保持 0600
//（Requirement 1.4）。
func TestWritePrivateKey_OverwriteTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")

	if err := os.WriteFile(path, []byte("OLD"), 0o644); err != nil {
		t.Fatalf("seed file error = %v", err)
	}

	newContent := []byte("NEW-PRIVATE-KEY-MATERIAL")
	if err := WritePrivateKey(path, newContent, true); err != nil {
		t.Fatalf("WritePrivateKey(overwrite=true) error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if !bytes.Equal(got, newContent) {
		t.Error("overwrite=true should replace existing content")
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode after overwrite = %o, want 0600", mode)
	}
}
