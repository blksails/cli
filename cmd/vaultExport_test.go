/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/vault"
)

// fakeVaultListerFull 实现 vaultListerFull，注入受控的完整记录（含密文 value）/err，
// 使 runVaultExport 可在不触达真实 Supabase 的前提下被验证。
type fakeVaultListerFull struct {
	secrets  []vault.Secret
	err      error
	gotApp   string
	gotCalls int
}

func (f *fakeVaultListerFull) List(app string) ([]vault.Secret, error) {
	f.gotCalls++
	f.gotApp = app
	if f.err != nil {
		return nil, f.err
	}
	return f.secrets, nil
}

// mapDecrypt 构造一个 fake decrypt：按 ciphertext→plaintext 映射；命中即成功，未命中则失败
// （模拟密文被篡改 / 主密钥不匹配）。
func mapDecrypt(m map[string]string) func(key []byte, ciphertext string) (string, error) {
	return func(_ []byte, ciphertext string) (string, error) {
		if pt, ok := m[ciphertext]; ok {
			return pt, nil
		}
		return "", fmt.Errorf("vault: 解密失败（模拟篡改）: %q", ciphertext)
	}
}

// 多 key 成功：List 返回 2 条（A/B），全部解密成功 → 输出恰为 "A=plainA\nB=plainB\n"
// 的 env 文本（KEY=VALUE 每行一条、稳定顺序），可被 bk app config:set 消费（R5.1/R5.2）。
func TestRunVaultExport_MultiSuccess(t *testing.T) {
	lister := &fakeVaultListerFull{secrets: []vault.Secret{
		{App: "myapp", Key: "A", Value: "cipherA"},
		{App: "myapp", Key: "B", Value: "cipherB"},
	}}
	decrypt := mapDecrypt(map[string]string{"cipherA": "plainA", "cipherB": "plainB"})
	var buf bytes.Buffer

	if err := runVaultExport(&buf, "myapp", []byte("k"), lister, decrypt); err != nil {
		t.Fatalf("runVaultExport returned error: %v", err)
	}
	if lister.gotApp != "myapp" {
		t.Errorf("List called with app=%q, want %q", lister.gotApp, "myapp")
	}

	const want = "A=plainA\nB=plainB\n"
	if buf.String() != want {
		t.Fatalf("output = %q, want %q", buf.String(), want)
	}

	// 断言为合法 env 文本：每非空行恰为一个 KEY=VALUE，KEY 非空。
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		k, _, ok := strings.Cut(line, "=")
		if !ok || k == "" {
			t.Errorf("line %q is not a valid KEY=VALUE env entry", line)
		}
	}
}

// TAMPER（R5.3 关键安全测试）：List 返回 2 条，第一条解密成功、第二条失败（模拟被篡改）
// → runVaultExport 返回非 nil 错误，且 w 完全为空：第一条明文 "plainA" 绝不出现。
// 证明无任何部分明文泄露。
func TestRunVaultExport_TamperedSecondYieldsEmptyOutput(t *testing.T) {
	lister := &fakeVaultListerFull{secrets: []vault.Secret{
		{App: "myapp", Key: "A", Value: "cipherA"},
		{App: "myapp", Key: "B", Value: "cipherB-TAMPERED"},
	}}
	// 仅 cipherA 可解密；cipherB-TAMPERED 未命中 → decrypt 失败。
	decrypt := mapDecrypt(map[string]string{"cipherA": "plainA"})
	var buf bytes.Buffer

	err := runVaultExport(&buf, "myapp", []byte("k"), lister, decrypt)
	if err == nil {
		t.Fatal("expected non-nil error on tampered ciphertext, got nil")
	}
	// 关键不变量：w 必须 0 字节——绝不输出已部分解密的明文（R5.3）。
	if buf.Len() != 0 {
		t.Fatalf("expected ZERO bytes on w after tamper, got %d bytes: %q", buf.Len(), buf.String())
	}
	// 第一条明文绝不泄露。
	if strings.Contains(buf.String(), "plainA") {
		t.Fatalf("partial plaintext %q leaked to w: %q", "plainA", buf.String())
	}
}

// 空 app：List 返回 [] → w 完全为空，nil 错误（零退出）（R5.4：输出空内容）。
func TestRunVaultExport_Empty(t *testing.T) {
	lister := &fakeVaultListerFull{secrets: []vault.Secret{}}
	decrypt := mapDecrypt(map[string]string{})
	var buf bytes.Buffer

	if err := runVaultExport(&buf, "emptyapp", []byte("k"), lister, decrypt); err != nil {
		t.Fatalf("runVaultExport returned error on empty app: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected empty output for empty app, got %d bytes: %q", buf.Len(), buf.String())
	}
}

// lister 错误：返回非 nil（→ 非零退出），且不向 w 写出任何内容。
func TestRunVaultExport_ListerError(t *testing.T) {
	wantErr := errors.New("boom")
	lister := &fakeVaultListerFull{err: wantErr}
	decrypt := mapDecrypt(map[string]string{})
	var buf bytes.Buffer

	err := runVaultExport(&buf, "myapp", []byte("k"), lister, decrypt)
	if err == nil {
		t.Fatal("expected error from lister failure, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error %v does not wrap underlying %v", err, wantErr)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output on lister error, got %q", buf.String())
	}
}

// 命令装配：vaultExportCmd 要求恰一个参数（ExactArgs(1)），并 self-register 到 vaultCmd。
func TestVaultExportCmd_ExactArgs(t *testing.T) {
	if vaultExportCmd.Args == nil {
		t.Fatal("vaultExportCmd.Args is nil, want cobra.ExactArgs(1)")
	}
	if err := vaultExportCmd.Args(vaultExportCmd, []string{"app"}); err != nil {
		t.Errorf("ExactArgs(1) rejected 1 arg: %v", err)
	}
	if err := vaultExportCmd.Args(vaultExportCmd, []string{}); err == nil {
		t.Error("ExactArgs(1) accepted 0 args, want error")
	}
	if err := vaultExportCmd.Args(vaultExportCmd, []string{"app", "extra"}); err == nil {
		t.Error("ExactArgs(1) accepted 2 args, want error")
	}

	// 确认已注册到 vaultCmd 命令组。
	var found bool
	for _, c := range vaultCmd.Commands() {
		if c == vaultExportCmd {
			found = true
			break
		}
	}
	if !found {
		t.Error("vaultExportCmd not registered on vaultCmd")
	}
}
