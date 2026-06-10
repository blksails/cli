package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pkg.blksails.net/bk/internal/sshkeys"
)

// sshKey_e2e_test.go 是「SSH 密钥发放」特性的端到端回归（Task 4.2）：以注入式把四个
// 可测核心串联成完整生命周期 provision → list(pending) → install(管理员) → list(installed)
// → revoke(revoked)，验证状态机 pending→installed→revoked 的真实流转、审计字段（操作者+时间）
// 被写入，且全程任何输出都不含私钥串（Requirement 1.1/2.1/4.1/5.1/6.2/10.1/10.2/10.3/10.4）。
//
// 本测试仅依赖既有生产缝（runProvision / runSSHKeyList / runSSHKeyInstall / runSSHKeyRevoke
// 及其注入接口），不触碰任何生产代码或其它 _test.go。fake 命名以 e2e 前缀避免与既有
// fakeRegisterer/fakeKeyLister/fakePendingStore/fakeRevokeStore/fakeKeyInstaller/fakeKeyRemover 冲突。

// e2eStore 是一个 backing 单一共享 map 的内存存储，单结构即满足
// keyRegisterer + keyLister + pendingStore + revokeStore 四个注入接口，
// 使状态可跨 provision/list/install/revoke 各步骤连续流转。
type e2eStore struct {
	recs   map[string]sshkeys.KeyRecord // 以 ID 为键的共享状态
	nextID int
	now    func() string // 注入式时间源，保证 installed_at/revoked_at 稳定可断言
}

func newE2EStore() *e2eStore {
	return &e2eStore{
		recs: map[string]sshkeys.KeyRecord{},
		now: func() string {
			return time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
		},
	}
}

// Register 分配 ID 并以 pending 落库（满足 keyRegisterer，对齐 *sshkeys.Store 语义）。
func (s *e2eStore) Register(rec sshkeys.KeyRecord) (sshkeys.KeyRecord, error) {
	s.nextID++
	rec.ID = fmt.Sprintf("key-%d", s.nextID)
	rec.Status = sshkeys.StatusPending
	rec.CreatedAt = s.now()
	s.recs[rec.ID] = rec
	return rec, nil
}

// ListMine 返回全部记录（满足 keyLister）。E2E 中单用户视角即“我的全部记录”。
func (s *e2eStore) ListMine() ([]sshkeys.KeyRecord, error) {
	out := make([]sshkeys.KeyRecord, 0, len(s.recs))
	for _, r := range s.recs {
		out = append(out, r)
	}
	return out, nil
}

// ListPending 仅返回 status==pending 的记录（满足 pendingStore）。
func (s *e2eStore) ListPending() ([]sshkeys.KeyRecord, error) {
	out := make([]sshkeys.KeyRecord, 0, len(s.recs))
	for _, r := range s.recs {
		if r.Status == sshkeys.StatusPending {
			out = append(out, r)
		}
	}
	return out, nil
}

// MarkInstalled 置 status=installed 并写入 installed_by/installed_at（满足 pendingStore，
// 审计字段 Requirement 5.3/10.3）。
func (s *e2eStore) MarkInstalled(id, by string) error {
	r, ok := s.recs[id]
	if !ok {
		return sshkeys.ErrNotFound
	}
	r.Status = sshkeys.StatusInstalled
	r.InstalledBy = by
	r.InstalledAt = s.now()
	s.recs[id] = r
	return nil
}

// Find 按指纹或名称定位记录（满足 revokeStore，Requirement 6.1）。
func (s *e2eStore) Find(ref string) (sshkeys.KeyRecord, error) {
	for _, r := range s.recs {
		if r.Fingerprint == ref || r.Name == ref {
			return r, nil
		}
	}
	return sshkeys.KeyRecord{}, sshkeys.ErrNotFound
}

// MarkRevoked 置 status=revoked 并写入 revoked_by/revoked_at（满足 revokeStore，
// 审计字段 Requirement 6.2/10.3）。
func (s *e2eStore) MarkRevoked(id, by string) error {
	r, ok := s.recs[id]
	if !ok {
		return sshkeys.ErrNotFound
	}
	r.Status = sshkeys.StatusRevoked
	r.RevokedBy = by
	r.RevokedAt = s.now()
	s.recs[id] = r
	return nil
}

// e2eDokku 是一个内存 Dokku 替身，单结构即满足 keyInstaller + keyRemover：
// 记录 Add/Remove 的调用参数，恒返回成功，使 install/revoke 编排不触网。
type e2eDokku struct {
	addNames []string
	addPubs  []string
	rmNames  []string
}

