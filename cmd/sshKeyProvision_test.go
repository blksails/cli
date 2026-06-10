package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/sshkeys"
)

// fakeRegisterer 是 keyRegisterer 的可注入测试替身：记录收到的 KeyRecord，
// 并按需返回错误或成功 representation，使 runProvision 的登记分支无需触达 Supabase。
type fakeRegisterer struct {
	called bool
	got    sshkeys.KeyRecord
	ret    sshkeys.KeyRecord
	err    error
}

func (f *fakeRegisterer) Register(rec sshkeys.KeyRecord) (sshkeys.KeyRecord, error) {
	f.called = true
	f.got = rec
	if f.err != nil {
		return sshkeys.KeyRecord{}, f.err
	}
	out := f.ret
	// 默认回写与入参一致的指纹/状态，模拟 DB representation。
	if out.Fingerprint == "" {
		out.Fingerprint = rec.Fingerprint
	}
	if out.Status == "" {
		out.Status = sshkeys.StatusPending
	}
	return out, nil
}

// fakeSetIdentity 记录是否被调用以及收到的私钥路径，并按需返回错误。
type fakeSetIdentity struct {
	called bool
	path   string
	err    error
}

func (f *fakeSetIdentity) fn(path string) error {
	f.called = true
	f.path = path
	return f.err
}

// pemMarker 是 OpenSSH 私钥 PEM 的起始标记；任何输出含此标记即视为泄露私钥。
const pemMarker = "PRIVATE KEY"

func baseOpts(keyPath string) provisionOpts {
	return provisionOpts{
		host:                 "h1.example.com",
		keyPath:              keyPath,
		force:                false,
		name:                 "bk-alice-h1-example-com",
		dokkuUser:            "dokku",
		setIdentityRequested: false,
		comment:              "alice@example.com h1.example.com",
	}
}

// (a) 登记失败：runProvision 返回非 nil 错误，提示「未登记 / 可重试」，
// 且私钥文件 WAS 落盘（落盘成功但登记失败）。(Req 1.5 / 2.4)
func TestRunProvision_RegisterFails_WritesKeyButReturnsError(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "h1.key")

	reg := &fakeRegisterer{err: errors.New("network down")}
	si := &fakeSetIdentity{}

	var buf bytes.Buffer
	err := runProvision(&buf, baseOpts(keyPath), reg, si.fn)
	if err == nil {
		t.Fatalf("expected error when register fails, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "未登记") || !strings.Contains(msg, "可重试") {
		t.Errorf("error message should mention 未登记/可重试, got: %q", msg)
	}

	// 私钥已落盘（keygen+WritePrivateKey 先于 register）。
	info, statErr := os.Stat(keyPath)
	if statErr != nil {
		t.Fatalf("private key should have been written before register, stat err: %v", statErr)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("private key perm = %o, want 0600", perm)
	}
	if !reg.called {
		t.Errorf("register should have been called (write succeeded)")
	}
	assertNoPrivateKeyLeak(t, buf.String(), msg)
}

// (b) 私钥已存在且未 --force：返回错误、不调用 register、原文件不变。(Req 1.4)
func TestRunProvision_ExistsNoForce_DoesNotRegisterOrOverwrite(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "h1.key")
	original := []byte("ORIGINAL-PRESERVED-CONTENT")
	if err := os.WriteFile(keyPath, original, 0o600); err != nil {
		t.Fatalf("setup: write original key: %v", err)
	}

	reg := &fakeRegisterer{}
	si := &fakeSetIdentity{}

	var buf bytes.Buffer
	err := runProvision(&buf, baseOpts(keyPath), reg, si.fn)
	if err == nil {
		t.Fatalf("expected error when key exists and force=false, got nil")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should tell user to use --force, got: %q", err.Error())
	}
	if reg.called {
		t.Errorf("register must NOT be called when key exists and !force")
	}
	got, _ := os.ReadFile(keyPath)
	if !bytes.Equal(got, original) {
		t.Errorf("original key file must be unchanged; got %q", string(got))
	}
	assertNoPrivateKeyLeak(t, buf.String(), err.Error())
}

