/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// proxy.go 提供 `bk proxy` 命令族的父命令骨架（design：File Structure Plan
// cmd/proxy.go 行 126；Component proxyCmd 行 215）。
//
// 边界（task 1.1）：本文件只负责
//   - 把 `proxy` 父命令经 init() 注册到 cli-foundation 的既有 rootCmd
//     （不改 root.go；沿用 cmd/upgrade.go / cmd/app.go 的 self-register 约定）。
//   - 承载 `mirror`/`forward` 两个子命令（子命令各自在其文件的 init() 中注册到 proxyCmd）。
//   - 无子命令执行 `bk proxy` 时显示帮助而非报错退出（Requirement 1.3）。
//
// 共享 hub 连接标志（--server/--token/--app/--insecure/--ca/--server-name）与
// resolveHubConfig 助手属后续任务（task 2.x，Requirement 2.x），此处刻意不引入，
// 保持骨架最小、便于后续干净扩展。

// proxyCmd 是 `bk proxy` 命令族的父命令。本身不执行动作，仅承载 mirror/forward
// 两个子命令。给定 RunE 直接渲染帮助，使无子命令执行 `bk proxy` 时显示用法
// （含可用子命令清单）而非报错退出（Requirement 1.1/1.2/1.3）。
var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "本地代理命令族（HTTP 流量镜像 / TCP 端口转发）",
	Long: `bk proxy 提供两种面向开发联调的本地代理模式，二者共享同一套
yamux+TLS 隧道连接配置（client 拨入 Hub，NAT 友好）：

  mirror   作为 Consumer 拨入 Hub，把镜像下来的 HTTP 请求反代到本地 target
           （单向，响应丢弃，不回送线上）。
  forward  在本地监听端口，把入站 TCP 连接经隧道或直连转发到远端目标地址。

直接执行 bk proxy（不带子命令）将显示本帮助及可用子命令。`,
	// 父命令本身无动作：直接打印帮助（含子命令清单）。给定 RunE 让 cobra 在
	// 无子命令时仍渲染 Usage 区块，保证 `bk proxy` 与 `bk proxy --help`
	// 都列出 mirror/forward（Requirement 1.3）。
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

// 共享 hub 连接标志（task 1.2，Requirement 2.1）：绑定到包级变量，供 mirror/forward
// 两子命令经 proxyCmd 的 PersistentFlags 复用。标志名保持扁平（不带 proxy. 前缀），
// 其对应的 .bs.yaml 配置键则收纳于独立的 proxy.* 命名空间（与 ssh.* 互不依赖）。
var (
	proxyServer     string
	proxyToken      string
	proxyApp        string
	proxyInsecure   bool
	proxyCAFile     string
	proxyServerName string
)

// hubConfig 聚合 mirror/forward 共享的 Hub 连接参数（design：hubConfig 助手）。
// 字段命名与 internal/mirror.Config / internal/tunnel.Config 的输入对齐
// （Server→ServerAddress、Token、App→AppID、Insecure、CAFile、ServerName），
// 便于后续 task 2.x 直接装配核心 Config。
type hubConfig struct {
	Server     string // host:port，必填
	Token      string // 共享 token，必填
	App        string // app_id，必填
	Insecure   bool   // 跳过 TLS 证书校验（仅开发）
	CAFile     string // 可选 CA bundle 路径
	ServerName string // 可选 TLS ServerName 覆盖
}

// resolveHubConfig 从 proxyCmd 的标志与 viper（.bs.yaml 的 proxy.* 块）解析 hub
// 配置并校验必填项（design：resolveHubConfig）。标志优先、否则回退配置文件。
//
// 对 --insecure（bool）以标志的 Changed 状态决定优先级：未显式设置时回退
// proxy.insecure 配置；显式设置时（含 --insecure=false）标志优先。
func resolveHubConfig(cmd *cobra.Command) (hubConfig, error) {
	insecureSet := cmd.Flags().Changed("insecure")
	return resolveHubConfigFrom(
		proxyServer, proxyToken, proxyApp,
		proxyInsecure, proxyCAFile, proxyServerName,
		insecureSet, viper.GetViper(),
	)
}

