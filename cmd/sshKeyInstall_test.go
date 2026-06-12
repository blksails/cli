package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/sshkeys"
)

// fakePendingStore 是 pendingStore 的可注入测试替身：按预置返回 pending 列表/错误，
// 并记录 MarkInstalled 的调用（id→by），使 runSSHKeyInstall 可在不触达 Supabase 的
// 前提下被验证（design「Unit（install 编排，注入 fake Store + fake dokku）」）。
type fakePendingStore struct {
	pending     []sshkeys.KeyRecord
	listErr     error
	marked      map[string]string // id -> by
	markErrByID map[string]error  // 指定 id 的 MarkInstalled 返回错误
}

func (f *fakePendingStore) ListPending() ([]sshkeys.KeyRecord, error) {
	return f.pending, f.listErr
}

func (f *fakePendingStore) MarkInstalled(id, by string) error {
	if f.markErrByID != nil {
		if err, ok := f.markErrByID[id]; ok && err != nil {
			return err
		}
	}
	if f.marked == nil {
		f.marked = map[string]string{}
	}
	f.marked[id] = by
	return nil
}

// fakeKeyInstaller 是 keyInstaller 的可注入测试替身：记录 Remove/Add 的调用顺序与参数，
// 并可按 name 预置 Add 失败，以覆盖单条失败不阻断、Remove 先于 Add、Remove 错误被忽略等路径。
type fakeKeyInstaller struct {
	calls     []string         // 形如 "remove:<name>" / "add:<name>"
	addErr    map[string]error // 指定 name 的 SSHKeysAdd 返回错误
	removeErr map[string]error // 指定 name 的 SSHKeysRemove 返回错误
}

func (f *fakeKeyInstaller) SSHKeysAdd(ctx context.Context, name, publicKey string) (string, error) {
	f.calls = append(f.calls, "add:"+name)
	if f.addErr != nil {
		if err, ok := f.addErr[name]; ok && err != nil {
			return "", err
		}
	}
	return "ok", nil
}

func (f *fakeKeyInstaller) SSHKeysRemove(ctx context.Context, name string) (string, error) {
	f.calls = append(f.calls, "remove:"+name)
	if f.removeErr != nil {
		if err, ok := f.removeErr[name]; ok && err != nil {
			return "", err
		}
	}
	return "ok", nil
}

// TestRunSSHKeyInstall_PermissionDenied 覆盖 Requirement 7.1/7.3：ListPending 被 RLS 拒绝
// （ErrPermission）时，runSSHKeyInstall 返回非 nil 错误且表述为「需要管理员权限」。
func TestRunSSHKeyInstall_PermissionDenied(t *testing.T) {
	store := &fakePendingStore{listErr: sshkeys.ErrPermission}
	inst := &fakeKeyInstaller{}
	var buf bytes.Buffer

	err := runSSHKeyInstall(context.Background(), &buf, store, inst, "admin-uid", nil, true)
	if err == nil {
		t.Fatalf("期望非 nil 错误（非管理员被拒），实际 nil")
	}
	if !strings.Contains(err.Error(), "管理员权限") {
		t.Fatalf("错误应表述为需要管理员权限，实际：%v", err)
	}
	if len(inst.calls) != 0 {
		t.Fatalf("被拒后不应调用 dokku，实际调用：%v", inst.calls)
	}
}

// TestRunSSHKeyInstall_Empty 覆盖 Requirement 5.5：无待安装记录时输出无待办提示并返回 nil。
func TestRunSSHKeyInstall_Empty(t *testing.T) {
	store := &fakePendingStore{pending: nil}
	inst := &fakeKeyInstaller{}
	var buf bytes.Buffer

	err := runSSHKeyInstall(context.Background(), &buf, store, inst, "admin-uid", nil, true)
	if err != nil {
		t.Fatalf("空列表应零退出（nil error），实际：%v", err)
	}
	if !strings.Contains(buf.String(), "无待安装") {
		t.Fatalf("应输出无待安装提示，实际：%q", buf.String())
	}
	if len(inst.calls) != 0 {
		t.Fatalf("空列表不应调用 dokku，实际调用：%v", inst.calls)
	}
}

