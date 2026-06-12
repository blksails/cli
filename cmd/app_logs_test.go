package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/dokku"
)

// fakeLogReader 以可注入的桩满足 appLogReader，记录调用并返回预置结果，
// 使 runAppLogs 无需真实 SSH/Dokku 即可被验证。
type fakeLogReader struct {
	called  bool
	gotApp  string
	gotOpts dokku.LogsOptions
	out     string
	err     error
}

func (f *fakeLogReader) Logs(_ context.Context, w io.Writer, app string, opts dokku.LogsOptions) error {
	f.called = true
	f.gotApp = app
	f.gotOpts = opts
	if f.err != nil {
		return f.err
	}
	_, err := io.WriteString(w, f.out)
	return err
}

// 默认快照（numSet=false）：以默认 num（0，含义为“无 -n 限制”）调用 Logs，
// 并把返回的日志原文逐字写入 w（Requirement 10.1）。
func TestRunAppLogs_DefaultSnapshot(t *testing.T) {
	f := &fakeLogReader{out: "line-a\nline-b\n"}
	var buf bytes.Buffer
	if err := runAppLogs(context.Background(), &buf, f, "myapp", dokku.LogsOptions{}, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.called {
		t.Fatal("expected Logs to be called")
	}
	if f.gotApp != "myapp" {
		t.Fatalf("app = %q, want myapp", f.gotApp)
	}
	if f.gotOpts.Num != 0 {
		t.Fatalf("num = %d, want 0 (default snapshot)", f.gotOpts.Num)
	}
	if got := buf.String(); got != "line-a\nline-b\n" {
		t.Fatalf("output = %q, want verbatim snapshot", got)
	}
}

// -n 5（numSet=true, num=5）：以 num=5 调用 Logs，限制最近 5 行（Requirement 10.2）。
func TestRunAppLogs_LimitLines(t *testing.T) {
	f := &fakeLogReader{out: "snap"}
	var buf bytes.Buffer
	if err := runAppLogs(context.Background(), &buf, f, "myapp", dokku.LogsOptions{Num: 5}, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.called {
		t.Fatal("expected Logs to be called")
	}
	if f.gotOpts.Num != 5 {
		t.Fatalf("num = %d, want 5", f.gotOpts.Num)
	}
	if buf.String() != "snap" {
		t.Fatalf("output = %q, want verbatim snapshot", buf.String())
	}
}

// 完整选项透传：-p/-q/-t 与 -n 一并落入 dokku.LogsOptions 并原样传给 Logs。
func TestRunAppLogs_AllOptionsForwarded(t *testing.T) {
	f := &fakeLogReader{out: "streamed"}
	var buf bytes.Buffer
	opts := dokku.LogsOptions{Num: 20, Process: "worker", Quiet: true, Tail: true}
	if err := runAppLogs(context.Background(), &buf, f, "myapp", opts, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.gotOpts != opts {
		t.Fatalf("opts = %+v, want %+v", f.gotOpts, opts)
	}
	if buf.String() != "streamed" {
		t.Fatalf("output = %q, want verbatim", buf.String())
	}
}

// -n 非正整数（numSet=true, num<=0）：提示行数错误并不调用 Logs（Requirement 10.3）。
func TestRunAppLogs_NonPositiveNum(t *testing.T) {
	for _, num := range []int{0, -3} {
		f := &fakeLogReader{out: "should-not-show"}
		var buf bytes.Buffer
		err := runAppLogs(context.Background(), &buf, f, "myapp", dokku.LogsOptions{Num: num}, true)
		if err == nil {
			t.Fatalf("num=%d: expected error for non-positive line count", num)
		}
		if f.called {
			t.Fatalf("num=%d: Logs must not be called on invalid line count", num)
		}
		if buf.Len() != 0 {
			t.Fatalf("num=%d: expected no output, got %q", num, buf.String())
		}
		if !strings.Contains(err.Error(), "行数") {
			t.Fatalf("num=%d: error %q should mention 行数", num, err.Error())
		}
	}
}

// 读取被拒绝：透传 dokku 错误（%w 包装），由命令层非零退出（Requirement 10.5）。
func TestRunAppLogs_LogsError(t *testing.T) {
	sentinel := errors.New("dokku: app not found")
	f := &fakeLogReader{err: sentinel}
	var buf bytes.Buffer
	err := runAppLogs(context.Background(), &buf, f, "ghost", dokku.LogsOptions{}, false)
	if err == nil {
		t.Fatal("expected error to be propagated")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v should wrap sentinel", err)
	}
}

// cobra 接线：appLogsCmd 暴露 -n/--num 标志且要求恰好一个应用名参数。
func TestAppLogsCmd_Wiring(t *testing.T) {
	if appLogsCmd.Args == nil {
		t.Fatal("appLogsCmd.Args should be set (ExactArgs(1))")
	}
	if err := appLogsCmd.Args(appLogsCmd, []string{}); err == nil {
		t.Fatal("expected error with 0 args (Requirement 10.4)")
	}
	if err := appLogsCmd.Args(appLogsCmd, []string{"a", "b"}); err == nil {
		t.Fatal("expected error with 2 args")
	}
	if err := appLogsCmd.Args(appLogsCmd, []string{"myapp"}); err != nil {
		t.Fatalf("unexpected error with 1 arg: %v", err)
	}
	// 与 dokku logs 对齐的全部标志及其短选项均需接线。
	for _, tc := range []struct{ long, short string }{
		{"num", "n"},
		{"ps", "p"},
		{"quiet", "q"},
		{"tail", "t"},
	} {
		if appLogsCmd.Flags().Lookup(tc.long) == nil {
			t.Fatalf("appLogsCmd should have --%s flag", tc.long)
		}
		if appLogsCmd.Flags().ShorthandLookup(tc.short) == nil {
			t.Fatalf("appLogsCmd should have -%s shorthand", tc.short)
		}
	}
}
