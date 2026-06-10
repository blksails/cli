package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/proxy"
	"pkg.blksails.net/bk/internal/tunnel"
)

// proxyForward_test.go 覆盖 task 2.2 的可测核心 runForward 与 forwardCmd 的参数装配
// （Requirements 4.1/4.2/4.3/4.4/4.5/4.6/4.7/7.1/7.3，design：forwardCmd 组件）。
//
// 这些测试通过注入 fake run 间谍，避免触达 proxy.Run（会绑定真实端口并阻塞），
// 只验证命令层的表达式解析、错误片段透出、启动输出与错误/取消透出语义。

// spyForwardRun 记录是否被调用及收到的 forwards，并返回预置错误。
type spyForwardRun struct {
	called   bool
	forwards []proxy.Forward
	ret      error
}

func (s *spyForwardRun) run(_ context.Context, _ proxy.Dialer, forwards []proxy.Forward) error {
	s.called = true
	s.forwards = forwards
	return s.ret
}

// TestRunForwardValidMultiSpec 验证多条合法表达式被逐个解析并交给 run（Req 4.1/4.2/4.4）。
func TestRunForwardValidMultiSpec(t *testing.T) {
	spy := &spyForwardRun{}
	var buf bytes.Buffer
	err := runForward(context.Background(), &buf, proxy.DirectDialer(), "直连",
		[]string{"8080:app:80", "9090:80"}, spy.run)
	if err != nil {
		t.Fatalf("runForward 应成功，得到错误: %v", err)
	}
	if !spy.called {
		t.Fatal("run 应被调用")
	}
	if len(spy.forwards) != 2 {
		t.Fatalf("应解析出 2 条转发，得到 %d", len(spy.forwards))
	}
	// 第 1 条：8080:app:80 → local 8080，remote app:80
	if got := spy.forwards[0]; got.LocalPort != 8080 || got.RemoteHost != "app" || got.RemotePort != 80 {
		t.Errorf("forwards[0] 解析错误: %+v", got)
	}
	// 第 2 条：9090:80 → local 9090，remote 127.0.0.1:80（省略远端主机）
	if got := spy.forwards[1]; got.LocalPort != 9090 || got.RemoteHost != "127.0.0.1" || got.RemotePort != 80 {
		t.Errorf("forwards[1] 解析错误: %+v", got)
	}
}

// TestRunForwardInvalidSpec 验证端口段非数字的非法表达式报错且指向错误片段，且不调用 run（Req 4.3/7.3）。
func TestRunForwardInvalidSpec(t *testing.T) {
	spy := &spyForwardRun{}
	var buf bytes.Buffer
	err := runForward(context.Background(), &buf, proxy.DirectDialer(), "直连",
		[]string{"abc:80"}, spy.run)
	if err == nil {
		t.Fatal("非法表达式应返回错误")
	}
	if !strings.Contains(err.Error(), "abc:80") {
		t.Errorf("错误信息应指向错误片段 abc:80，得到: %v", err)
	}
	if spy.called {
		t.Error("解析失败时不应调用 run")
	}
}

// TestRunForwardBindError 验证 run 返回的端口占用/绑定错误被包装透出（errors.Is 可识别）（Req 4.7）。
func TestRunForwardBindError(t *testing.T) {
	bindErr := errors.New("proxy: 监听本地 127.0.0.1:8080 失败: address already in use")
	spy := &spyForwardRun{ret: bindErr}
	var buf bytes.Buffer
	err := runForward(context.Background(), &buf, proxy.DirectDialer(), "直连",
		[]string{"8080:80"}, spy.run)
	if err == nil {
		t.Fatal("run 返回绑定错误时 runForward 应返回错误")
	}
	if !errors.Is(err, bindErr) {
		t.Errorf("应以 %%w 包装原始错误，errors.Is 应为真，得到: %v", err)
	}
}

// TestRunForwardContextCanceled 验证信号触发的正常停止（run 返回 context.Canceled）→ nil（Req 6.x）。
func TestRunForwardContextCanceled(t *testing.T) {
	spy := &spyForwardRun{ret: context.Canceled}
	var buf bytes.Buffer
	err := runForward(context.Background(), &buf, proxy.DirectDialer(), "直连",
		[]string{"8080:80"}, spy.run)
	if err != nil {
		t.Fatalf("context.Canceled（正常停止）应返回 nil，得到: %v", err)
	}
}

