package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/mirror"
)

// proxyMirror_test.go 覆盖 task 2.1 的可测核心 runMirror 与 mirrorCmd 的标志装配
// （Requirements 3.1/3.2/3.3/3.4/3.5/3.6/6.4/6.5/7.1/7.4，design：mirrorCmd 组件）。
//
// 这些测试通过注入 fake run 间谍，避免触达真实 Hub（mirror.Run 会阻塞），
// 只验证命令层的标志解析、校验、默认值、确认输出与错误透出语义。

// spyRun 记录是否被调用及收到的 Config，并返回预置错误。
type spyRun struct {
	called bool
	cfg    mirror.Config
	ret    error
}

func (s *spyRun) run(_ context.Context, cfg mirror.Config) error {
	s.called = true
	s.cfg = cfg
	return s.ret
}

func newHub() hubConfig {
	return hubConfig{Server: "hub.example:8443", Token: "secret-tok", App: "demo-app"}
}

// 3.3：缺 --target → 报错，run 不被调用。
func TestRunMirror_MissingTarget(t *testing.T) {
	spy := &spyRun{}
	var buf bytes.Buffer
	err := runMirror(context.Background(), &buf, newHub(), mirrorFlags{}, spy.run)
	if err == nil {
		t.Fatal("缺 --target 应返回错误，得到 nil")
	}
	if spy.called {
		t.Fatal("校验失败时不应调用 run")
	}
}

// 3.3：target 缺 scheme（localhost:8080）或缺 host（http://）→ 报错，run 不被调用。
func TestRunMirror_InvalidTarget(t *testing.T) {
	cases := []string{"localhost:8080", "http://"}
	for _, tgt := range cases {
		spy := &spyRun{}
		var buf bytes.Buffer
		err := runMirror(context.Background(), &buf, newHub(), mirrorFlags{target: tgt}, spy.run)
		if err == nil {
			t.Fatalf("非法 target %q 应返回错误，得到 nil", tgt)
		}
		if spy.called {
			t.Fatalf("非法 target %q 时不应调用 run", tgt)
		}
	}
}

// 3.2：非法 --header（无冒号）→ 报错，run 不被调用。
func TestRunMirror_MalformedHeader(t *testing.T) {
	spy := &spyRun{}
	var buf bytes.Buffer
	f := mirrorFlags{target: "http://127.0.0.1:8080", headers: []string{"NoColonHere"}}
	err := runMirror(context.Background(), &buf, newHub(), f, spy.run)
	if err == nil {
		t.Fatal("非法 header 应返回错误，得到 nil")
	}
	if spy.called {
		t.Fatal("header 解析失败时不应调用 run")
	}
}

// 3.1/3.2/7.1：合法 target + 两条 --header → run 被调用，Config 装配正确（含默认值），
// 确认输出含「镜像」「单向」。
func TestRunMirror_ValidAssemblesConfigAndConfirms(t *testing.T) {
	spy := &spyRun{}
	var buf bytes.Buffer
	hub := newHub()
	f := mirrorFlags{
		target:  "http://127.0.0.1:8080",
		headers: []string{"A:1", "B:2"},
	}
	if err := runMirror(context.Background(), &buf, hub, f, spy.run); err != nil {
		t.Fatalf("合法输入不应返回错误：%v", err)
	}
	if !spy.called {
		t.Fatal("合法输入应调用 run")
	}
	c := spy.cfg
	if c.TargetURL != "http://127.0.0.1:8080" {
		t.Errorf("TargetURL=%q，期望 http://127.0.0.1:8080", c.TargetURL)
	}
	if c.ServerAddress != hub.Server || c.Token != hub.Token || c.AppID != hub.App {
		t.Errorf("hub 字段未正确映射：%+v", c)
	}
	if c.Method != "*" {
		t.Errorf("Method 默认应为 *，得到 %q", c.Method)
	}
	if c.PathPrefix != "/" {
		t.Errorf("PathPrefix 默认应为 /，得到 %q", c.PathPrefix)
	}
	if c.RuleID != "bk-mirror" {
		t.Errorf("RuleID 默认应为 bk-mirror，得到 %q", c.RuleID)
	}
	if len(c.Headers) != 2 || c.Headers["A"] != "1" || c.Headers["B"] != "2" {
		t.Errorf("Headers 解析错误：%+v", c.Headers)
	}
	out := buf.String()
	if !strings.Contains(out, "镜像") {
		t.Errorf("确认输出应含「镜像」，得到：%q", out)
	}
	if !strings.Contains(out, "单向") {
		t.Errorf("确认输出应含「单向」，得到：%q", out)
	}
	if !strings.Contains(out, hub.Server) || !strings.Contains(out, hub.App) || !strings.Contains(out, f.target) {
		t.Errorf("确认输出应含 Hub 地址/app/target，得到：%q", out)
	}
	if strings.Contains(out, hub.Token) {
		t.Errorf("确认输出不得含 token 明文，得到：%q", out)
	}
}

