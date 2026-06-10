/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"pkg.blksails.net/bk/internal/mirror"
)

// proxyMirror.go 提供 `bk proxy mirror` 子命令（design：File Structure Plan
// cmd/proxyMirror.go 行 127；Component mirrorCmd）。
//
// 边界（task 2.1）：本文件装配 mirror 专属标志（--target/--method/--path/--host/
// --header/--rule-id），经既有 resolveHubConfig（task 1.2）取 hub/TLS，组装
// mirror.Config 并在 signalContext（task 1.3）下调用既有核心 mirror.Run。
// 不改 proxy.go / proxy_signal.go / proxyForward.go / internal/*。

// mirror 专属标志绑定到包级变量（与 forward 子命令的标志相互独立）。
var (
	mirrorTarget  string
	mirrorMethod  string
	mirrorPath    string
	mirrorHost    string
	mirrorHeaders []string
	mirrorRuleID  string
)

// mirrorFlags 聚合 mirror 子命令的原始标志输入，作为 runMirror 的可测输入。
// headers 保存原始 "Key:Value" 字符串列表，由 runMirror 解析为请求头过滤 map。
type mirrorFlags struct {
	target  string
	method  string
	path    string
	host    string
	headers []string // 原始 "Key:Value"，可重复
	ruleID  string
}

// mirrorCmd 是 `bk proxy mirror` 子命令。HTTP 流量镜像模式：作为 Consumer 经
// yamux+TLS 拨入 Hub，注册一条路由规则，把 Hub 镜像下来的 HTTP 请求反向代理到
// 本地 target（单向，响应丢弃，不回送线上；Requirement 3.x）。
var mirrorCmd = &cobra.Command{
	Use:   "mirror --target http://host:port [--method M] [--path P] [--host H] [--header K:V]... [--rule-id ID]",
	Short: "HTTP 流量镜像：拨入 Hub 并把镜像请求反代到本地 target",
	// Example 渲染为帮助的 "Examples:" 段（Requirement 1.4：--help 须含至少一个示例）。
	Example: `  # 把 GET /api 前缀的镜像请求反代到本地 8080
  bk proxy mirror --server hub:8443 --token <tok> --app demo \
    --target http://127.0.0.1:8080 --method GET --path /api

  # 仅供开发：跳过 Hub TLS 证书校验，并按请求头过滤
  bk proxy mirror --server hub:8443 --token <tok> --app demo \
    --target http://127.0.0.1:8080 --insecure --header X-Env:dev`,
	Long: `mirror 作为 Consumer 经 yamux+TLS 拨入 Hub，注册一条路由规则，把 Hub
镜像下来的 HTTP 请求反向代理到 --target 指定的本地地址。

语义为「流量镜像」：单向、反代响应不回送线上，仅用于在开发机接收/调试线上
某类请求的副本，而非 TCP 端口转发。

Hub 连接配置（--server/--token/--app/--insecure/--ca/--server-name）由 proxy
父命令的共享标志提供，或从 .bs.yaml 的 proxy.* 块读取。

示例:
  # 把 GET /api 前缀的镜像请求反代到本地 8080
  bk proxy mirror --server hub.example:8443 --token $TOK --app demo \
    --target http://127.0.0.1:8080 --method GET --path /api

  # 仅供开发：跳过 Hub TLS 证书校验，并按请求头过滤
  bk proxy mirror --target http://127.0.0.1:8080 --insecure \
    --header X-Env:dev --header X-Team:core`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// 先解析 hub 配置：缺 server/token/app 由 resolveHubConfig 返回错误，
		// cobra 以非零码退出，且不建任何连接（Requirement 2.4）。
		hub, err := resolveHubConfig(cmd)
		if err != nil {
			return err
		}

		f := mirrorFlags{
			target:  mirrorTarget,
			method:  mirrorMethod,
			path:    mirrorPath,
			host:    mirrorHost,
			headers: mirrorHeaders,
			ruleID:  mirrorRuleID,
		}

		// 信号驱动的可取消 context：收到 SIGINT/SIGTERM 时取消，
		// mirror.Run 据「ctx 取消即正常退出」语义收尾（Requirement 6.x）。
		ctx, stop := signalContext(cmd.Context())
		defer stop()

		return runMirror(ctx, cmd.OutOrStdout(), hub, f, mirror.Run)
	},
}