// TestRunForwardStartupOutput 验证每条转发输出含 "->" 与传输标签（Req 4.7/7.1）。
func TestRunForwardStartupOutput(t *testing.T) {
	spy := &spyForwardRun{}
	var buf bytes.Buffer
	err := runForward(context.Background(), &buf, proxy.DirectDialer(), "直连",
		[]string{"8080:app:80", "9090:80"}, spy.run)
	if err != nil {
		t.Fatalf("runForward 应成功，得到错误: %v", err)
	}
	out := buf.String()
	if c := strings.Count(out, "->"); c < 2 {
		t.Errorf("应为每条转发输出 '本地 -> 远端'，'->' 出现次数 %d，输出:\n%s", c, out)
	}
	if c := strings.Count(out, "直连"); c < 2 {
		t.Errorf("每条转发应输出传输标签 '直连'，出现次数 %d，输出:\n%s", c, out)
	}
}

// --- task 2.3：selectForwardDialer 的 Dialer 选择（隧道/直连）（Req 5.1/5.2/5.3/5.4） ---

// fakeCloser 是 newTunnel 间谍返回的可关闭句柄，用于断言 selectForwardDialer
// 透出的正是 newTunnel 产出的那个 closer（便于 RunE defer-close）。
type fakeCloser struct{ closed bool }

func (c *fakeCloser) Close() error { c.closed = true; return nil }

// spyNewTunnel 记录 newTunnel 是否被调用及收到的 tunnel.Config，并返回预置结果。
type spyNewTunnel struct {
	called bool
	gotCfg tunnel.Config
	dialer proxy.Dialer
	closer io.Closer
	err    error
}

func (s *spyNewTunnel) new(cfg tunnel.Config) (proxy.Dialer, io.Closer, error) {
	s.called = true
	s.gotCfg = cfg
	return s.dialer, s.closer, s.err
}

// TestSelectForwardDialerDirectFlag 验证 --direct 时走直连、不调用 newTunnel（Req 5.2/5.3）。
func TestSelectForwardDialerDirectFlag(t *testing.T) {
	spy := &spyNewTunnel{dialer: proxy.DirectDialer(), closer: &fakeCloser{}}
	hub := hubConfig{Server: "hub:8443", Token: "t", App: "a"}
	dialer, closer, transport, err := selectForwardDialer(hub, nil, true, spy.new)
	if err != nil {
		t.Fatalf("--direct 不应返回错误，得到: %v", err)
	}
	if spy.called {
		t.Error("--direct 时不应调用 newTunnel")
	}
	if dialer == nil {
		t.Error("应返回直连 Dialer")
	}
	if closer != nil {
		t.Error("直连不应返回 closer")
	}
	if transport != "直连" {
		t.Errorf("传输标签应为 直连，得到: %q", transport)
	}
}

// TestSelectForwardDialerIncompleteHubFallback 验证 hub 配置不全（hubErr != nil）
// 且未 --direct 时回退直连、不调用 newTunnel（Req 5.2）。
func TestSelectForwardDialerIncompleteHubFallback(t *testing.T) {
	spy := &spyNewTunnel{dialer: proxy.DirectDialer(), closer: &fakeCloser{}}
	hubErr := errors.New("缺少必填项: server, token, app")
	dialer, closer, transport, err := selectForwardDialer(hubConfig{}, hubErr, false, spy.new)
	if err != nil {
		t.Fatalf("hub 不全应回退直连而非报错，得到: %v", err)
	}
	if spy.called {
		t.Error("hub 不全时不应调用 newTunnel（应回退直连）")
	}
	if dialer == nil {
		t.Error("应返回直连 Dialer")
	}
	if closer != nil {
		t.Error("直连不应返回 closer")
	}
	if transport != "直连" {
		t.Errorf("传输标签应为 直连，得到: %q", transport)
	}
}