// TestRunSSHKeyInstall_PartialFailure 覆盖 Requirement 5.2/5.3/5.4/5.6：两条 pending，
// 一条 Add 成功、一条 Add 失败；成功条回写 MarkInstalled(id, adminID) 并保持安装，失败条
// 不回写（保持 pending）；汇总 成功 1 / 失败 1；且每条在 Add 前都先尝试 Remove。
func TestRunSSHKeyInstall_PartialFailure(t *testing.T) {
	store := &fakePendingStore{
		pending: []sshkeys.KeyRecord{
			{ID: "id-ok", Name: "bk-alice-host", PublicKey: "ssh-ed25519 AAAA alice"},
			{ID: "id-bad", Name: "bk-bob-host", PublicKey: "ssh-ed25519 BBBB bob"},
		},
	}
	inst := &fakeKeyInstaller{
		addErr: map[string]error{"bk-bob-host": errors.New("dokku ssh-keys:add: 名称已存在")},
	}
	var buf bytes.Buffer

	err := runSSHKeyInstall(context.Background(), &buf, store, inst, "admin-uid", nil, true)
	if err != nil {
		t.Fatalf("单条失败不应使整体出错，实际：%v", err)
	}

	// 成功条回写、失败条不回写。
	if got := store.marked["id-ok"]; got != "admin-uid" {
		t.Fatalf("成功条应回写 MarkInstalled(id-ok, admin-uid)，实际 by=%q", got)
	}
	if _, ok := store.marked["id-bad"]; ok {
		t.Fatalf("失败条不应回写 MarkInstalled（应保持 pending），实际已回写")
	}

	// 每条都先 Remove 再 Add。
	want := []string{"remove:bk-alice-host", "add:bk-alice-host", "remove:bk-bob-host", "add:bk-bob-host"}
	if strings.Join(inst.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("调用顺序应为先 Remove 后 Add，实际：%v", inst.calls)
	}

	out := buf.String()
	if !strings.Contains(out, "成功 1") || !strings.Contains(out, "失败 1") {
		t.Fatalf("汇总应为 成功 1 / 失败 1，实际：%q", out)
	}
	// 失败原因应被记录展示。
	if !strings.Contains(out, "名称已存在") {
		t.Fatalf("应展示失败原因，实际：%q", out)
	}
}

// TestRunSSHKeyInstall_RemoveErrorIgnored 覆盖 Requirement 5.2/9.3：Remove 返回错误（如名称
// 不存在）应被忽略——只要随后的 Add 成功，该条即视为安装成功并回写 installed。
func TestRunSSHKeyInstall_RemoveErrorIgnored(t *testing.T) {
	store := &fakePendingStore{
		pending: []sshkeys.KeyRecord{
			{ID: "id-1", Name: "bk-carol-host", PublicKey: "ssh-ed25519 CCCC carol"},
		},
	}
	inst := &fakeKeyInstaller{
		removeErr: map[string]error{"bk-carol-host": errors.New("dokku ssh-keys:remove: 名称不存在")},
	}
	var buf bytes.Buffer

	err := runSSHKeyInstall(context.Background(), &buf, store, inst, "admin-uid", nil, true)
	if err != nil {
		t.Fatalf("Remove 错误应被忽略，实际整体出错：%v", err)
	}
	if got := store.marked["id-1"]; got != "admin-uid" {
		t.Fatalf("Add 成功应回写 installed，实际 by=%q", got)
	}
	if !strings.Contains(buf.String(), "成功 1") {
		t.Fatalf("应汇总 成功 1，实际：%q", buf.String())
	}
}

// twoPending 构造两条 pending（alice/bob）供选择性代装测试复用。
func twoPending() *fakePendingStore {
	return &fakePendingStore{
		pending: []sshkeys.KeyRecord{
			{ID: "id-a", Name: "bk-alice-host", Fingerprint: "SHA256:AAAA", Host: "h1", PublicKey: "ssh-ed25519 AAAA alice"},
			{ID: "id-b", Name: "bk-bob-host", Fingerprint: "SHA256:BBBB", Host: "h1", PublicKey: "ssh-ed25519 BBBB bob"},
		},
	}
}

