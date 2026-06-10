/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"pkg.blksails.net/bk/internal/proxy"
	"pkg.blksails.net/bk/internal/tunnel"
)

// proxyForward.go 提供 `bk proxy forward` 子命令（design：File Structure Plan
// cmd/proxyForward.go；Component forwardCmd）。
//
// 边界（task 2.2）：本文件装配转发表达式解析与转发运行——把一个或多个位置参数
// 经既有 proxy.ParseForward 解析为 []proxy.Forward，在 signalContext（task 1.3）
// 下调用既有核心 proxy.Run 并发运行多条转发，并为每条输出启动确认。
// 边界（task 2.3，Requirement 5.1/5.2/5.3/5.4/5.5）：在 task 2.2 基础上补全
// 「如何到达远端」的 Dialer 选择——提供 --direct 标志，并由 selectForwardDialer
// 在隧道（internal/tunnel.New 适配 yamux Forwarder）与直连（proxy.DirectDialer）
// 两种实现间选择注入转发核心；转发核心仅依赖 proxy.Dialer 抽象（解耦，R5.3）。
// 不改 proxy.go / proxy_signal.go / proxyMirror.go / internal/*。

// forwardDirect 绑定 --direct 标志：强制使用直连 Dialer（同网段联调/测试），
// 即便 hub 配置齐备也不建隧道（Requirement 5.2）。
var forwardDirect bool

// forwardCmd 是 `bk proxy forward` 子命令。TCP 端口转发模式：在本地监听端口，
// 把入站 TCP 连接经选定 Dialer 转发到远端目标地址。
var forwardCmd = &cobra.Command{
	Use:   "forward <expr>...",
	Short: "TCP 端口转发：本地监听并把入站连接转发到远端目标",
	// Example 渲染为帮助的 "Examples:" 段（Requirement 1.4：--help 须含至少一个示例）。
	Example: `  # 经 yamux 隧道：本地 8080 转发到 app.internal:80，同时本地 9090 转发到 127.0.0.1:80
  bk proxy forward --server hub:8443 --token <tok> --app demo 8080:app.internal:80 9090:80

  # 同网段直连（--direct）：即便 hub 配置齐备也不建隧道，本地 5432 直连到 db.local:5432
  bk proxy forward --direct 5432:db.local:5432`,
	Long: `forward 接受一个或多个转发表达式，在本地监听对应端口并把入站 TCP 连接
转发到远端目标。表达式形如 local:host:remote（指定远端主机）或 local:remote
（远端主机默认为 127.0.0.1）。

可在一次调用中传入多条表达式，为每一条建立独立的本地监听与转发。任一表达式
形式非法或端口段非数字将以非零码退出；本地监听端口被占用/无法绑定亦以非零码
退出；单个入站连接拨远端失败仅终止该连接，不影响其它在途连接与后续监听。

示例:
  # 本地 8080 转发到 app:80，同时本地 9090 转发到 127.0.0.1:80
  bk proxy forward 8080:app:80 9090:80`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		specs := args

		// hub 配置对 forward 为可选（与 mirror 不同）：缺 server/token/app 时
		// resolveHubConfig 返回错误，但这里不硬失败——交由 selectForwardDialer
		// 在 hub 不全或 --direct 时回退直连（Requirement 5.2）。
		hub, hubErr := resolveHubConfig(cmd)

		// 选择 Dialer：hub 齐备且未 --direct → 经 tunnel.New 建立 yamux 隧道 Dialer
		// （传输「隧道」，并产出 io.Closer 供退出时关闭）；否则 → 直连 Dialer（传输「直连」）。
		// 隧道建立失败（如拨入/创建 forwarder 失败）→ 返回错误，非零退出且不运行转发（Requirement 5.4）。
		dialer, closer, transport, err := selectForwardDialer(hub, hubErr, forwardDirect, tunnel.New)
		if err != nil {
			return err
		}
		if closer != nil {
			// 命令退出后关闭隧道句柄，释放 yamux 会话（Requirement 5.1 隧道生命周期）。
			defer closer.Close()
		}

		// 信号驱动的可取消 context：收到 SIGINT/SIGTERM 时取消，
		// proxy.Run 据「ctx 取消即正常退出」语义停止监听并收尾在途连接（Requirement 6.x）。
		ctx, stop := signalContext(cmd.Context())
		defer stop()

		// 注入带 per-connection 拨号错误处理器的运行函数：当某个入站连接经选定 Dialer
		// 拨远端失败（如隧道被 Hub 拒绝），把原因呈现给用户（Requirement 5.4），同时
		// 该失败仍由核心做单连接隔离、不影响整体转发（Requirement 4.6）。错误写到 stderr。
		errW := cmd.ErrOrStderr()
		run := func(rctx context.Context, d proxy.Dialer, fs []proxy.Forward) error {
			return proxy.RunWithDialErrorHandler(rctx, d, fs, func(remoteAddr string, err error) {
				fmt.Fprintln(errW, forwardDialErrorMessage(remoteAddr, err))
			})
		}

		return runForwardWithWarn(ctx, cmd.OutOrStdout(), errW, dialer, transport, hub.Insecure, specs, run)
	},
}