func (d *e2eDokku) SSHKeysAdd(ctx context.Context, name, publicKey string) (string, error) {
	d.addNames = append(d.addNames, name)
	d.addPubs = append(d.addPubs, publicKey)
	return "ok", nil
}

func (d *e2eDokku) SSHKeysRemove(ctx context.Context, name string) (string, error) {
	d.rmNames = append(d.rmNames, name)
	return "ok", nil
}

// e2ePrivateKeyMarkers 是任何私钥落盘内容都会含有的 PEM 头标记。若它们出现在任一步骤的
// 用户输出里，即视为私钥泄漏（Requirement 10.1/10.2）。命名加 e2e 前缀避免与
// sshKeyProvision_test.go 的 pemMarker/assertNoPrivateKeyLeak 冲突。
var e2ePrivateKeyMarkers = []string{"PRIVATE KEY", "BEGIN OPENSSH PRIVATE KEY"}

func assertNoKeyLeakE2E(t *testing.T, label, out string) {
	t.Helper()
	for _, m := range e2ePrivateKeyMarkers {
		if strings.Contains(out, m) {
			t.Fatalf("[%s] 输出疑似泄漏私钥（含标记 %q）：\n%s", label, m, out)
		}
	}
}

// TestSSHKeyLifecycle_EndToEnd 串联真实 keygen + 四个可测核心，断言完整生命周期、
// 状态机流转 pending→installed→revoked、审计字段写入与全程无私钥泄漏。
func TestSSHKeyLifecycle_EndToEnd(t *testing.T) {
	store := newE2EStore()
	dokku := &e2eDokku{}
	ctx := context.Background()
	const adminID = "admin-uid"

	// 用真实 keygen 落盘到 t.TempDir，保证 Requirement 1.1 的「本机生成 + 私钥落盘」连续性。
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")

	// 收集每一步的输出，最后统一做私钥泄漏断言（Requirement 10.1/10.2）。
	var allOutputs []string

	// ---- 步骤 1：provision（真实 keygen + 登记 pending）----
	var provBuf bytes.Buffer
	opts := provisionOpts{
		host:                 "dokku.example.com",
		keyPath:              keyPath,
		force:                false,
		name:                 "bk-alice-dokku.example.com",
		dokkuUser:            "dokku",
		setIdentityRequested: false,
		comment:              "alice@example.com dokku.example.com",
	}
	// setIdentity 不应被调用（setIdentityRequested=false）；调用即失败。
	noSetIdentity := func(string) error {
		t.Fatalf("setIdentity 不应被调用（setIdentityRequested=false）")
		return nil
	}
	if err := runProvision(&provBuf, opts, store, noSetIdentity); err != nil {
		t.Fatalf("runProvision 失败：%v", err)
	}
	allOutputs = append(allOutputs, provBuf.String())

	// 私钥确实落盘（1.1 连续性）：文件存在且权限 0600。
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("私钥未落盘：%v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("私钥权限应为 0600，实际 %o", perm)
	}

	// store 现有 1 条 pending 记录（Requirement 2.1）。
	if len(store.recs) != 1 {
		t.Fatalf("provision 后应有 1 条登记，实际 %d", len(store.recs))
	}
	var rec sshkeys.KeyRecord
	for _, r := range store.recs {
		rec = r
	}
	if rec.Status != sshkeys.StatusPending {
		t.Fatalf("provision 后状态应为 pending，实际 %s", rec.Status)
	}
	if rec.Fingerprint == "" || !strings.HasPrefix(rec.Fingerprint, "SHA256:") {
		t.Fatalf("登记应含 SHA256 指纹，实际 %q", rec.Fingerprint)
	}
	// 输出含指纹（Requirement 2.5）。
	if !strings.Contains(provBuf.String(), rec.Fingerprint) {
		t.Fatalf("provision 输出应含指纹 %q，实际：\n%s", rec.Fingerprint, provBuf.String())
	}
	assertNoKeyLeakE2E(t, "provision", provBuf.String())

	// ---- 步骤 2：list（pending）----
	var listPendingBuf bytes.Buffer
	if err := runSSHKeyList(&listPendingBuf, store); err != nil {
		t.Fatalf("runSSHKeyList(pending) 失败：%v", err)
	}
	allOutputs = append(allOutputs, listPendingBuf.String())
	if !strings.Contains(listPendingBuf.String(), string(sshkeys.StatusPending)) {
		t.Fatalf("list 应显示 pending 状态，实际：\n%s", listPendingBuf.String())
	}
	if !strings.Contains(listPendingBuf.String(), rec.Name) {
		t.Fatalf("list 应显示记录名称 %q，实际：\n%s", rec.Name, listPendingBuf.String())
	}
	assertNoKeyLeakE2E(t, "list-pending", listPendingBuf.String())

	// ---- 步骤 3：install（管理员）----
	var installBuf bytes.Buffer
	if err := runSSHKeyInstall(ctx, &installBuf, store, dokku, adminID); err != nil {
		t.Fatalf("runSSHKeyInstall 失败：%v", err)
	}
	allOutputs = append(allOutputs, installBuf.String())

	// fakeDokku.Add 以记录的 name + publicKey 被调用（Requirement 5.2）。
	if len(dokku.addNames) != 1 || dokku.addNames[0] != rec.Name {
		t.Fatalf("install 应以名称 %q 调用 SSHKeysAdd，实际 %v", rec.Name, dokku.addNames)
	}
	if len(dokku.addPubs) != 1 || dokku.addPubs[0] != rec.PublicKey {
		t.Fatalf("install 应以记录公钥调用 SSHKeysAdd，实际 %v", dokku.addPubs)
	}

	// store 记录回写 installed + installed_by + installed_at（Requirement 5.3/10.3）。
	installed := store.recs[rec.ID]
	if installed.Status != sshkeys.StatusInstalled {
		t.Fatalf("install 后状态应为 installed，实际 %s", installed.Status)
	}
	if installed.InstalledBy != adminID {
		t.Fatalf("installed_by 应为 %q，实际 %q", adminID, installed.InstalledBy)
	}
	if installed.InstalledAt == "" {
		t.Fatalf("installed_at 应被写入，实际为空")
	}
	assertNoKeyLeakE2E(t, "install", installBuf.String())

	// ---- 步骤 4：list（installed）----
	var listInstalledBuf bytes.Buffer
	if err := runSSHKeyList(&listInstalledBuf, store); err != nil {
		t.Fatalf("runSSHKeyList(installed) 失败：%v", err)
	}
	allOutputs = append(allOutputs, listInstalledBuf.String())
	if !strings.Contains(listInstalledBuf.String(), string(sshkeys.StatusInstalled)) {
		t.Fatalf("list 应显示 installed 状态，实际：\n%s", listInstalledBuf.String())
	}
	assertNoKeyLeakE2E(t, "list-installed", listInstalledBuf.String())

	// ---- 步骤 5：revoke（管理员，按指纹定位）----
	var revokeBuf bytes.Buffer
	if err := runSSHKeyRevoke(ctx, &revokeBuf, store, dokku, rec.Fingerprint, adminID); err != nil {
		t.Fatalf("runSSHKeyRevoke 失败：%v", err)
	}
	allOutputs = append(allOutputs, revokeBuf.String())

	// fakeDokku.Remove 被调用（Requirement 6.1）。install 的幂等前置 Remove + revoke 的 Remove。
	if len(dokku.rmNames) == 0 {
		t.Fatalf("revoke 应调用 SSHKeysRemove")
	}
	if last := dokku.rmNames[len(dokku.rmNames)-1]; last != rec.Name {
		t.Fatalf("revoke 应以名称 %q 调用 SSHKeysRemove，实际 %q", rec.Name, last)
	}

	// store 记录回写 revoked + revoked_by + revoked_at（Requirement 6.2/10.3）。
	revoked := store.recs[rec.ID]
	if revoked.Status != sshkeys.StatusRevoked {
		t.Fatalf("revoke 后状态应为 revoked，实际 %s", revoked.Status)
	}
	if revoked.RevokedBy != adminID {
		t.Fatalf("revoked_by 应为 %q，实际 %q", adminID, revoked.RevokedBy)
	}
	if revoked.RevokedAt == "" {
		t.Fatalf("revoked_at 应被写入，实际为空")
	}
	// 安装审计字段在吊销后仍保留（Requirement 10.3：操作者与时间保留）。
	if revoked.InstalledBy != adminID || revoked.InstalledAt == "" {
		t.Fatalf("revoke 后应保留安装审计字段，实际 installed_by=%q installed_at=%q",
			revoked.InstalledBy, revoked.InstalledAt)
	}
	assertNoKeyLeakE2E(t, "revoke", revokeBuf.String())

	// ---- 全程私钥泄漏断言（Requirement 10.1/10.2）----
	for i, out := range allOutputs {
		assertNoKeyLeakE2E(t, fmt.Sprintf("all-outputs[%d]", i), out)
	}

	// ---- 状态机路径断言：pending→installed→revoked ----
	// 终态为 revoked，且全程历经 installed（由 install 步骤的 installed_at 非空佐证）。
	final := store.recs[rec.ID]
	if final.Status != sshkeys.StatusRevoked {
		t.Fatalf("终态应为 revoked，实际 %s", final.Status)
	}
	if final.InstalledAt == "" {
		t.Fatalf("状态机应历经 installed（installed_at 非空），但为空")
	}
}
