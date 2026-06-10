/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// vault_test.go 覆盖 cmd/vault.go 的公共辅助与父命令装配（design：File Structure
// Plan cmd/vault.go；Requirement 1.4/1.5/1.6/6.1/6.2/6.5/7.1/7.2）。
// 仅测纯逻辑与本机文件行为，不触达网络/Supabase。

// TestParseVaultKeyValue 校验 KEY=VALUE 解析：仅以首个 '=' 分隔（R1.5），
// 缺 '=' 或空 key 报错（R1.4），允许空值（KEY=）。
func TestParseVaultKeyValue(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantKey   string
		wantValue string
		wantErr   bool
	}{
		{name: "simple", in: "K=V", wantKey: "K", wantValue: "V"},
		{name: "value with equals", in: "K=a=b=c", wantKey: "K", wantValue: "a=b=c"},
		{name: "url value", in: "URL=http://x?a=b", wantKey: "URL", wantValue: "http://x?a=b"},
		{name: "empty value ok", in: "K=", wantKey: "K", wantValue: ""},
		{name: "no equals", in: "novalue", wantErr: true},
		{name: "empty key", in: "=V", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, val, err := parseVaultKeyValue(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseVaultKeyValue(%q) expected error, got key=%q val=%q", tc.in, key, val)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVaultKeyValue(%q) unexpected error: %v", tc.in, err)
			}
			if key != tc.wantKey || val != tc.wantValue {
				t.Fatalf("parseVaultKeyValue(%q) = (%q, %q), want (%q, %q)", tc.in, key, val, tc.wantKey, tc.wantValue)
			}
		})
	}
}

// TestVaultMasterKeyAtGeneratesWith0600 验证主密钥文件不存在时首用自动生成、
// 文件权限 0600、返回 32 字节密钥（R6.1/6.2）。
func TestVaultMasterKeyAtGeneratesWith0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "vault.key") // 含不存在的子目录以校验自动建目录

	key, err := vaultMasterKeyAt(path)
	if err != nil {
		t.Fatalf("vaultMasterKeyAt first call: unexpected error: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("master key length = %d, want 32", len(key))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat generated key file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key file perm = %o, want 0600", perm)
	}
}

// TestVaultMasterKeyAtIdempotent 验证再次调用返回与首用一致的同一密钥（多端/
// 多次操作一致性，R7.4 的本机前提；幂等加载）。
func TestVaultMasterKeyAtIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.key")

	first, err := vaultMasterKeyAt(path)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := vaultMasterKeyAt(path)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("master key not idempotent: first=%x second=%x", first, second)
	}
}

// TestVaultMasterKeyAtCorruptNoOverwrite 验证文件损坏/长度异常时返回明确错误，
// 且不静默覆盖生成新密钥（R6.5）。
func TestVaultMasterKeyAtCorruptNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.key")

	garbage := []byte("not-a-valid-base64-32-byte-key!!")
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatalf("seed corrupt key file: %v", err)
	}

	if _, err := vaultMasterKeyAt(path); err == nil {
		t.Fatalf("vaultMasterKeyAt on corrupt file: expected error, got nil")
	}

	// 文件内容必须保持不变（未被覆盖生成）。
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read key file: %v", err)
	}
	if !bytes.Equal(after, garbage) {
		t.Fatalf("corrupt key file was overwritten: got %q, want %q", after, garbage)
	}
}

// TestVaultCmdRegistered 验证 vaultCmd 已注册到 rootCmd，且其 RunE（无子命令时）
// 渲染帮助而不报错（完成态：bk vault --help 显示 vault 命令）。
func TestVaultCmdRegistered(t *testing.T) {
	var found bool
	for _, c := range rootCmd.Commands() {
		if c.Name() == "vault" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("vaultCmd not registered on rootCmd")
	}

	var buf bytes.Buffer
	vaultCmd.SetOut(&buf)
	vaultCmd.SetErr(&buf)
	if err := vaultCmd.RunE(vaultCmd, nil); err != nil {
		t.Fatalf("vaultCmd.RunE returned error: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatalf("vaultCmd help produced no output")
	}
}
