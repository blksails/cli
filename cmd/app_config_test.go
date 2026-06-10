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

// app_config_test.go 验证 `bk app config <app>` 的可测核心 runAppConfig 的三类路径
// （Requirement 4.1/4.2/4.4、12.1/12.2）。通过 fakeAppConfigReader 注入，
// 不触达真实 SSH/Dokku。

// fakeAppConfigReader 实现 appConfigReader，记录调用并返回预置结果，用于无副作用单测。
type fakeAppConfigReader struct {
	env          map[string]string // ConfigGet 返回的环境变量
	envErr       error             // ConfigGet 返回的错误
	rawOut       string            // Run 返回的原始文本
	runErr       error             // Run 返回的错误
	configCalled bool              // ConfigGet 是否被调用
	runCalled    bool              // Run 是否被调用
	configApp    string            // ConfigGet 收到的应用名
	runArgs      []string          // Run 收到的参数
}

func (f *fakeAppConfigReader) ConfigGet(_ context.Context, app string) (map[string]string, error) {
	f.configCalled = true
	f.configApp = app
	return f.env, f.envErr
}

func (f *fakeAppConfigReader) Run(_ context.Context, args ...string) (string, error) {
	f.runCalled = true
	f.runArgs = args
	return f.rawOut, f.runErr
}

// 有变量（≥2）→ 默认表格化呈现，输出含全部键与值（Requirement 4.1/12.1）。
func TestRunAppConfig_TableShowsVars(t *testing.T) {
	f := &fakeAppConfigReader{env: map[string]string{"FOO": "bar", "BAZ": "qux"}}
	var buf bytes.Buffer

	if err := runAppConfig(context.Background(), &buf, f, "myapp", false); err != nil {
		t.Fatalf("runAppConfig 返回了非预期错误：%v", err)
	}

	out := buf.String()
	for _, want := range []string{"FOO", "bar", "BAZ", "qux"} {
		if !strings.Contains(out, want) {
			t.Errorf("表格输出应包含 %q，实际：%q", want, out)
		}
	}
	if !f.configCalled {
		t.Errorf("非 raw 路径应调用 ConfigGet")
	}
	if f.configApp != "myapp" {
		t.Errorf("ConfigGet 应收到应用名 myapp，实际：%q", f.configApp)
	}
	if f.runCalled {
		t.Errorf("非 raw 路径不应调用 Run")
	}
}

// 空配置 → 友好提示 + nil error（零退出）（Requirement 4.2/12.1）。
func TestRunAppConfig_EmptyConfigFriendlyHint(t *testing.T) {
	f := &fakeAppConfigReader{env: map[string]string{}}
	var buf bytes.Buffer

	if err := runAppConfig(context.Background(), &buf, f, "myapp", false); err != nil {
		t.Fatalf("空配置不是错误，应返回 nil，实际：%v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "暂无环境变量") {
		t.Errorf("空配置应输出友好提示，实际：%q", out)
	}
}

// ConfigGet 出错 → runAppConfig 透传该错误（cobra 据此非零退出）（Requirement 4.4/12.3）。
func TestRunAppConfig_ConfigGetError(t *testing.T) {
	sentinel := errors.New("dokku config:show: app does not exist")
	f := &fakeAppConfigReader{envErr: sentinel}
	var buf bytes.Buffer

	err := runAppConfig(context.Background(), &buf, f, "ghost", false)
	if err == nil {
		t.Fatalf("ConfigGet 出错时 runAppConfig 应返回错误")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("应透传底层 dokku 错误（含 stderr），实际：%v", err)
	}
}

// raw=true → 输出等于 Run 原始文本，且不调用 ConfigGet，Run 以 config:show <app> 调用（Requirement 12.2）。
func TestRunAppConfig_RawOutputsVerbatim(t *testing.T) {
	raw := "=====> myapp env vars\nFOO:  bar\nBAZ:  qux\n"
	f := &fakeAppConfigReader{rawOut: raw}
	var buf bytes.Buffer

	if err := runAppConfig(context.Background(), &buf, f, "myapp", true); err != nil {
		t.Fatalf("raw 路径返回了非预期错误：%v", err)
	}

	if buf.String() != raw {
		t.Errorf("raw 路径应原样输出 dokku 文本，期望 %q，实际 %q", raw, buf.String())
	}
	if !f.runCalled {
		t.Errorf("raw 路径应调用 Run")
	}
	if f.configCalled {
		t.Errorf("raw 路径不应调用 ConfigGet")
	}
	if len(f.runArgs) != 2 || f.runArgs[0] != "config:show" || f.runArgs[1] != "myapp" {
		t.Errorf("raw 路径应以 config:show <app> 调用 Run，实际：%v", f.runArgs)
	}
}

// raw=true 且 Run 出错 → 透传错误（非零退出）（Requirement 4.4/12.3）。
func TestRunAppConfig_RawRunError(t *testing.T) {
	sentinel := errors.New("dokku config:show: permission denied")
	f := &fakeAppConfigReader{runErr: sentinel}
	var buf bytes.Buffer

	err := runAppConfig(context.Background(), &buf, f, "myapp", true)
	if err == nil {
		t.Fatalf("raw 路径 Run 出错时应返回错误")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("raw 路径应透传底层 dokku 错误，实际：%v", err)
	}
}

// appConfigCmd 已注册到 appCmd 且使用 config，要求恰好 1 个位置参数（Requirement 4.3）。
func TestAppConfigCmd_Registered(t *testing.T) {
	if appConfigCmd.Use != "config <app>" {
		t.Errorf("appConfigCmd.Use 应为 config <app>，实际：%q", appConfigCmd.Use)
	}
	if appConfigCmd.Args == nil {
		t.Errorf("appConfigCmd 应设置 Args 校验（恰好 1 个应用名参数）")
	} else {
		if err := appConfigCmd.Args(appConfigCmd, []string{}); err == nil {
			t.Errorf("缺少应用名参数时 Args 校验应失败（Requirement 4.3）")
		}
		if err := appConfigCmd.Args(appConfigCmd, []string{"a", "b"}); err == nil {
			t.Errorf("多余参数时 Args 校验应失败")
		}
		if err := appConfigCmd.Args(appConfigCmd, []string{"a"}); err != nil {
			t.Errorf("恰好 1 个参数时 Args 校验应通过，实际：%v", err)
		}
	}
	found := false
	for _, c := range appCmd.Commands() {
		if c == appConfigCmd {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("appConfigCmd 应注册为 appCmd 的子命令")
	}
}
