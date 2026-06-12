/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"pkg.blksails.net/bk/internal/dokku"
)

// app_logs.go 实现 `bk app logs <app>`：在当前 profile 指向的 Dokku 主机上
// 读取并展示应用的日志快照，并提供 `-n <N>` 限制行数选项
// （design「app_logs（R10）」/「通用执行流」；Requirement 10.1/10.2/10.3/10.4/10.5）。
//
// 边界（_Boundary: appLogsCmd_）：本文件只承载 logs 子命令与其可测核心 runAppLogs，
// 经 init() self-register 到既有 appCmd。复用 app.go 的连接装配 appClient，
// 不修改 app.go / app_render.go / internal/*。

// appLogReader 抽象 logs 所需的唯一读取缝：把 dokku 的日志写入 w。
// *dokku.Client 通过其 Logs 满足该接口，使 runAppLogs 可注入 fake、
// 在不触达真实 SSH/Dokku 的前提下被验证。
type appLogReader interface {
	Logs(ctx context.Context, w io.Writer, app string, opts dokku.LogsOptions) error
}

// runAppLogs 是 logs 的可测核心。
//
// 选项语义与 dokku.Client.Logs 一致并直传给远端 dokku：
//   - Num>0 限制为最近 N 行（Requirement 10.2）；numSet=false 时为默认快照（Requirement 10.1）。
//   - Process 仅显示指定进程类型；Quiet 输出原始日志；Tail 持续流式输出。
//
// 校验：当用户显式提供 `-n`（numSet=true）但值非正（opts.Num<=0）时，给出可读提示并直接返回错误，
// 在调用远端前于核心层拦截、不触发 Logs（Requirement 10.3，由命令层非零退出）。
//
// 成功时由 Logs 把 dokku 输出写入 w（快照一次性写入，--tail 实时流式）。Logs 已把 dokku
// stderr 拼入 error；读取被拒绝（如应用不存在）时以 %w 透传，由命令层非零退出（Requirement 10.5）。
func runAppLogs(ctx context.Context, w io.Writer, c appLogReader, app string, opts dokku.LogsOptions, numSet bool) error {
	if numSet && opts.Num <= 0 {
		return fmt.Errorf("行数需为正整数，得到 %d", opts.Num)
	}
	if err := c.Logs(ctx, w, app, opts); err != nil {
		return fmt.Errorf("读取应用 %q 日志失败：%w", app, err)
	}
	return nil
}

// 本命令的局部选项，对应 dokku logs 的标志：
//   - appLogsNum   -n/--num：默认 0 表示“无 -n 限制 / 默认快照”，与 dokku num<=0 语义一致。
//     是否显式设置由 cmd.Flags().Changed("num") 判定，从而区分默认快照与 -n 0 的非法输入。
//   - appLogsPs    -p/--ps：仅显示指定进程类型的日志。
//   - appLogsQuiet -q/--quiet：原始日志（无颜色/时间/进程名前缀）。
//   - appLogsTail  -t/--tail：持续流式输出，直到中断。
var (
	appLogsNum   int
	appLogsPs    string
	appLogsQuiet bool
	appLogsTail  bool
)

// appLogsCmd 是 `bk app logs <app>`。装配按当前 profile 连接的 dokku.Client 后
// 委托 runAppLogs；RunE 保持轻薄，读取/校验/展示/退出码语义均落在 runAppLogs。
//
// 采用 cobra.ExactArgs(1)：未提供应用名（0 参数）时由 cobra 提示参数错误并以
// 非零退出码结束（Requirement 10.4）。
var appLogsCmd = &cobra.Command{
	Use:   "logs <app>",
	Short: "查看 Dokku 应用的日志快照",
	Long: `连接当前 profile 指向的 Dokku 主机并读取 <app> 的日志。

默认返回 dokku 的日志快照并原样展示。支持与远端 dokku logs 对齐的全部参数：
  -n, --num N     仅返回最近 N 行（需为正整数）
  -p, --ps NAME   仅显示指定进程类型（如 web、worker）的日志
  -q, --quiet     原始日志：去掉颜色、时间戳与进程名前缀
  -t, --tail      持续流式输出，直到 Ctrl-C 中断

未提供应用名、N 非正整数、或读取被 Dokku 拒绝时，提示错误并以非零退出码结束，
便于脚本判定成败。

示例用法：
  bk app logs myapp
  bk app logs myapp -n 100
  bk app logs myapp -p web
  bk app logs myapp -t
  bk app logs myapp -q -n 50 -p worker`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := appClient(profile)
		if err != nil {
			return err
		}
		defer c.Close()
		opts := dokku.LogsOptions{
			Num:     appLogsNum,
			Process: appLogsPs,
			Quiet:   appLogsQuiet,
			Tail:    appLogsTail,
		}
		return runAppLogs(cmd.Context(), cmd.OutOrStdout(), c, args[0],
			opts, cmd.Flags().Changed("num"))
	},
}

func init() {
	appLogsCmd.Flags().IntVarP(&appLogsNum, "num", "n", 0,
		"仅返回最近 N 行日志（需为正整数；默认返回 dokku 的日志快照）")
	appLogsCmd.Flags().StringVarP(&appLogsPs, "ps", "p", "",
		"仅显示指定进程类型（如 web、worker）的日志")
	appLogsCmd.Flags().BoolVarP(&appLogsQuiet, "quiet", "q", false,
		"原始日志：去掉颜色、时间戳与进程名前缀")
	appLogsCmd.Flags().BoolVarP(&appLogsTail, "tail", "t", false,
		"持续流式输出日志，直到中断")
	appCmd.AddCommand(appLogsCmd)
}
