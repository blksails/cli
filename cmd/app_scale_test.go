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

// app_scale_test.go 验证 `bk app scale <app> <process=count>` 的可测核心
// runAppScale 的多类路径（Requirement 8.1/8.2/8.4）。
// 通过 fakeAppScaler 注入，不触达真实 SSH/Dokku。

// fakeAppScaler 实现 appScaler，记录调用并返回预置结果，用于无副作用单测。
type fakeAppScaler struct {
	out             string // PsScale 返回的结果文本
	err             error  // PsScale 返回的错误
	called          bool   // PsScale 是否被调用
	receivedApp     string // PsScale 收到的应用名
	receivedProcess string // PsScale 收到的进程名
	receivedCount   int    // PsScale 收到的副本数
}

func (f *fakeAppScaler) PsScale(_ context.Context, app, process string, count int) (string, error) {
	f.called = true
	f.receivedApp = app
	f.receivedProcess = process
	f.receivedCount = count
	return f.out, f.err
}

// (a) 合法扩缩容 "web=3" → 解析后以 (app,"web",3) 调用 PsScale，结果写入 w
// （Requirement 8.1）。
func TestRunAppScale_ValidScaleSuccess(t *testing.T) {
	f := &fakeAppScaler{out: "-----> Scaling web to 3\n"}
	var buf bytes.Buffer

	err := runAppScale(context.Background(), &buf, f, "myapp", "web=3")
	if err != nil {
		t.Fatalf("runAppScale 返回了非预期错误：%v", err)
	}
	if !f.called {
		t.Fatalf("成功路径应调用 PsScale")
	}
	if f.receivedApp != "myapp" {
		t.Errorf("应以应用名 myapp 调用 PsScale，实际：%q", f.receivedApp)
	}
	if f.receivedProcess != "web" {
		t.Errorf("应以进程名 web 调用 PsScale，实际：%q", f.receivedProcess)
	}
	if f.receivedCount != 3 {
		t.Errorf("应以副本数 3 调用 PsScale，实际：%d", f.receivedCount)
	}
	if !strings.Contains(buf.String(), "Scaling web to 3") {
		t.Errorf("成功路径应将结果写入 w，实际：%q", buf.String())
	}
}

// (b) 非整数副本数 "web=abc" → appParseProcessCount 报错，PsScale 不被调用
// （Requirement 8.2）。
func TestRunAppScale_NonIntegerCountDoesNotCallPsScale(t *testing.T) {
	f := &fakeAppScaler{}
	var buf bytes.Buffer

	err := runAppScale(context.Background(), &buf, f, "myapp", "web=abc")
	if err == nil {
		t.Fatalf("副本数非整数时 runAppScale 应返回错误")
	}
	if f.called {
		t.Errorf("解析失败时不应调用 PsScale")
	}
}

// (b') 缺少 '=' "web" → appParseProcessCount 报错，PsScale 不被调用（Requirement 8.2）。
func TestRunAppScale_MissingEqualsDoesNotCallPsScale(t *testing.T) {
	f := &fakeAppScaler{}
	var buf bytes.Buffer

	err := runAppScale(context.Background(), &buf, f, "myapp", "web")
	if err == nil {
		t.Fatalf("缺少 '=' 时 runAppScale 应返回错误")
	}
	if f.called {
		t.Errorf("解析失败时不应调用 PsScale")
	}
}

// (b'') 负数副本数 "web=-1" → appParseProcessCount 报错，PsScale 不被调用
// （Requirement 8.2）。
func TestRunAppScale_NegativeCountDoesNotCallPsScale(t *testing.T) {
	f := &fakeAppScaler{}
	var buf bytes.Buffer

	err := runAppScale(context.Background(), &buf, f, "myapp", "web=-1")
	if err == nil {
		t.Fatalf("副本数为负数时 runAppScale 应返回错误")
	}
	if f.called {
		t.Errorf("解析失败时不应调用 PsScale")
	}
}

// (c) PsScale 出错（扩缩容被拒绝）→ runAppScale 以 %w 透传该错误（Requirement 8.4）。
func TestRunAppScale_RemoteRejectionPassesThrough(t *testing.T) {
	sentinel := errors.New("dokku ps:scale: scheduler rejected")
	f := &fakeAppScaler{err: sentinel}
	var buf bytes.Buffer

	err := runAppScale(context.Background(), &buf, f, "myapp", "web=3")
	if err == nil {
		t.Fatalf("PsScale 出错时 runAppScale 应返回错误")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("应透传底层 dokku 错误（含 stderr），实际：%v", err)
	}
	if !f.called {
		t.Errorf("解析成功后应调用 PsScale")
	}
}

// 缺少应用名或扩缩容参数 → appScaleCmd 采用 ExactArgs(2)，参数数 !=2 时 Args 校验失败
// （cobra 据此提示并非零退出）（Requirement 8.3）。
func TestAppScaleCmd_RequiresAppAndSpec(t *testing.T) {
	if err := appScaleCmd.Args(appScaleCmd, []string{}); err == nil {
		t.Errorf("0 参数时 Args 校验应失败")
	}
	if err := appScaleCmd.Args(appScaleCmd, []string{"myapp"}); err == nil {
		t.Errorf("仅应用名（无 process=count）时 Args 校验应失败")
	}
	if err := appScaleCmd.Args(appScaleCmd, []string{"myapp", "web=3"}); err != nil {
		t.Errorf("应用名 + process=count 时 Args 校验应通过，实际：%v", err)
	}
	if err := appScaleCmd.Args(appScaleCmd, []string{"myapp", "web=3", "extra"}); err == nil {
		t.Errorf("多余参数时 Args 校验应失败")
	}
}

// appScaleCmd 已注册到 appCmd 且 Use 以 scale 开头。
func TestAppScaleCmd_Registered(t *testing.T) {
	if !strings.HasPrefix(appScaleCmd.Use, "scale") {
		t.Errorf("appScaleCmd.Use 应以 scale 开头，实际：%q", appScaleCmd.Use)
	}
	found := false
	for _, c := range appCmd.Commands() {
		if c == appScaleCmd {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("appScaleCmd 应注册为 appCmd 的子命令")
	}
}
