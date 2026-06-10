package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/vault"
)

// fakeVaultRemover 记录 Remove 的入参并返回预置错误，使 runVaultRm 可在不触达真实
// Supabase 的前提下被验证（success / not-found / 其它存储错误三条删除路径）。
type fakeVaultRemover struct {
	err    error
	gotApp string
	gotKey string
	called bool
}

func (f *fakeVaultRemover) Remove(app, key string) error {
	f.called = true
	f.gotApp = app
	f.gotKey = key
	return f.err
}

func TestRunVaultRm_Success_ConfirmsDeletion(t *testing.T) {
	var w bytes.Buffer
	remover := &fakeVaultRemover{}

	err := runVaultRm(&w, "myapp", "DB_PASSWORD", remover)
	if err != nil {
		t.Fatalf("runVaultRm 返回错误：%v", err)
	}
	// Remove 必须以正确的 (app,key) 被调用（仅删目标 key，R4.1/R4.4）。
	if !remover.called {
		t.Fatalf("Remove 未被调用")
	}
	if remover.gotApp != "myapp" || remover.gotKey != "DB_PASSWORD" {
		t.Fatalf("Remove 入参错误：期望 (myapp, DB_PASSWORD)，实际 (%q, %q)", remover.gotApp, remover.gotKey)
	}
	// 删除成功须向用户确认该 key 已删除（R4.2），输出应含 key 名。
	if !strings.Contains(w.String(), "DB_PASSWORD") {
		t.Fatalf("成功输出未确认被删 key 名：%q", w.String())
	}
}

func TestRunVaultRm_NotFound_FriendlyHintZeroExit(t *testing.T) {
	var w bytes.Buffer
	// 模拟 store 在记录不存在时返回包裹 ErrNotFound 的错误。
	remover := &fakeVaultRemover{err: fmt.Errorf("app=%q key=%q: %w", "myapp", "MISSING", vault.ErrNotFound)}

	err := runVaultRm(&w, "myapp", "MISSING", remover)
	// 关键幂等断言（R4.3）：不存在时友好提示并零退出 —— 返回 nil，而非错误。
	if err != nil {
		t.Fatalf("未找到时应友好提示并零退出（返回 nil），实际返回错误：%v", err)
	}
	// 必须给出友好提示（非空输出），告知该 key 当前不存在 / 可能已删除。
	if strings.TrimSpace(w.String()) == "" {
		t.Fatalf("未找到时应输出友好提示，实际输出为空")
	}
}

func TestRunVaultRm_OtherError_NonZero(t *testing.T) {
	var w bytes.Buffer
	remover := &fakeVaultRemover{err: errors.New("store boom")}

	err := runVaultRm(&w, "myapp", "KEY", remover)
	// 非 ErrNotFound 的存储/权限错误应返回非 nil（非零退出）。
	if err == nil {
		t.Fatalf("其它存储错误时应返回错误（非零退出），但返回 nil")
	}
	if errors.Is(err, vault.ErrNotFound) {
		t.Fatalf("普通存储错误不应被识别为 ErrNotFound：%v", err)
	}
}

func TestVaultRmCmd_ExactArgs2(t *testing.T) {
	if vaultRmCmd.Args == nil {
		t.Fatalf("vaultRmCmd.Args 未设置，应为 cobra.ExactArgs(2)")
	}
	// 少于 2 个参数应报错。
	if err := vaultRmCmd.Args(vaultRmCmd, []string{"onlyapp"}); err == nil {
		t.Fatalf("仅 1 个参数应被 ExactArgs(2) 拒绝")
	}
	// 多于 2 个参数应报错。
	if err := vaultRmCmd.Args(vaultRmCmd, []string{"app", "key", "extra"}); err == nil {
		t.Fatalf("3 个参数应被 ExactArgs(2) 拒绝")
	}
	// 恰好 2 个参数应通过。
	if err := vaultRmCmd.Args(vaultRmCmd, []string{"app", "key"}); err != nil {
		t.Fatalf("恰好 2 个参数应通过，实际：%v", err)
	}
	// 必须 self-register 到 vaultCmd。
	found := false
	for _, c := range vaultCmd.Commands() {
		if c == vaultRmCmd {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("vaultRmCmd 未注册到 vaultCmd")
	}
}