// 3.2：自定义标志值应覆盖默认值。
func TestRunMirror_CustomFlagsOverrideDefaults(t *testing.T) {
	spy := &spyRun{}
	var buf bytes.Buffer
	f := mirrorFlags{
		target: "https://api.local:9000",
		method: "POST",
		path:   "/v1",
		host:   "svc.example",
		ruleID: "my-rule",
	}
	if err := runMirror(context.Background(), &buf, newHub(), f, spy.run); err != nil {
		t.Fatalf("不应返回错误：%v", err)
	}
	c := spy.cfg
	if c.Method != "POST" || c.PathPrefix != "/v1" || c.Host != "svc.example" || c.RuleID != "my-rule" {
		t.Errorf("自定义标志未生效：%+v", c)
	}
}

// 7.4：hub.Insecure=true → 输出含「仅供开发用途」提示。
func TestRunMirror_InsecureWarning(t *testing.T) {
	spy := &spyRun{}
	var buf bytes.Buffer
	hub := newHub()
	hub.Insecure = true
	f := mirrorFlags{target: "http://127.0.0.1:8080"}
	if err := runMirror(context.Background(), &buf, hub, f, spy.run); err != nil {
		t.Fatalf("不应返回错误：%v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "仅供开发用途") {
		t.Errorf("insecure 应给出「仅供开发用途」提示，得到：%q", out)
	}
}

// 3.5/3.6：run 返回 hub 连接错误 → runMirror 返回包含原因的包装错误（非零退出）。
func TestRunMirror_HubErrorWrapped(t *testing.T) {
	hubErr := errors.New("connection refused by hub")
	spy := &spyRun{ret: hubErr}
	var buf bytes.Buffer
	f := mirrorFlags{target: "http://127.0.0.1:8080"}
	err := runMirror(context.Background(), &buf, newHub(), f, spy.run)
	if err == nil {
		t.Fatal("run 返回错误时 runMirror 应返回错误")
	}
	if !errors.Is(err, hubErr) {
		t.Errorf("应包装原始 hub 错误（errors.Is），得到：%v", err)
	}
	if !strings.Contains(err.Error(), "connection refused by hub") {
		t.Errorf("错误应含原因，得到：%v", err)
	}
}

// 6.4：run 返回 nil（信号触发的正常退出）→ runMirror 返回 nil（零退出）。
func TestRunMirror_NilRunZeroExit(t *testing.T) {
	spy := &spyRun{ret: nil}
	var buf bytes.Buffer
	f := mirrorFlags{target: "http://127.0.0.1:8080"}
	if err := runMirror(context.Background(), &buf, newHub(), f, spy.run); err != nil {
		t.Fatalf("run 返回 nil 时应零退出，得到：%v", err)
	}
}

// 6.4/6.5：ctx 取消且 run 返回 context.Canceled → 视为正常退出（零退出）。
func TestRunMirror_CanceledTreatedAsZeroExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	spy := &spyRun{ret: context.Canceled}
	var buf bytes.Buffer
	f := mirrorFlags{target: "http://127.0.0.1:8080"}
	if err := runMirror(ctx, &buf, newHub(), f, spy.run); err != nil {
		t.Fatalf("信号取消（context.Canceled）应零退出，得到：%v", err)
	}
}

// 1.4/3.2：mirrorCmd 已装配 mirror 专属标志。
func TestMirrorCmd_HasFlags(t *testing.T) {
	for _, name := range []string{"target", "method", "path", "host", "header", "rule-id"} {
		if mirrorCmd.Flags().Lookup(name) == nil {
			t.Errorf("mirrorCmd 缺少标志 --%s", name)
		}
	}
}
