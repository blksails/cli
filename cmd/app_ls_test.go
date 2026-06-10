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

// app_ls_test.go 验证 `bk app ls` 的可测核心 runAppLs 的三类路径
// （Requirement 1.1/1.2/1.3/1.4、12.1/12.2）。通过 fakeAppLister 注入，
// 不触达真实 SSH/Dokku。

// fakeAppLister 实现 appLister，记录调用并返回预置结果，用于无副作用单测。
type fakeAppLister struct {
	apps       []string // AppsList 返回的应用清单
	appsErr    error    // AppsList 返回的错误
	rawOut     string   // Run 返回的原始文本
	runErr     error    // Run 返回的错误
	appsCalled bool     // AppsList 是否被调用
	runCalled  bool     // Run 是否被调用
	runArgs    []string // Run 收到的参数
}

func (f *fakeAppLister) AppsList(_ context.Context) ([]string, error) {
	f.appsCalled = true
	return f.apps, f.appsErr
}

func (f *fakeAppLister) Run(_ context.Context, args ...string) (string, error) {
	f.runCalled = true
	f.runArgs = args
	return f.rawOut, f.runErr
}

// 有应用（≥2）→ 默认表格化呈现，输出含全部应用名且对齐（Requirement 1.1/12.1）。
func TestRunAppLs_TableListsApps(t *testing.T) {
	f := &fakeAppLister{apps: []string{"alpha", "beta"}}
	var buf bytes.Buffer

	if err := runAppLs(context.Background(), &buf, f, false); err != nil {
		t.Fatalf("runAppLs 返回了非预期错误：%v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("表格输出应包含全部应用名，实际：%q", out)
	}
	if !f.appsCalled {
		t.Errorf("非 raw 路径应调用 AppsList")
	}
	if f.runCalled {
		t.Errorf("非 raw 路径不应调用 Run")
	}
}

// 空清单 → 友好提示 + nil error（零退出）（Requirement 1.2/12.1）。
func TestRunAppLs_EmptyListFriendlyHint(t *testing.T) {
	f := &fakeAppLister{apps: nil}
	var buf bytes.Buffer

	if err := runAppLs(context.Background(), &buf, f, false); err != nil {
		t.Fatalf("空清单不是错误，应返回 nil，实际：%v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "暂无应用") {
		t.Errorf("空清单应输出友好提示，实际：%q", out)
	}
}

// AppsList 出错 → runAppLs 透传该错误（cobra 据此非零退出）（Requirement 1.4/12.3）。
func TestRunAppLs_AppsListError(t *testing.T) {
	sentinel := errors.New("dokku apps:list: connection refused")
	f := &fakeAppLister{appsErr: sentinel}
	var buf bytes.Buffer

	err := runAppLs(context.Background(), &buf, f, false)
	if err == nil {
		t.Fatalf("AppsList 出错时 runAppLs 应返回错误")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("应透传底层 dokku 错误（含 stderr），实际：%v", err)
	}
}

// raw=true → 输出等于 Run 原始文本，且不调用 AppsList（Requirement 12.2）。
func TestRunAppLs_RawOutputsVerbatim(t *testing.T) {
	raw := "=====> My Apps\nalpha\nbeta\n"
	f := &fakeAppLister{rawOut: raw}
	var buf bytes.Buffer

	if err := runAppLs(context.Background(), &buf, f, true); err != nil {
		t.Fatalf("raw 路径返回了非预期错误：%v", err)
	}

	if buf.String() != raw {
		t.Errorf("raw 路径应原样输出 dokku 文本，期望 %q，实际 %q", raw, buf.String())
	}
	if !f.runCalled {
		t.Errorf("raw 路径应调用 Run")
	}
	if f.appsCalled {
		t.Errorf("raw 路径不应调用 AppsList")
	}
	if len(f.runArgs) != 1 || f.runArgs[0] != "apps:list" {
		t.Errorf("raw 路径应以 apps:list 调用 Run，实际：%v", f.runArgs)
	}
}

// raw=true 且 Run 出错 → 透传错误（非零退出）（Requirement 1.4/12.3）。
func TestRunAppLs_RawRunError(t *testing.T) {
	sentinel := errors.New("dokku apps:list: permission denied")
	f := &fakeAppLister{runErr: sentinel}
	var buf bytes.Buffer

	err := runAppLs(context.Background(), &buf, f, true)
	if err == nil {
		t.Fatalf("raw 路径 Run 出错时应返回错误")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("raw 路径应透传底层 dokku 错误，实际：%v", err)
	}
}

// appLsCmd 已注册到 appCmd 且名为 ls。
func TestAppLsCmd_Registered(t *testing.T) {
	if appLsCmd.Use != "ls" {
		t.Errorf("appLsCmd.Use 应为 ls，实际：%q", appLsCmd.Use)
	}
	found := false
	for _, c := range appCmd.Commands() {
		if c == appLsCmd {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("appLsCmd 应注册为 appCmd 的子命令")
	}
}