// TestSelectForwardDialerTunnel 验证 hub 齐备且未 --direct 时走隧道：newTunnel 被调用，
// tunnel.Config 由 hubConfig 正确映射，返回隧道 Dialer + 该 closer + "隧道"（Req 5.1/5.3）。
func TestSelectForwardDialerTunnel(t *testing.T) {
	wantDialer := proxy.DirectDialer() // 任意非 nil Dialer 作为隧道 Dialer 占位
	wantCloser := &fakeCloser{}
	spy := &spyNewTunnel{dialer: wantDialer, closer: wantCloser}
	hub := hubConfig{
		Server:     "hub.example:8443",
		Token:      "tok",
		App:        "demo",
		Insecure:   true,
		CAFile:     "/etc/ca.pem",
		ServerName: "hub.internal",
	}
	dialer, closer, transport, err := selectForwardDialer(hub, nil, false, spy.new)
	if err != nil {
		t.Fatalf("隧道分支不应返回错误，得到: %v", err)
	}
	if !spy.called {
		t.Fatal("hub 齐备且非 --direct 时应调用 newTunnel")
	}
	// tunnel.Config 字段映射断言（Req 5.1/5.3）。
	if spy.gotCfg.ServerAddress != hub.Server {
		t.Errorf("ServerAddress 映射错误: %q", spy.gotCfg.ServerAddress)
	}
	if spy.gotCfg.Token != hub.Token {
		t.Errorf("Token 映射错误: %q", spy.gotCfg.Token)
	}
	if spy.gotCfg.AppID != hub.App {
		t.Errorf("AppID 映射错误: %q", spy.gotCfg.AppID)
	}
	if spy.gotCfg.Insecure != hub.Insecure {
		t.Errorf("Insecure 映射错误: %v", spy.gotCfg.Insecure)
	}
	if spy.gotCfg.CAFile != hub.CAFile {
		t.Errorf("CAFile 映射错误: %q", spy.gotCfg.CAFile)
	}
	if spy.gotCfg.ServerName != hub.ServerName {
		t.Errorf("ServerName 映射错误: %q", spy.gotCfg.ServerName)
	}
	if dialer != wantDialer {
		t.Error("应返回 newTunnel 产出的隧道 Dialer")
	}
	// closer 必须是 newTunnel 产出的那个（供 RunE defer-close）。
	if closer != io.Closer(wantCloser) {
		t.Error("应透出 newTunnel 产出的 closer")
	}
	if transport != "隧道" {
		t.Errorf("传输标签应为 隧道，得到: %q", transport)
	}
}

// TestSelectForwardDialerTunnelBuildError 验证 newTunnel 失败时 selectForwardDialer
// 返回非 nil 错误（使 RunE 非零退出、不运行转发）（Req 5.4）。
func TestSelectForwardDialerTunnelBuildError(t *testing.T) {
	buildErr := errors.New("tunnel: 创建 forwarder 失败")
	spy := &spyNewTunnel{err: buildErr}
	hub := hubConfig{Server: "hub:8443", Token: "t", App: "a"}
	dialer, closer, transport, err := selectForwardDialer(hub, nil, false, spy.new)
	if err == nil {
		t.Fatal("newTunnel 失败时应返回错误")
	}
	if !errors.Is(err, buildErr) {
		t.Errorf("应以 %%w 包装原始建隧道错误，得到: %v", err)
	}
	if dialer != nil || closer != nil || transport != "" {
		t.Errorf("失败时应返回零值 dialer/closer/transport，得到: %v / %v / %q", dialer, closer, transport)
	}
}

// TestForwardCmdHasDirectFlag 验证 forwardCmd 定义了 --direct 布尔标志（Req 5.2）。
func TestForwardCmdHasDirectFlag(t *testing.T) {
	f := forwardCmd.Flags().Lookup("direct")
	if f == nil {
		t.Fatal("forwardCmd 应定义 --direct 标志")
	}
	if f.Value.Type() != "bool" {
		t.Errorf("--direct 应为 bool 类型，得到: %s", f.Value.Type())
	}
}

// --- task 3.2（修复）：forwardDialErrorMessage 把 per-connection 拨号错误呈现给用户 ---

// TestForwardDialErrorMessageRejectReason 验证当拨号错误是 Hub 拒绝（含 "rejected"/
// "target_not_allowed"）时，呈现给用户的消息既包含远端地址与 Hub 拒绝原因（R5.4），
// 又说明该限制由 Hub 侧安全策略执行、bk 不绕过（R5.4/R5.5）。
func TestForwardDialErrorMessageRejectReason(t *testing.T) {
	remoteAddr := "127.0.0.1:1"
	rejectErr := errors.New("yamuxproxy: forward to 127.0.0.1:1 rejected: target_not_allowed")
	msg := forwardDialErrorMessage(remoteAddr, rejectErr)

	if !strings.Contains(msg, remoteAddr) {
		t.Errorf("消息应包含远端地址 %q，得到: %q", remoteAddr, msg)
	}
	if !strings.Contains(msg, "rejected") || !strings.Contains(msg, "target_not_allowed") {
		t.Errorf("消息应如实呈现 Hub 拒绝原因（'rejected'/'target_not_allowed'），得到: %q", msg)
	}
	// Hub 策略说明（R5.4/R5.5）：限制由 Hub 侧执行，bk 不绕过。
	if !strings.Contains(msg, "Hub") {
		t.Errorf("被 Hub 拒绝时消息应说明该限制由 Hub 侧安全策略决定，得到: %q", msg)
	}
}

