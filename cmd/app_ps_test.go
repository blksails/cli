package cmd

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

// fakeAppPsReader 注入 runAppPs 的 Ps 缝，记录入参并返回预设结果，
// 使可测核心在不触达真实 SSH/Dokku 的前提下被验证。
type fakeAppPsReader struct {
	out      string
	err      error
	gotApp   string
	gotCalls int
}

func (f *fakeAppPsReader) Ps(_ context.Context, app string) (string, error) {
	f.gotCalls++
	f.gotApp = app
	return f.out, f.err
}

// 成功路径（Requirement 7.1）：以应用名调用 Ps，将进程状态原文逐字写入 w，返回 nil。
func TestRunAppPs_Success_WritesStatusVerbatim(t *testing.T) {
	const status = "=====> myapp process information\n    web:  running (CID abc123)\n"
	f := &fakeAppPsReader{out: status}
	var buf bytes.Buffer

	err := runAppPs(context.Background(), &buf, f, "myapp")
	if err != nil {
		t.Fatalf("runAppPs 返回非预期错误：%v", err)
	}
	if f.gotCalls != 1 {
		t.Fatalf("Ps 调用次数 = %d，期望 1", f.gotCalls)
	}
	if f.gotApp != "myapp" {
		t.Fatalf("Ps 收到的 app = %q，期望 \"myapp\"", f.gotApp)
	}
	if got := buf.String(); got != status {
		t.Fatalf("写入内容 = %q，期望原文逐字 %q", got, status)
	}
}

// 远端拒绝/应用不存在（Requirement 7.3）：Ps 返回的错误以 %w 透传，errors.Is 可命中。
func TestRunAppPs_Error_WrappedPassthrough(t *testing.T) {
	sentinel := errors.New("App myapp does not exist")
	f := &fakeAppPsReader{err: sentinel}
	var buf bytes.Buffer

	err := runAppPs(context.Background(), &buf, f, "myapp")
	if err == nil {
		t.Fatal("runAppPs 应返回错误，但得到 nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("errors.Is 未命中底层 dokku 错误：%v", err)
	}
}

// 缺名（Requirement 7.2）：appPsCmd 采用 ExactArgs(1)，0 参数被拒、1 参数通过。
func TestAppPsCmd_ArgsExactlyOne(t *testing.T) {
	if appPsCmd.Args == nil {
		t.Fatal("appPsCmd.Args 未设置，期望 cobra.ExactArgs(1)")
	}
	if err := appPsCmd.Args(appPsCmd, []string{}); err == nil {
		t.Error("0 参数应被拒绝，但 Args 校验通过")
	}
	if err := appPsCmd.Args(appPsCmd, []string{"myapp"}); err != nil {
		t.Errorf("1 参数应通过，但 Args 校验失败：%v", err)
	}
	if err := appPsCmd.Args(appPsCmd, []string{"a", "b"}); err == nil {
		t.Error("2 参数应被拒绝，但 Args 校验通过")
	}
}

// 守卫：确认 appPsCmd 自注册到 appCmd，子命令 use 为 "ps"。
func TestAppPsCmd_RegisteredOnAppCmd(t *testing.T) {
	var found *cobra.Command
	for _, sub := range appCmd.Commands() {
		if sub.Name() == "ps" {
			found = sub
			break
		}
	}
	if found == nil {
		t.Fatal("appCmd 下未找到 ps 子命令")
	}
	if found != appPsCmd {
		t.Fatal("appCmd 注册的 ps 子命令不是 appPsCmd")
	}
}
