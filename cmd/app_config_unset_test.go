/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// app_config_unset_test.go 验证 `bk app config:unset <app> KEY [KEY...]` 的可测核心
// runAppConfigUnset 的多类路径（Requirement 6.1/6.2/6.3/6.4）。
// 通过 fakeAppConfigUnsetter 注入，不触达真实 SSH/Dokku。

// fakeAppConfigUnsetter 实现 appConfigUnsetter，记录调用并返回预置结果，用于无副作用单测。
type fakeAppConfigUnsetter struct {
	out          string   // ConfigUnset 返回的结果文本
	err          error    // ConfigUnset 返回的错误
	called       bool     // ConfigUnset 是否被调用
	receivedApp  string   // ConfigUnset 收到的应用名
	receivedKeys []string // ConfigUnset 收到的变参键（存为 slice 以断言透传）
}

func (f *fakeAppConfigUnsetter) ConfigUnset(_ context.Context, app string, keys ...string) (string, error) {
	f.called = true
	f.receivedApp = app
	f.receivedKeys = keys
	return f.out, f.err
}

// (a) 删除成功 → 以应用名与精确的键 slice 调用 ConfigUnset，结果写入 w，err 为 nil
// （Requirement 6.1）。
func TestRunAppConfigUnset_Success(t *testing.T) {
	f := &fakeAppConfigUnsetter{out: "-----> Unsetting config vars\n"}
	var buf bytes.Buffer

	err := runAppConfigUnset(context.Background(), &buf, f, "myapp", []string{"A", "B"})
	if err != nil {
		t.Fatalf("runAppConfigUnset 返回了非预期错误：%v", err)
	}
	if !f.called {
		t.Fatalf("成功路径应调用 ConfigUnset")
	}
	if f.receivedApp != "myapp" {
		t.Errorf("应以应用名 myapp 调用 ConfigUnset，实际：%q", f.receivedApp)
	}
	want := []string{"A", "B"}
	if len(f.receivedKeys) != len(want) {
		t.Fatalf("ConfigUnset 收到的键数应为 %d，实际：%v", len(want), f.receivedKeys)
	}
	for i, k := range want {
		if f.receivedKeys[i] != k {
			t.Errorf("ConfigUnset 第 %d 个键应为 %q，实际：%q", i, k, f.receivedKeys[i])
		}
	}
	if !strings.Contains(buf.String(), "Unsetting config vars") {
		t.Errorf("成功路径应将结果写入 w，实际：%q", buf.String())
	}
}

// (b) ConfigUnset 出错（删除被拒绝）→ runAppConfigUnset 以 %w 透传该错误
// （Requirement 6.4）。
func TestRunAppConfigUnset_RemoteRejectionPassesThrough(t *testing.T) {
	sentinel := errors.New("dokku config:unset: no such key")
	f := &fakeAppConfigUnsetter{err: sentinel}
	var buf bytes.Buffer

	err := runAppConfigUnset(context.Background(), &buf, f, "myapp", []string{"A"})
	if err == nil {
		t.Fatalf("ConfigUnset 出错时 runAppConfigUnset 应返回错误")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("应透传底层 dokku 错误（含 stderr），实际：%v", err)
	}
}

// 缺少应用名或全部键 → appConfigUnsetCmd 采用 MinimumNArgs(2)，<2 参数时 Args 校验失败
// （cobra 据此提示并非零退出）（Requirement 6.2/6.3）。
func TestAppConfigUnsetCmd_RequiresAppAndKey(t *testing.T) {
	if err := appConfigUnsetCmd.Args(appConfigUnsetCmd, []string{}); err == nil {
		t.Errorf("0 参数时 Args 校验应失败")
	}
	if err := appConfigUnsetCmd.Args(appConfigUnsetCmd, []string{"myapp"}); err == nil {
		t.Errorf("仅应用名（无待删除键）时 Args 校验应失败")
	}
	if err := appConfigUnsetCmd.Args(appConfigUnsetCmd, []string{"myapp", "A"}); err != nil {
		t.Errorf("应用名 + 1 个键时 Args 校验应通过，实际：%v", err)
	}
}

// appConfigUnsetCmd 已注册到 appCmd 且 Use 以 config:unset 开头。
func TestAppConfigUnsetCmd_Registered(t *testing.T) {
	if !strings.HasPrefix(appConfigUnsetCmd.Use, "config:unset") {
		t.Errorf("appConfigUnsetCmd.Use 应以 config:unset 开头，实际：%q", appConfigUnsetCmd.Use)
	}
	found := false
	for _, c := range appCmd.Commands() {
		if c == appConfigUnsetCmd {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("appConfigUnsetCmd 应注册为 appCmd 的子命令")
	}
}
