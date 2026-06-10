package cmd

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

// fakeAppRestarter 是 appRestarter 的可注入实现，记录调用参数并返回预置结果，
// 使 runAppRestart 在不触达真实 SSH/Dokku 的前提下被验证。
type fakeAppRestarter struct {
	gotApp string
	called bool
	out    string
	err    error
}

func (f *fakeAppRestarter) PsRestart(_ context.Context, app string) (string, error) {
	f.called = true
	f.gotApp = app
	return f.out, f.err
}

// 成功路径（Requirement 9.1）：以应用名调用 PsRestart，结果原样写入 w，返回 nil。
func TestRunAppRestart_Success(t *testing.T) {
	fake := &fakeAppRestarter{out: "restart ok\n"}
	var buf bytes.Buffer

	if err := runAppRestart(context.Background(), &buf, fake, "myapp"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fake.called {
		t.Fatal("PsRestart was not called")
	}
	if fake.gotApp != "myapp" {
		t.Fatalf("PsRestart called with %q, want %q", fake.gotApp, "myapp")
	}
	if got := buf.String(); got != "restart ok\n" {
		t.Fatalf("output = %q, want %q", got, "restart ok\n")
	}
}

// 远端拒绝路径（Requirement 9.3）：PsRestart 返回错误时透传（errors.Is 可识别原错误），
// 由命令层非零退出。
func TestRunAppRestart_Error(t *testing.T) {
	sentinel := errors.New("dokku: restart rejected")
	fake := &fakeAppRestarter{err: sentinel}
	var buf bytes.Buffer

	err := runAppRestart(context.Background(), &buf, fake, "myapp")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v does not wrap sentinel", err)
	}
}

// 缺名路径（Requirement 9.2）：appRestartCmd 采用 ExactArgs(1)，0 参数被拒绝、1 参数被接受。
func TestAppRestartCmd_Args(t *testing.T) {
	if err := appRestartCmd.Args(appRestartCmd, []string{}); err == nil {
		t.Error("expected error for 0 args, got nil")
	}
	if err := appRestartCmd.Args(appRestartCmd, []string{"myapp"}); err != nil {
		t.Errorf("unexpected error for 1 arg: %v", err)
	}
	if err := appRestartCmd.Args(appRestartCmd, []string{"a", "b"}); err == nil {
		t.Error("expected error for 2 args, got nil")
	}
}

var _ cobra.PositionalArgs = appRestartCmd.Args
