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

// app_config_set_test.go 验证 `bk app config:set <app> KEY=VALUE...` 的可测核心
// runAppConfigSet 的多类路径（Requirement 5.1/5.3/5.4/5.5/5.6）。
// 通过 fakeAppConfigSetter 注入，不触达真实 SSH/Dokku。

// fakeAppConfigSetter 实现 appConfigSetter，记录调用并返回预置结果，用于无副作用单测。
type fakeAppConfigSetter struct {
	out          string            // ConfigSet 返回的结果文本
	err          error             // ConfigSet 返回的错误
	called       bool              // ConfigSet 是否被调用
	receivedApp  string            // ConfigSet 收到的应用名
	receivedKV   map[string]string // ConfigSet 收到的键值映射
	receivedNoRS bool              // ConfigSet 收到的 noRestart
}

func (f *fakeAppConfigSetter) ConfigSet(_ context.Context, app string, kv map[string]string, noRestart bool) (string, error) {
	f.called = true
	f.receivedApp = app
	f.receivedKV = kv
	f.receivedNoRS = noRestart
	return f.out, f.err
}

// (a) 多对设置成功 → 解析后以正确的 map 与 noRestart 调用 ConfigSet，结果写入 w
// （Requirement 5.1）。
func TestRunAppConfigSet_MultiPairSuccess(t *testing.T) {
	f := &fakeAppConfigSetter{out: "-----> Setting config vars\n"}
	var buf bytes.Buffer

	err := runAppConfigSet(context.Background(), &buf, f, "myapp", []string{"A=1", "B=2"}, false)
	if err != nil {
		t.Fatalf("runAppConfigSet 返回了非预期错误：%v", err)
	}
	if !f.called {
		t.Fatalf("成功路径应调用 ConfigSet")
	}
	if f.receivedApp != "myapp" {
		t.Errorf("应以应用名 myapp 调用 ConfigSet，实际：%q", f.receivedApp)
	}
	want := map[string]string{"A": "1", "B": "2"}
	if len(f.receivedKV) != len(want) {
		t.Fatalf("ConfigSet 收到的映射规模应为 %d，实际：%v", len(want), f.receivedKV)
	}
	for k, v := range want {
		if f.receivedKV[k] != v {
			t.Errorf("ConfigSet 应收到 %s=%s，实际：%q", k, v, f.receivedKV[k])
		}
	}
	if f.receivedNoRS {
		t.Errorf("未传 --no-restart 时 noRestart 应为 false")
	}
	if !strings.Contains(buf.String(), "Setting config vars") {
		t.Errorf("成功路径应将结果写入 w，实际：%q", buf.String())
	}
}

// (b) --no-restart=true → ConfigSet 收到 noRestart==true（Requirement 5.4）。
func TestRunAppConfigSet_NoRestartPassedThrough(t *testing.T) {
	f := &fakeAppConfigSetter{out: "ok\n"}
	var buf bytes.Buffer

	if err := runAppConfigSet(context.Background(), &buf, f, "myapp", []string{"A=1"}, true); err != nil {
		t.Fatalf("runAppConfigSet 返回了非预期错误：%v", err)
	}
	if !f.called {
		t.Fatalf("应调用 ConfigSet")
	}
	if !f.receivedNoRS {
		t.Errorf("传入 --no-restart 时 ConfigSet 应收到 noRestart==true")
	}
}

// (c) 非法参数（不含 '='）→ appParseKeyValues 报错，ConfigSet 不被调用
// （Requirement 5.3）。
func TestRunAppConfigSet_InvalidPairDoesNotCallConfigSet(t *testing.T) {
	f := &fakeAppConfigSetter{}
	var buf bytes.Buffer

	err := runAppConfigSet(context.Background(), &buf, f, "myapp", []string{"A=1", "BAD"}, false)
	if err == nil {
		t.Fatalf("参数格式非法时 runAppConfigSet 应返回错误")
	}
	if f.called {
		t.Errorf("解析失败时不应调用 ConfigSet")
	}
}

// (c') 空 KV 列表 → appParseKeyValues 报错，ConfigSet 不被调用（Requirement 5.2）。
func TestRunAppConfigSet_EmptyPairsDoesNotCallConfigSet(t *testing.T) {
	f := &fakeAppConfigSetter{}
	var buf bytes.Buffer

	err := runAppConfigSet(context.Background(), &buf, f, "myapp", nil, false)
	if err == nil {
		t.Fatalf("缺少 KEY=VALUE 时 runAppConfigSet 应返回错误")
	}
	if f.called {
		t.Errorf("缺少配置项时不应调用 ConfigSet")
	}
}

// (d) ConfigSet 出错（设置被拒绝）→ runAppConfigSet 以 %w 透传该错误
// （Requirement 5.6）。
func TestRunAppConfigSet_RemoteRejectionPassesThrough(t *testing.T) {
	sentinel := errors.New("dokku config:set: deploy failed")
	f := &fakeAppConfigSetter{err: sentinel}
	var buf bytes.Buffer

	err := runAppConfigSet(context.Background(), &buf, f, "myapp", []string{"A=1"}, false)
	if err == nil {
		t.Fatalf("ConfigSet 出错时 runAppConfigSet 应返回错误")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("应透传底层 dokku 错误（含 stderr），实际：%v", err)
	}
}

// 缺少应用名或全部 KV → appConfigSetCmd 采用 MinimumNArgs(2)，<2 参数时 Args 校验失败
// （cobra 据此提示并非零退出）（Requirement 5.2/5.5）。
func TestAppConfigSetCmd_RequiresAppAndPair(t *testing.T) {
	if err := appConfigSetCmd.Args(appConfigSetCmd, []string{}); err == nil {
		t.Errorf("0 参数时 Args 校验应失败")
	}
	if err := appConfigSetCmd.Args(appConfigSetCmd, []string{"myapp"}); err == nil {
		t.Errorf("仅应用名（无 KEY=VALUE）时 Args 校验应失败")
	}
	if err := appConfigSetCmd.Args(appConfigSetCmd, []string{"myapp", "A=1"}); err != nil {
		t.Errorf("应用名 + 1 个 KEY=VALUE 时 Args 校验应通过，实际：%v", err)
	}
}

// appConfigSetCmd 已注册到 appCmd 且 Use 以 config:set 开头，并提供 --no-restart 标志。
func TestAppConfigSetCmd_Registered(t *testing.T) {
	if !strings.HasPrefix(appConfigSetCmd.Use, "config:set") {
		t.Errorf("appConfigSetCmd.Use 应以 config:set 开头，实际：%q", appConfigSetCmd.Use)
	}
	if appConfigSetCmd.Flags().Lookup("no-restart") == nil {
		t.Errorf("appConfigSetCmd 应提供 --no-restart 标志")
	}
	found := false
	for _, c := range appCmd.Commands() {
		if c == appConfigSetCmd {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("appConfigSetCmd 应注册为 appCmd 的子命令")
	}
}