// TestForwardDialErrorMessageGeneric 验证一般拨号失败（非 Hub 拒绝）时，消息仍呈现
// 远端地址与原始错误，但不追加 Hub 策略说明（避免对直连失败误导）。
func TestForwardDialErrorMessageGeneric(t *testing.T) {
	remoteAddr := "db.local:5432"
	dialErr := errors.New("dial tcp: connection refused")
	msg := forwardDialErrorMessage(remoteAddr, dialErr)

	if !strings.Contains(msg, remoteAddr) {
		t.Errorf("消息应包含远端地址 %q，得到: %q", remoteAddr, msg)
	}
	if !strings.Contains(msg, "connection refused") {
		t.Errorf("消息应如实呈现原始拨号错误，得到: %q", msg)
	}
	if strings.Contains(msg, "Hub") {
		t.Errorf("一般拨号失败不应追加 Hub 策略说明（避免误导），得到: %q", msg)
	}
}

// --- task 7.4（修复）：forward 路径在隧道 + insecure 时给出「仅供开发用途」提示 ---

// TestForwardInsecureWarningTunnelInsecure 验证 transport=="隧道" 且 insecure=true 时
// 产生「跳过 TLS 证书校验、仅供开发用途」的提示（与 mirror 对称，Req 7.4）。
func TestForwardInsecureWarningTunnelInsecure(t *testing.T) {
	msg := forwardInsecureWarning("隧道", true)
	if msg == "" {
		t.Fatal("隧道 + insecure 时应产生 insecure 提示")
	}
	if !strings.Contains(msg, "--insecure") {
		t.Errorf("提示应提及 --insecure，得到: %q", msg)
	}
	if !strings.Contains(msg, "仅供开发用途") {
		t.Errorf("提示应明确仅供开发用途，得到: %q", msg)
	}
	if !strings.Contains(msg, "TLS") {
		t.Errorf("提示应说明跳过 TLS 证书校验，得到: %q", msg)
	}
}

// TestForwardInsecureWarningDirectNoWarning 验证直连路径（无 TLS）即使 insecure=true
// 也不产生 insecure 提示（直连不走 TLS，Req 7.4 仅针对 TLS 路径）。
func TestForwardInsecureWarningDirectNoWarning(t *testing.T) {
	if msg := forwardInsecureWarning("直连", true); msg != "" {
		t.Errorf("直连路径不应产生 insecure 提示，得到: %q", msg)
	}
}

// TestForwardInsecureWarningTunnelSecure 验证隧道但未 insecure 时不产生提示。
func TestForwardInsecureWarningTunnelSecure(t *testing.T) {
	if msg := forwardInsecureWarning("隧道", false); msg != "" {
		t.Errorf("隧道但非 insecure 不应产生提示，得到: %q", msg)
	}
}

// TestRunForwardEmitsInsecureWarning 验证隧道 + insecure 时 runForward 把 insecure 提示
// 写到 stderr（errW），且直连/隧道非 insecure 时不写（Req 7.4）。
func TestRunForwardEmitsInsecureWarning(t *testing.T) {
	cases := []struct {
		name      string
		transport string
		insecure  bool
		wantWarn  bool
	}{
		{"隧道+insecure", "隧道", true, true},
		{"隧道+secure", "隧道", false, false},
		{"直连+insecure", "直连", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &spyForwardRun{}
			var out, errBuf bytes.Buffer
			err := runForwardWithWarn(context.Background(), &out, &errBuf, proxy.DirectDialer(),
				tc.transport, tc.insecure, []string{"8080:80"}, spy.run)
			if err != nil {
				t.Fatalf("runForwardWithWarn 应成功，得到错误: %v", err)
			}
			gotWarn := strings.Contains(errBuf.String(), "仅供开发用途")
			if gotWarn != tc.wantWarn {
				t.Errorf("insecure 提示 = %v，期望 %v；stderr:\n%s", gotWarn, tc.wantWarn, errBuf.String())
			}
		})
	}
}

// TestForwardCmdRequiresArg 验证 forwardCmd 要求至少一个位置参数（0 参被拒）（Req 4.x，cobra.MinimumNArgs(1)）。
func TestForwardCmdRequiresArg(t *testing.T) {
	if forwardCmd.Args == nil {
		t.Fatal("forwardCmd.Args 应设置为 cobra.MinimumNArgs(1)")
	}
	if err := forwardCmd.Args(forwardCmd, []string{}); err == nil {
		t.Error("0 个位置参数应被拒绝")
	}
	if err := forwardCmd.Args(forwardCmd, []string{"8080:80"}); err != nil {
		t.Errorf("1 个位置参数应被接受，得到: %v", err)
	}
}
