package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/sshkeys"
)

// fakeRevokeStore 是 revokeStore 的可注入测试替身：按预置返回 Find 的记录/错误，
// 并记录 MarkRevoked 的调用（id→by），使 runSSHKeyRevoke 可在不触达 Supabase 的
// 前提下被验证（design「Unit（revoke 编排，注入 fake Store + fake dokku）」）。
type fakeRevokeStore struct {
	rec        sshkeys.KeyRecord
	findErr    error
	marked     map[string]string // id -> by
	markCalled bool
	markErr    error
}

func (f *fakeRevokeStore) Find(ref string) (sshkeys.KeyRecord, error) {
	if f.findErr != nil {
		return sshkeys.KeyRecord{}, f.findErr
	}
	return f.rec, nil
}

func (f *fakeRevokeStore) MarkRevoked(id, by string) error {
	f.markCalled = true
	if f.markErr != nil {
		return f.markErr
	}
	if f.marked == nil {
		f.marked = map[string]string{}
	}
	f.marked[id] = by
	return nil
}

// fakeKeyRemover 是 keyRemover 的可注入测试替身：记录 SSHKeysRemove 的调用（name），
// 并可预置返回错误，以覆盖移除失败时不误标 revoked 的路径。
type fakeKeyRemover struct {
	removed   []string // 调用过的 name
	removeErr error
	callCount int
}

func (f *fakeKeyRemover) SSHKeysRemove(ctx context.Context, name string) (string, error) {
	f.callCount++
	f.removed = append(f.removed, name)
	if f.removeErr != nil {
		return "", f.removeErr
	}
	return "ok", nil
}

// (a) Find 返回 ErrNotFound：零退出、友好提示、不调用 Remove/MarkRevoked（幂等，Req 6.3）。
func TestRunSSHKeyRevoke_NotFound(t *testing.T) {
	store := &fakeRevokeStore{findErr: sshkeys.ErrNotFound}
	rem := &fakeKeyRemover{}
	var buf bytes.Buffer

	err := runSSHKeyRevoke(context.Background(), &buf, store, rem, "deadbeef", "admin-uid")
	if err != nil {
		t.Fatalf("期望 nil error（幂等零退出），得到：%v", err)
	}
	if rem.callCount != 0 {
		t.Errorf("不应调用 SSHKeysRemove，实际调用 %d 次", rem.callCount)
	}
	if store.markCalled {
		t.Errorf("不应调用 MarkRevoked")
	}
	if !strings.Contains(buf.String(), "未找到") {
		t.Errorf("期望友好的未找到提示，实际输出：%q", buf.String())
	}
}

// (b) 记录已是 StatusRevoked：零退出、友好提示、不调用 Remove/MarkRevoked（幂等，Req 6.3）。
func TestRunSSHKeyRevoke_AlreadyRevoked(t *testing.T) {
	store := &fakeRevokeStore{rec: sshkeys.KeyRecord{
		ID:     "id-1",
		Name:   "bk-alice-host",
		Status: sshkeys.StatusRevoked,
	}}
	rem := &fakeKeyRemover{}
	var buf bytes.Buffer

	err := runSSHKeyRevoke(context.Background(), &buf, store, rem, "bk-alice-host", "admin-uid")
	if err != nil {
		t.Fatalf("期望 nil error（幂等零退出），得到：%v", err)
	}
	if rem.callCount != 0 {
		t.Errorf("不应调用 SSHKeysRemove，实际调用 %d 次", rem.callCount)
	}
	if store.markCalled {
		t.Errorf("不应再次调用 MarkRevoked")
	}
	if !strings.Contains(buf.String(), "已吊销") {
		t.Errorf("期望友好的已吊销提示，实际输出：%q", buf.String())
	}
}

// (c) 成功路径：Find 返回 pending/installed 记录 → 以 rec.Name 调用 SSHKeysRemove，
// 随后以 (rec.ID, adminID) 调用 MarkRevoked，输出确认吊销（Req 6.1/6.2）。
func TestRunSSHKeyRevoke_Success(t *testing.T) {
	store := &fakeRevokeStore{rec: sshkeys.KeyRecord{
		ID:     "id-42",
		Name:   "bk-bob-host",
		Status: sshkeys.StatusInstalled,
	}}
	rem := &fakeKeyRemover{}
	var buf bytes.Buffer

	err := runSSHKeyRevoke(context.Background(), &buf, store, rem, "bk-bob-host", "admin-uid")
	if err != nil {
		t.Fatalf("期望 nil error，得到：%v", err)
	}
	if len(rem.removed) != 1 || rem.removed[0] != "bk-bob-host" {
		t.Errorf("期望以 rec.Name 调用一次 SSHKeysRemove，实际：%v", rem.removed)
	}
	if !store.markCalled {
		t.Fatalf("期望调用 MarkRevoked")
	}
	if by, ok := store.marked["id-42"]; !ok || by != "admin-uid" {
		t.Errorf("期望 MarkRevoked(id-42, admin-uid)，实际 marked=%v", store.marked)
	}
	if !strings.Contains(buf.String(), "已吊销") {
		t.Errorf("期望吊销成功确认，实际输出：%q", buf.String())
	}
}

// (d) SSHKeysRemove 失败：返回非 nil error 且不调用 MarkRevoked（不误标 revoked，Req 6.4）。
func TestRunSSHKeyRevoke_RemoveFails(t *testing.T) {
	store := &fakeRevokeStore{rec: sshkeys.KeyRecord{
		ID:     "id-7",
		Name:   "bk-carol-host",
		Status: sshkeys.StatusInstalled,
	}}
	rem := &fakeKeyRemover{removeErr: errors.New("ssh: connection refused")}
	var buf bytes.Buffer

	err := runSSHKeyRevoke(context.Background(), &buf, store, rem, "bk-carol-host", "admin-uid")
	if err == nil {
		t.Fatalf("期望非 nil error（移除失败应非零退出）")
	}
	if store.markCalled {
		t.Errorf("移除失败时不得调用 MarkRevoked（不可误标 revoked）")
	}
}

// (e) Find 返回 ErrPermission：返回非 nil error 且文案提及管理员权限（Req 7.1）。
func TestRunSSHKeyRevoke_Permission(t *testing.T) {
	store := &fakeRevokeStore{findErr: sshkeys.ErrPermission}
	rem := &fakeKeyRemover{}
	var buf bytes.Buffer

	err := runSSHKeyRevoke(context.Background(), &buf, store, rem, "deadbeef", "admin-uid")
	if err == nil {
		t.Fatalf("期望非 nil error（权限不足应非零退出）")
	}
	if !strings.Contains(err.Error(), "管理员权限") {
		t.Errorf("期望错误文案提及『管理员权限』，实际：%v", err)
	}
	if rem.callCount != 0 {
		t.Errorf("权限被拒时不应调用 SSHKeysRemove")
	}
}
