package vault

import (
	"path/filepath"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := LoadOrCreateKey(filepath.Join(t.TempDir(), "vault.key"))
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}

	plain := "s3cr3t-密码-value"
	enc, err := Encrypt(key, plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if enc == plain {
		t.Fatal("密文与明文相同，加密未生效")
	}
	dec, err := Decrypt(key, enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if dec != plain {
		t.Fatalf("round-trip 不一致: got %q want %q", dec, plain)
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	k1, _ := LoadOrCreateKey(filepath.Join(t.TempDir(), "k1"))
	k2, _ := LoadOrCreateKey(filepath.Join(t.TempDir(), "k2"))

	enc, err := Encrypt(k1, "hello")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Decrypt(k2, enc); err == nil {
		t.Fatal("用错误密钥解密应失败，但成功了")
	}
}

func TestLoadOrCreateKeyPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.key")
	k1, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("首次: %v", err)
	}
	k2, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("二次: %v", err)
	}
	if string(k1) != string(k2) {
		t.Fatal("两次加载的主密钥不一致，持久化失败")
	}
}