// runMirror 是 mirror 子命令的可测核心：解析/校验标志，组装 mirror.Config，
// 输出启动确认信息，并调用注入的 run（生产为 mirror.Run）。把 run 作为参数注入
// 使单元测试可用 fake 间谍替代真实 Hub 阻塞调用。
//
// 流程：
//  1. 解析 --header（"Key:Value" → map）；任一格式非法（无冒号）→ 报错，不调用 run（Requirement 3.2）。
//  2. 校验 --target：非空且 url.Parse 后含 scheme 与 host；否则报错，不调用 run（Requirement 3.3）。
//  3. 由 hub + flags 组装 mirror.Config（应用默认值 method=* / path=/ / rule-id=bk-mirror）。
//  4. 向 w 输出启动确认：Hub 地址、app、target，并明确「流量镜像、单向、响应丢弃」语义
//     （Requirement 7.1/3.6）；若 insecure 另给「仅供开发用途」提示（Requirement 7.4）。
//  5. 调用 run；返回错误时包装为含 Hub 连接失败原因的错误（Requirement 3.5/3.6）→ 非零退出。
//     信号触发的正常停止（run 返回 nil 或 context.Canceled）→ 返回 nil（零退出，Requirement 6.4/6.5）。
func runMirror(
	ctx context.Context,
	w io.Writer,
	hub hubConfig,
	f mirrorFlags,
	run func(context.Context, mirror.Config) error,
) error {
	// (1) 解析 header 过滤集。
	headers, err := parseMirrorHeaders(f.headers)
	if err != nil {
		return err
	}

	// (2) 校验 target：缺失或缺 scheme/host 先行报错（Requirement 3.3）。
	if f.target == "" {
		return fmt.Errorf("缺少必填项: --target（须为 http://host:port 形式的本地反代目标）")
	}
	u, err := url.Parse(f.target)
	if err != nil {
		return fmt.Errorf("无效的 --target %q: %w", f.target, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("无效的 --target %q: 须含 scheme 与 host（例如 http://127.0.0.1:8080）", f.target)
	}

	// (3) 组装 mirror.Config（应用默认值）。
	method := f.method
	if method == "" {
		method = "*"
	}
	path := f.path
	if path == "" {
		path = "/"
	}
	ruleID := f.ruleID
	if ruleID == "" {
		ruleID = "bk-mirror"
	}
	cfg := mirror.Config{
		ServerAddress: hub.Server,
		Token:         hub.Token,
		AppID:         hub.App,
		TargetURL:     f.target,
		RuleID:        ruleID,
		Method:        method,
		PathPrefix:    path,
		Host:          f.host,
		Headers:       headers,
		Insecure:      hub.Insecure,
		CAFile:        hub.CAFile,
		ServerName:    hub.ServerName,
	}

	// (4) 启动确认信息（Requirement 7.1）：明确「流量镜像/单向/响应丢弃」语义
	// （Requirement 3.6）。绝不打印 token 明文（Requirement 7.2）。
	fmt.Fprintf(w, "启动 HTTP 流量镜像：Hub %s，app %s → 反代到 %s\n", hub.Server, hub.App, f.target)
	fmt.Fprintf(w, "  规则: method=%s path=%s rule-id=%s", method, path, ruleID)
	if f.host != "" {
		fmt.Fprintf(w, " host=%s", f.host)
	}
	if len(headers) > 0 {
		fmt.Fprintf(w, " headers=%d 条", len(headers))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  语义: 流量镜像（单向，反代响应丢弃、不回送线上），非 TCP 端口转发")
	if hub.Insecure {
		// insecure 提示（Requirement 7.4）。
		fmt.Fprintln(w, "  警告: 已启用 --insecure，跳过 Hub TLS 证书校验，仅供开发用途")
	}
	fmt.Fprintln(w, "  按 Ctrl-C 停止")

	// (5) 运行镜像核心。
	if err := run(ctx, cfg); err != nil {
		// 信号触发的正常停止视为零退出（Requirement 6.4/6.5）。
		if ctx.Err() != nil || isContextCanceled(err) {
			return nil
		}
		// 透出 Hub 连接失败/被拒原因（Requirement 3.5/3.6），非零退出。
		return fmt.Errorf("镜像运行失败（与 Hub %s 的连接/订阅出错）: %w", hub.Server, err)
	}
	return nil
}

// parseMirrorHeaders 把 "Key:Value" 列表解析为请求头过滤 map。
// 任一项缺少 ':' 分隔符即视为格式非法并返回错误（Requirement 3.2）。
func parseMirrorHeaders(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for _, h := range raw {
		k, v, ok := strings.Cut(h, ":")
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if !ok || k == "" {
			return nil, fmt.Errorf("无效的 --header %q: 须为 Key:Value 形式", h)
		}
		out[k] = v
	}
	return out, nil
}

// isContextCanceled 报告 err 是否由 context 取消（信号触发的正常退出）导致。
func isContextCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func init() {
	proxyCmd.AddCommand(mirrorCmd)

	// mirror 专属标志（Requirement 3.2）：定义在 mirrorCmd 的本地 Flags 上，
	// 与 proxyCmd 的共享 hub 标志（PersistentFlags）互不冲突。
	fl := mirrorCmd.Flags()
	fl.StringVar(&mirrorTarget, "target", "", "本地反代目标，须为 http://host:port 形式（必填）")
	fl.StringVar(&mirrorMethod, "method", "", "HTTP 方法过滤，缺省任意（*）")
	fl.StringVar(&mirrorPath, "path", "", "路径前缀过滤，缺省 /")
	fl.StringVar(&mirrorHost, "host", "", "可选 Host 头精确匹配")
	fl.StringArrayVar(&mirrorHeaders, "header", nil, "请求头过滤 Key:Value（可重复，全部需匹配）")
	fl.StringVar(&mirrorRuleID, "rule-id", "", "客户端自定义路由规则 ID，缺省 bk-mirror")
}