// TestRunSSHKeyInstall_NoSelectorListsOnly 是安全闸门核心：既不指定 <名称|指纹> 也不带 --all 时，
// 不得代装任何一条，只列出 pending 供审核（避免无差别放行任意 provision 的 pending）。
func TestRunSSHKeyInstall_NoSelectorListsOnly(t *testing.T) {
	store := twoPending()
	inst := &fakeKeyInstaller{}
	var buf bytes.Buffer

	err := runSSHKeyInstall(context.Background(), &buf, store, inst, "admin-uid", nil, false)
	if err != nil {
		t.Fatalf("仅列出不应返回错误，实际：%v", err)
	}
	if len(inst.calls) != 0 {
		t.Fatalf("不指定且无 --all 时不得对 Dokku 做任何 Add/Remove，实际调用：%v", inst.calls)
	}
	if len(store.marked) != 0 {
		t.Fatalf("不指定且无 --all 时不得回写 installed，实际：%v", store.marked)
	}
	out := buf.String()
	if !strings.Contains(out, "bk-alice-host") || !strings.Contains(out, "bk-bob-host") {
		t.Fatalf("应列出全部 pending 供审核，实际：%q", out)
	}
	if !strings.Contains(out, "--all") {
		t.Fatalf("应提示可用 --all 代装全部，实际：%q", out)
	}
	if strings.Contains(out, "已安装") || strings.Contains(out, "代装完成") {
		t.Fatalf("仅列出时不应出现代装汇总，实际：%q", out)
	}
}

// TestRunSSHKeyInstall_SelectorByName 验证按名称精确选择：仅代装匹配的那条，其余 pending 不被触碰。
func TestRunSSHKeyInstall_SelectorByName(t *testing.T) {
	store := twoPending()
	inst := &fakeKeyInstaller{}
	var buf bytes.Buffer

	err := runSSHKeyInstall(context.Background(), &buf, store, inst, "admin-uid", []string{"bk-alice-host"}, false)
	if err != nil {
		t.Fatalf("按名称代装不应出错，实际：%v", err)
	}
	if got := store.marked["id-a"]; got != "admin-uid" {
		t.Fatalf("被选中的 alice 应回写 installed，实际 by=%q", got)
	}
	if _, ok := store.marked["id-b"]; ok {
		t.Fatalf("未被选中的 bob 不应被代装/回写，实际已回写")
	}
	if strings.Join(inst.calls, ",") != "remove:bk-alice-host,add:bk-alice-host" {
		t.Fatalf("应只对 alice 执行 remove+add，实际：%v", inst.calls)
	}
}

// TestRunSSHKeyInstall_SelectorByFingerprint 验证按指纹精确选择，与按名称等价。
func TestRunSSHKeyInstall_SelectorByFingerprint(t *testing.T) {
	store := twoPending()
	inst := &fakeKeyInstaller{}
	var buf bytes.Buffer

	if err := runSSHKeyInstall(context.Background(), &buf, store, inst, "admin-uid", []string{"SHA256:BBBB"}, false); err != nil {
		t.Fatalf("按指纹代装不应出错，实际：%v", err)
	}
	if _, ok := store.marked["id-a"]; ok {
		t.Fatalf("未被选中的 alice 不应被代装")
	}
	if got := store.marked["id-b"]; got != "admin-uid" {
		t.Fatalf("按指纹选中的 bob 应回写 installed，实际 by=%q", got)
	}
}

// TestRunSSHKeyInstall_UnknownSelector 验证：选择器无任何匹配时返回错误且不代装任何一条。
func TestRunSSHKeyInstall_UnknownSelector(t *testing.T) {
	store := twoPending()
	inst := &fakeKeyInstaller{}
	var buf bytes.Buffer

	err := runSSHKeyInstall(context.Background(), &buf, store, inst, "admin-uid", []string{"nope"}, false)
	if err == nil {
		t.Fatalf("无匹配选择器应返回错误")
	}
	if len(inst.calls) != 0 || len(store.marked) != 0 {
		t.Fatalf("无匹配时不得代装任何一条，实际 calls=%v marked=%v", inst.calls, store.marked)
	}
}