// forwardInsecureWarning 返回 forward 路径的「仅供开发用途」insecure 提示文本，
// 当且仅当传输为「隧道」且启用 insecure 时非空（与 mirror 对称，Requirement 7.4）。
//
// 仅隧道路径经 yamux+TLS 拨入 Hub 才会因 --insecure 跳过 TLS 证书校验；直连路径
// 不走 TLS，故即使 insecure 为真也不提示（避免对同网段直连联调误导）。文本与
// proxyMirror.go 中的提示保持一致以求体验统一。
func forwardInsecureWarning(transport string, insecure bool) string {
	if transport != "隧道" || !insecure {
		return ""
	}
	return "  警告: 已启用 --insecure，跳过 Hub TLS 证书校验，仅供开发用途"
}

// forwardDialErrorMessage 构造把「单连接拨远端失败」呈现给用户的消息（Requirement 5.4）。
//
// 基础消息含远端地址与原始错误，便于用户判断连通性/配置问题（Requirement 7.x）。
// 当错误看起来是隧道被 Hub 拒绝（含 "rejected"，通常伴随 yamuxproxy 给出的原因如
// "target_not_allowed"）时，追加说明：该限制由 Hub 侧安全策略（MaxForwarders /
// ForwardTargets allowlist）执行，bk 不绕过（Requirement 5.4/5.5）。
func forwardDialErrorMessage(remoteAddr string, err error) string {
	msg := fmt.Sprintf("转发到 %s 失败：%v", remoteAddr, err)
	if isHubReject(err) {
		msg += "（该限制由 Hub 侧安全策略 ForwardTargets/MaxForwarders 执行，bk 不绕过）"
	}
	return msg
}

// isHubReject 判断拨号错误是否为隧道被 Hub 拒绝（yamuxproxy 的拒绝错误形如
// "yamuxproxy: forward to <addr> rejected: <reason>"）。
func isHubReject(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "rejected")
}

// selectForwardDialer 在隧道与直连两种 proxy.Dialer 实现间选择，体现「如何到达远端」
// 与转发核心解耦（Requirement 5.3）：转发核心仅消费返回的 Dialer，不感知传输细节。
//
// 选择规则：
//   - direct 为真，或 hubErr != nil（hub 配置不全）→ 直连 DirectDialer，传输「直连」，
//     不调用 newTunnel，无 Closer（Requirement 5.2）。
//   - 否则（hub 齐备且非 --direct）→ 由 hubConfig 映射 tunnel.Config 调用 newTunnel
//     （生产为 tunnel.New）建立 yamux 隧道 Dialer，传输「隧道」，并透出其 io.Closer
//     供调用方退出时关闭（Requirement 5.1）。
//
// newTunnel 失败时返回包装后的错误（dialer/closer/transport 均为零值），使调用方
// 非零退出且不运行转发（Requirement 5.4）。
//
// 关于 Hub 拒绝（app 未开 forwarding 或目标不在 ForwardTargets allowlist）：该拒绝
// 在「拨远端」时由隧道 Dialer 的 DialContext 透出，经 proxy.Run → runForward 以 %w
// 包装含 Hub 拒绝原因的错误呈现（Requirement 5.4）。此限制由 Hub 侧安全策略执行，
// bk 不绕过（Requirement 5.5）；本函数只负责建立隧道，不在此处复处理拨号期拒绝。
func selectForwardDialer(
	hub hubConfig,
	hubErr error,
	direct bool,
	newTunnel func(tunnel.Config) (proxy.Dialer, io.Closer, error),
) (proxy.Dialer, io.Closer, string, error) {
	// 直连分支：显式 --direct，或 hub 配置不全 → 回退直连（不建隧道）。
	if direct || hubErr != nil {
		return proxy.DirectDialer(), nil, "直连", nil
	}

	// 隧道分支：由 hubConfig 映射 tunnel.Config 并建立 yamux 隧道 Dialer。
	dialer, closer, err := newTunnel(tunnel.Config{
		ServerAddress: hub.Server,
		Token:         hub.Token,
		AppID:         hub.App,
		Insecure:      hub.Insecure,
		CAFile:        hub.CAFile,
		ServerName:    hub.ServerName,
	})
	if err != nil {
		// 隧道建立失败 → 非零退出，不运行转发（Requirement 5.4）。
		return nil, nil, "", fmt.Errorf("建立 yamux 隧道失败: %w", err)
	}
	return dialer, closer, "隧道", nil
}