// resolveHubConfigFrom 是 resolveHubConfig 的可测核心：注入显式标志值与
// *viper.Viper，使测试可同时供标志值与含 proxy.* 键的 viper 并断言优先级，
// 无需触达文件系统或全局 viper（镜像 cli-foundation 的 sshConfigFrom 模式）。
//
// 优先级（Requirement 2.2/2.3）：字符串项非空标志优先，否则回退 viper proxy.<key>；
// 布尔项 insecure 以 flagInsecureSet（标志 Changed 状态）决定——已设置则用标志值，
// 否则回退 proxy.insecure。
//
// 校验（Requirement 2.4/7.2）：server/token/app 解析后为空 → 返回指明缺失项的错误
// （错误绝不含 token 明文），并返回零值 hubConfig，使调用方非零退出且不建连。
func resolveHubConfigFrom(
	flagServer, flagToken, flagApp string,
	flagInsecure bool,
	flagCA, flagServerName string,
	flagInsecureSet bool,
	v *viper.Viper,
) (hubConfig, error) {
	cfg := hubConfig{
		Server:     firstNonEmpty(flagServer, v.GetString("proxy.server")),
		Token:      firstNonEmpty(flagToken, v.GetString("proxy.token")),
		App:        firstNonEmpty(flagApp, v.GetString("proxy.app")),
		CAFile:     firstNonEmpty(flagCA, v.GetString("proxy.ca")),
		ServerName: firstNonEmpty(flagServerName, v.GetString("proxy.server_name")),
	}
	if flagInsecureSet {
		cfg.Insecure = flagInsecure
	} else {
		cfg.Insecure = v.GetBool("proxy.insecure")
	}

	var missing []string
	if cfg.Server == "" {
		missing = append(missing, "server")
	}
	if cfg.Token == "" {
		missing = append(missing, "token")
	}
	if cfg.App == "" {
		missing = append(missing, "app")
	}
	if len(missing) > 0 {
		// 仅列出缺失项名称；token 明文绝不进入错误信息（Requirement 7.2）。
		return hubConfig{}, fmt.Errorf("缺少必填项: %s（经 --%s 标志或 .bs.yaml 的 proxy.* 配置提供）",
			strings.Join(missing, ", "), strings.Join(missing, "/--"))
	}
	return cfg, nil
}

// firstNonEmpty 返回第一个非空字符串，用于实现「标志优先、否则回退配置」。
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func init() {
	rootCmd.AddCommand(proxyCmd)

	// 共享 hub 连接标志（Requirement 2.1）：定义在 proxyCmd 的 PersistentFlags 上，
	// 使 mirror/forward 两子命令均可继承复用。
	pf := proxyCmd.PersistentFlags()
	pf.StringVar(&proxyServer, "server", "", "Hub 的 TLS 地址 host:port（覆盖 .bs.yaml 的 proxy.server）")
	pf.StringVar(&proxyToken, "token", "", "共享认证 token（覆盖 .bs.yaml 的 proxy.token）")
	pf.StringVar(&proxyApp, "app", "", "app_id（覆盖 .bs.yaml 的 proxy.app）")
	pf.BoolVar(&proxyInsecure, "insecure", false, "跳过 Hub TLS 证书校验（仅供开发用途；覆盖 .bs.yaml 的 proxy.insecure）")
	pf.StringVar(&proxyCAFile, "ca", "", "可选 CA bundle 路径（覆盖 .bs.yaml 的 proxy.ca）")
	pf.StringVar(&proxyServerName, "server-name", "", "可选 TLS ServerName 覆盖（覆盖 .bs.yaml 的 proxy.server_name）")
}
