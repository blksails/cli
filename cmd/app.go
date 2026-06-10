/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"pkg.blksails.net/bk/internal/dokku"
	"pkg.blksails.net/bk/internal/sshx"
)

// app.go 提供 `bk app` 命令组的父命令与按 profile 的连接装配辅助层
// （design：File Structure Plan cmd/app.go 行 132；Components appCmd 行 214 / appClient 行 215）。
//
// 边界（_Boundary: appCmd, appClient_）：本文件只负责
//   - 注册 `app` 父命令到既有 rootCmd（不改 root.go 业务逻辑；经 init() 追加，design 行 132）。
//   - 定义命令组级持久标志 --sudo（默认 false）/ --raw（Requirement 11.3 / 12.2）。
//   - 暴露供后续 ls/create/destroy/config/ps/... 子命令复用的连接装配辅助 appClient；
//     子命令各自在其文件的 init() 里 self-register 到 appCmd（本文件不挂子命令）。
//
// 依赖方向（design 行 245）：cmd/app* → cmd 层 SSHConfig、internal/dokku、internal/sshx；
// internal/* 不反向依赖 cmd。装配（取 SSH 配置 → 构造并连接 dokku.Client）刻意集中在 cmd 层完成。

// appCmd 是 `bk app` 命令组的父命令。本身不执行动作，仅承载子命令与命令组级
// 持久标志（--sudo 供 appClient 读取以决定执行模式；--raw 供子命令读取以输出原文）。
var appCmd = &cobra.Command{
	Use:   "app",
	Short: "管理 Dokku 应用（列举/创建/销毁/配置/进程/日志）",
	Long: `通过进程内 SSH 连接 Dokku 主机并管理应用。

连接参数取自当前 profile 的 ssh 块（见 .bs.yaml 的 ssh.host 等），
随全局 --profile 标志切换目标主机。--sudo 控制以 sudo dokku 形式执行；
--raw 让支持的子命令直接输出 dokku 原始文本而非表格。`,
	// 父命令本身无动作：直接打印帮助（含子命令清单与 --sudo/--raw 标志）。
	// 给定 RunE 也让 cobra 在尚无子命令时仍渲染 Usage/Flags 区块，保证
	// `bk app --help` 列出标志（完成态）。子命令由各自文件 init() 注册后自动出现。
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

// appSudo 控制装配 dokku.Client 时是否以 `sudo dokku <args>` 形式执行（普通管理员
// 账号需 sudo 包装；标准 dokku 强制命令账号则为 false）。作为命令组级持久标志，
// appClient 通过它决定 dokku.Config.Sudo（Requirement 11.3）。
var appSudo bool

// appRaw 让支持的子命令直接输出 dokku 原始文本而非表格化结果（Requirement 12.2）。
// 作为命令组级持久标志由各子命令按需读取；本文件仅负责注册。
var appRaw bool

func init() {
	rootCmd.AddCommand(appCmd)

	appCmd.PersistentFlags().BoolVar(&appSudo, "sudo", false,
		"以 sudo 方式执行 dokku 命令（普通管理员账号使用；默认按 dokku 强制命令执行）")
	appCmd.PersistentFlags().BoolVar(&appRaw, "raw", false,
		"输出 dokku 原始文本而非表格化结果")
}

// appClient 按当前 profile 装配并返回已连接的 dokku.Client（Requirement 11.1/11.2/11.3/11.4）。
// 经 cli-foundation 的 SSHConfig(profile) 取连接参数，按命令组级 --sudo 持久标志构造
// dokku.Config 并连接。返回的 client 由调用方负责 Close。
//
// 经 internal/dokku → internal/sshx 执行，不依赖系统 ssh 可执行文件（11.4）。
func appClient(profile string) (*dokku.Client, error) {
	return appClientWith(profile, appSudo, SSHConfig, dokku.New)
}

// appClientWith 是 appClient 的可测核心：把 SSH 配置加载与 dokku 客户端构造作为
// 钩子注入，使装配逻辑无需真实 SSH 服务即可单测。
//
// 行为：调用 loadSSHConfig(profile) 取 sshx.Config；该入口在 ssh.host 缺失/为空时已
// 返回错误，本助手透传并包装为引导配置的可读错误后返回，且不调用 newClient
// （Requirement 11.2）。否则以 dokku.Config{SSH: cfg, Sudo: sudo} 调 newClient——
// sudo=false 即 dokku 用户强制命令模式，sudo=true 走 sudo dokku（Requirement 11.3）。
// SSH 登录用户为空时由 dokku.New 默认为 "dokku"，本助手不在此重定义 ssh 块。
func appClientWith(
	profile string,
	sudo bool,
	loadSSHConfig func(string) (sshx.Config, error),
	newClient func(dokku.Config) (*dokku.Client, error),
) (*dokku.Client, error) {
	cfg, err := loadSSHConfig(profile)
	if err != nil {
		// 透传 SSHConfig 错误并引导配置 ssh 主机（Requirement 11.2）。
		return nil, fmt.Errorf(
			"无法获取 Dokku 主机连接配置：%w\n请在 .bs.yaml 中配置 ssh.host（及可选 ssh.user/ssh.port/ssh.identity），"+
				"或运行 `bk` 配置当前 profile 的 SSH 接入后重试",
			err)
	}
	return newClient(dokku.Config{SSH: cfg, Sudo: sudo})
}