// runForward 是 forward 子命令的可测核心：解析转发表达式、输出启动确认，并调用
// 注入的 run（生产为 proxy.Run）。把 run 作为参数注入使单元测试可用 fake 间谍替代
// proxy.Run（其绑定真实端口并阻塞）。
//
// 流程：
//  1. 逐个经 proxy.ParseForward 解析 specs；任一失败 → 返回含错误片段的错误，不调用
//     run（Requirement 4.3/7.3 指向具体错误片段）。
//  2. 为每条转发输出启动确认 `本地 -> 远端 (transport)`（Requirement 4.7/7.1）。
//  3. 调用 run(ctx, dialer, forwards)；返回错误时以 %w 包装透出（如端口占用/无法绑定，
//     Requirement 4.7）→ 非零退出；信号触发的正常停止（run 返回 nil 或 context.Canceled）
//     → 返回 nil（零退出，Requirement 6.x）。
func runForward(
	ctx context.Context,
	w io.Writer,
	dialer proxy.Dialer,
	transport string,
	specs []string,
	run func(context.Context, proxy.Dialer, []proxy.Forward) error,
) error {
	// 无 insecure 上下文的旧入口：提示写入丢弃端（隧道+insecure 提示仅由
	// runForwardWithWarn 经 RunE 触发，见 Requirement 7.4）。
	return runForwardWithWarn(ctx, w, io.Discard, dialer, transport, false, specs, run)
}

// runForwardWithWarn 是 runForward 的 insecure-aware 形态：在启动确认之外，于隧道 +
// insecure 时把「仅供开发用途」提示写到 errW（Requirement 7.4，与 mirror 对称）。
// errW 与启动输出 w 分离，便于把告警归到 stderr 而启动确认归到 stdout。
func runForwardWithWarn(
	ctx context.Context,
	w io.Writer,
	errW io.Writer,
	dialer proxy.Dialer,
	transport string,
	insecure bool,
	specs []string,
	run func(context.Context, proxy.Dialer, []proxy.Forward) error,
) error {
	// (1) 解析全部转发表达式；任一非法即报错指向错误片段，不启动任何转发。
	forwards := make([]proxy.Forward, 0, len(specs))
	for _, spec := range specs {
		f, err := proxy.ParseForward(spec)
		if err != nil {
			return fmt.Errorf("无效的转发表达式 %q: %w", spec, err)
		}
		forwards = append(forwards, f)
	}

	// (2) 启动确认：为每条转发输出「本地 -> 远端 (传输方式)」（Requirement 7.1）。
	for _, f := range forwards {
		fmt.Fprintf(w, "转发 %s (%s)\n", f.String(), transport)
	}
	// insecure 提示（Requirement 7.4）：仅隧道路径走 TLS，写到 stderr 与 mirror 对称。
	if warn := forwardInsecureWarning(transport, insecure); warn != "" {
		fmt.Fprintln(errW, warn)
	}
	fmt.Fprintln(w, "按 Ctrl-C 停止")

	// (3) 运行转发核心。
	if err := run(ctx, dialer, forwards); err != nil {
		// 信号触发的正常停止视为零退出（Requirement 6.x）。
		if ctx.Err() != nil || isContextCanceled(err) {
			return nil
		}
		// 透出端口占用/无法绑定等错误（Requirement 4.7），非零退出。
		return fmt.Errorf("端口转发运行失败: %w", err)
	}
	return nil
}

func init() {
	proxyCmd.AddCommand(forwardCmd)

	// --direct 强制直连，用于同网段联调/测试；不指定且 hub 配置齐备时走隧道（Requirement 5.2）。
	forwardCmd.Flags().BoolVar(&forwardDirect, "direct", false,
		"强制使用直连 Dialer（同网段联调/测试），即便 hub 配置齐备也不建 yamux 隧道")
}
