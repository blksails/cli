package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/vault"
)

// fakeVaultGetter 返回预置密文或错误，使 runVaultGet 可在不触达真实 Supabase 的前提下
// 被验证（found / not-found / 其它存储错误三条读路径）。
type fakeVaultGetter struct {
	ciphertext string
	err        error
}

func (f *fakeVaultGetter) Get(app, key string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.ciphertext, nil
}

// fakeDecryptOK 把可识别的「密文」还原为明文，断言输出确实来自解密产物。
func fakeDecryptOK(plaintext string) func(key []byte, ciphertext string) (string, error) {
	return func(key []byte, ciphertext string) (string, error) {
		return plaintext, nil
	}
}

// fakeDecryptErr 模拟解密失败（主密钥不符 / 密文被篡改）。
func fakeDecryptErr(key []byte, ciphertext string) (string, error) {
	return "", errors.New("decrypt boom")
}

func TestRunVaultGet_Found_OutputsOnlyPlaintext(t *testing.T) {
	var w bytes.Buffer
	const cipher = "tampered-or-real-cipher"
	const plaintext = "supersecret"
	getter := &fakeVaultGetter{ciphertext: cipher}

	err := runVaultGet(&w, "myapp", "DB_PASSWORD", []byte("k"), getter, fakeDecryptOK(plaintext))
	if err != nil {
		t.Fatalf("runVaultGet 返回错误：%v", err)
	}

	// 仅输出明文 VALUE 本身（R2.1/R2.4）：去除尾部换行后必须严格等于明文。
	if got := strings.TrimRight(w.String(), "\n"); got != plaintext {
		t.Fatalf("输出非纯明文：期望 %q，实际 %q", plaintext, got)
	}
	// 不得附带 key 名或密文。
	if strings.Contains(w.String(), "DB_PASSWORD") {
		t.Fatalf("输出泄露了 key 名：%q", w.String())
	}
	if strings.Contains(w.String(), cipher) {
		t.Fatalf("输出泄露了密文：%q", w.String())
	}
}

func TestRunVaultGet_NotFound_NonZeroNoPlaintext(t *testing.T) {
	var w bytes.Buffer
	getter := &fakeVaultGetter{err: fmt.Errorf("app=%q key=%q: %w", "myapp", "MISSING", vault.ErrNotFound)}

	err := runVaultGet(&w, "myapp", "MISSING", []byte("k"), getter, fakeDecryptOK("should-not-appear"))
	if err == nil {
		t.Fatalf("未找到时应返回错误，但返回 nil")
	}
	if !errors.Is(err, vault.ErrNotFound) {
		t.Fatalf("未找到错误应可经 errors.Is 识别为 ErrNotFound：%v", err)
	}
	// 未找到路径绝不输出任何明文（w 必须为空）。
	if w.Len() != 0 {
		t.Fatalf("未找到时不应有任何输出，实际：%q", w.String())
	}
}

func TestRunVaultGet_DecryptFailure_NonZeroNoLeak(t *testing.T) {
	var w bytes.Buffer
	const cipher = "tampered-cipher"
	getter := &fakeVaultGetter{ciphertext: cipher}

	err := runVaultGet(&w, "myapp", "DB_PASSWORD", []byte("wrong-key"), getter, fakeDecryptErr)
	if err == nil {
		t.Fatalf("解密失败时应返回错误，但返回 nil")
	}
	// 关键安全断言：解密失败时不得输出任何明文，也不得泄露密文（R2.3）。
	if w.Len() != 0 {
		t.Fatalf("解密失败时输出必须为空，实际：%q", w.String())
	}
	if strings.Contains(w.String(), cipher) {
		t.Fatalf("解密失败时泄露了密文：%q", w.String())
	}
}

func TestRunVaultGet_OtherStoreError_NonZeroNoPlaintext(t *testing.T) {
	var w bytes.Buffer
	getter := &fakeVaultGetter{err: errors.New("store boom")}

	err := runVaultGet(&w, "myapp", "KEY", []byte("k"), getter, fakeDecryptOK("should-not-appear"))
	if err == nil {
		t.Fatalf("存储错误时应返回错误，但返回 nil")
	}
	if w.Len() != 0 {
		t.Fatalf("存储错误时不应有任何输出，实际：%q", w.String())
	}
}

func TestVaultGetCmd_ExactArgs2(t *testing.T) {
	if vaultGetCmd.Args == nil {
		t.Fatalf("vaultGetCmd.Args 未设置，应为 cobra.ExactArgs(2)")
	}
	// 少于 2 个参数应报错。
	if err := vaultGetCmd.Args(vaultGetCmd, []string{"onlyapp"}); err == nil {
		t.Fatalf("仅 1 个参数应被 ExactArgs(2) 拒绝")
	}
	// 多于 2 个参数应报错。
	if err := vaultGetCmd.Args(vaultGetCmd, []string{"app", "key", "extra"}); err == nil {
		t.Fatalf("3 个参数应被 ExactArgs(2) 拒绝")
	}
	// 恰好 2 个参数应通过。
	if err := vaultGetCmd.Args(vaultGetCmd, []string{"app", "key"}); err != nil {
		t.Fatalf("恰好 2 个参数应通过，实际：%v", err)
	}
	// 必须 self-register 到 vaultCmd。
	found := false
	for _, c := range vaultCmd.Commands() {
		if c == vaultGetCmd {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("vaultGetCmd 未注册到 vaultCmd")
	}
}