// (c) 成功：register 收到 Status pending + 正确 Name/Host/PublicKey/Fingerprint，
// 输出含指纹。(Req 2.1 / 2.5)
func TestRunProvision_Success_RegistersPendingAndPrintsFingerprint(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "h1.key")

	reg := &fakeRegisterer{}
	si := &fakeSetIdentity{}

	var buf bytes.Buffer
	opts := baseOpts(keyPath)
	if err := runProvision(&buf, opts, reg, si.fn); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reg.called {
		t.Fatalf("register should have been called on success")
	}
	if reg.got.Status != sshkeys.StatusPending {
		t.Errorf("registered Status = %q, want pending", reg.got.Status)
	}
	if reg.got.Name != opts.name {
		t.Errorf("registered Name = %q, want %q", reg.got.Name, opts.name)
	}
	if reg.got.Host != opts.host {
		t.Errorf("registered Host = %q, want %q", reg.got.Host, opts.host)
	}
	if reg.got.DokkuUser != opts.dokkuUser {
		t.Errorf("registered DokkuUser = %q, want %q", reg.got.DokkuUser, opts.dokkuUser)
	}
	if !strings.HasPrefix(reg.got.PublicKey, "ssh-ed25519 ") {
		t.Errorf("registered PublicKey should be an authorized_keys line, got: %q", reg.got.PublicKey)
	}
	if !strings.HasPrefix(reg.got.Fingerprint, "SHA256:") {
		t.Errorf("registered Fingerprint should be SHA256:..., got: %q", reg.got.Fingerprint)
	}

	out := buf.String()
	if !strings.Contains(out, reg.got.Fingerprint) {
		t.Errorf("output should contain the fingerprint %q; got: %q", reg.got.Fingerprint, out)
	}
	if !strings.Contains(out, string(sshkeys.StatusPending)) {
		t.Errorf("output should confirm status pending; got: %q", out)
	}
	assertNoPrivateKeyLeak(t, out)
}

// (d) setIdentityRequested=true → setIdentity 收到私钥路径；
//
//	requested=false → setIdentity 不调用且输出提示私钥路径。(Req 3.1 / 3.2)
func TestRunProvision_SetIdentityToggle(t *testing.T) {
	// requested=true
	dir1 := t.TempDir()
	keyPath1 := filepath.Join(dir1, "h1.key")
	reg1 := &fakeRegisterer{}
	si1 := &fakeSetIdentity{}
	opts1 := baseOpts(keyPath1)
	opts1.setIdentityRequested = true
	var buf1 bytes.Buffer
	if err := runProvision(&buf1, opts1, reg1, si1.fn); err != nil {
		t.Fatalf("unexpected error (requested=true): %v", err)
	}
	if !si1.called {
		t.Errorf("setIdentity should be called when requested")
	}
	if si1.path != keyPath1 {
		t.Errorf("setIdentity got path %q, want %q", si1.path, keyPath1)
	}
	assertNoPrivateKeyLeak(t, buf1.String())

	// requested=false
	dir2 := t.TempDir()
	keyPath2 := filepath.Join(dir2, "h1.key")
	reg2 := &fakeRegisterer{}
	si2 := &fakeSetIdentity{}
	opts2 := baseOpts(keyPath2)
	opts2.setIdentityRequested = false
	var buf2 bytes.Buffer
	if err := runProvision(&buf2, opts2, reg2, si2.fn); err != nil {
		t.Fatalf("unexpected error (requested=false): %v", err)
	}
	if si2.called {
		t.Errorf("setIdentity must NOT be called when not requested")
	}
	if !strings.Contains(buf2.String(), keyPath2) {
		t.Errorf("output should mention the private key path %q when not requested; got: %q", keyPath2, buf2.String())
	}
	assertNoPrivateKeyLeak(t, buf2.String())
}

// assertNoPrivateKeyLeak 断言所有给定字符串都不含私钥 PEM 标记（Req 10.1/10.2）。
func assertNoPrivateKeyLeak(t *testing.T, strs ...string) {
	t.Helper()
	for _, s := range strs {
		if strings.Contains(s, pemMarker) {
			t.Errorf("output/error leaked a private-key PEM marker %q in: %q", pemMarker, s)
		}
	}
}
