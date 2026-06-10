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

// app_create_test.go 验证 `bk app create <app>` 的可测核心 runAppCreate 的三类路径
// （Requirement 2.1/2.2/2.3）。通过 fakeAppCreator 注入，不触达真实 SSH/Dokku。

// fakeAppCreator 实现 appCreator，记录调用并返回预置结果，用于无副作用单测。
type fakeAppCreator struct {
	out         string // AppsCreate 返回的创建结果文本
	err         error  // AppsCreate 返回的错误
	called      bool   // AppsCreate 是否被调用
	receivedArg string // AppsCreate 收到的应用名
}

func (f *fakeAppCreator) AppsCreate(_ context.Context, name string) (string, error) {
	f.called = true
	f.receivedArg = name
	return f.out, f.err
}

// 创建成功 → 以应用名调用 AppsCreate，结果写入 w，返回 nil（Requirement 2.1）。
func TestRunAppCreate_SuccessWritesResult(t *testing.T) {
	f := &fakeAppCreator{out: "=====> Creating myapp... done\n"}
	var buf bytes.Buffer

	if err := runAppCreate(context.Background(), &buf, f, "myapp"); err != nil {
		t.Fatalf("runAppCreate 返回了非预期错误：%v", err)
	}

	if !f.called {
		t.Errorf("应调用 AppsCreate")
	}
	if f.receivedArg != "myapp" {
		t.Errorf("应以应用名 myapp 调用 AppsCreate，实际：%q", f.receivedArg)
	}
	if !strings.Contains(buf.String(), "myapp") {
		t.Errorf("成功路径应将创建结果写入 w，实际：%q", buf.String())
	}
}

// AppsCreate 出错（应用已存在/被拒绝）→ runAppCreate 透传该错误（cobra 据此非零退出）
// （Requirement 2.3）。
func TestRunAppCreate_RemoteRejectionPassesThrough(t *testing.T) {
	sentinel := errors.New("dokku apps:create: name is already taken")
	f := &fakeAppCreator{err: sentinel}
	var buf bytes.Buffer

	err := runAppCreate(context.Background(), &buf, f, "myapp")
	if err == nil {
		t.Fatalf("AppsCreate 出错时 runAppCreate 应返回错误")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("应透传底层 dokku 错误（含 stderr），实际：%v", err)
	}
}

// 缺少应用名 → appCreateCmd 采用 ExactArgs(1)，0 参数时 Args 校验失败
// （cobra 据此提示并非零退出）（Requirement 2.2）。
func TestAppCreateCmd_RequiresAppNameArg(t *testing.T) {
	if err := appCreateCmd.Args(appCreateCmd, []string{}); err == nil {
		t.Errorf("缺少应用名（0 参数）时 Args 校验应失败")
	}
	if err := appCreateCmd.Args(appCreateCmd, []string{"myapp"}); err != nil {
		t.Errorf("恰好 1 个应用名参数时 Args 校验应通过，实际：%v", err)
	}
	if err := appCreateCmd.Args(appCreateCmd, []string{"a", "b"}); err == nil {
		t.Errorf("多于 1 个参数时 Args 校验应失败")
	}
}

// appCreateCmd 已注册到 appCmd 且 Use 以 create 开头。
func TestAppCreateCmd_Registered(t *testing.T) {
	if !strings.HasPrefix(appCreateCmd.Use, "create") {
		t.Errorf("appCreateCmd.Use 应以 create 开头，实际：%q", appCreateCmd.Use)
	}
	found := false
	for _, c := range appCmd.Commands() {
		if c == appCreateCmd {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("appCreateCmd 应注册为 appCmd 的子命令")
	}
}
