/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// app_logs.go 实现 `bk app logs <app>`：在当前 profile 指向的 Dokku 主机上
// 读取并展示应用的日志快照，并提供 `-n <N>` 限制行数选项
// （design「app_logs（R10）」/「通用执行流」；Requirement 10.1/10.2/10.3/10.4/10.5）。
//
// 边界（_Boundary: appLogsCmd_）：本文件只承载 logs 子命令与其可测核心 runAppLogs，
// 经 init() self-register 到既有 appCmd。复用 app.go 的连接装配 appClient，
// 不修改 app.go / app_render.go / internal/*。

// appLogReader 抽象 logs 所需的唯一读取缝：返回 dokku 的日志快照原文。
// *dokku.Client 通过其 Logs 满足该接口，使 runAppLogs 可注入 fake、
// 在不触达真实 SSH/Dokku 的前提下被验证。
type appLogReader interface {
	Logs(context.Context, string, int) (string, error)
}

// runAppLogs 是 logs 的可测核心。
//
// 行数语义与 dokku.Client.Logs 一致：num>0 时限制为最近 num 行（Requirement 10.2），
// num<=0（默认 numSet=false 传入 0）时不加 --num，返回默认日志快照（Requirement 10.1）。
//
// 校验：当用户显式提供 `-n`（numSet=true）但值非正（num<=0）时，给出可读提示并直接返回错误，
// 在调用远端前于核心层拦截、不触发 Logs（Requirement 10.3，由命令层非零退出）。
//
// 成功时把 dokku 返回的日志文本原样写入 w（默认透传，不做加工）。Logs 已把 dokku stderr
// 拼入 error；读取被拒绝（如应用不存在）时以 %w 透传，由命令层非零退出（Requirement 10.5）。
func runAppLogs(ctx context.Context, w io.Writer, c appLogReader, app string, num int, numSet bool) error {
	if numSet && num <= 0 {
		return fmt.Errorf("行数需为正整数，得到 %d", num)
	}
	out, err := c.Logs(ctx, app, num)
	if err != nil {
		return fmt.Errorf("读取应用 %q 日志失败：%w", app, err)
	}
	_, err = io.WriteString(w, out)
	return err
}

// appLogsNum 承载本命令的局部 `-n`/`--num` 行数选项。默认 0 表示“无 -n 限制 /
// 默认快照”，与 dokku.Client.Logs 的 num<=0 语义一致。是否显式设置由
// cmd.Flags().Changed("num") 判定，从而区分默认快照与 -n 0 的非法输入。
var appLogsNum int

// appLogsCmd 是 `bk app logs <app>`。装配按当前 profile 连接的 dokku.Client 后
// 委托 runAppLogs；RunE 保持轻薄，读取/校验/展示/退出码语义均落在 runAppLogs。
//
// 采用 cobra.ExactArgs(1)：未提供应用名（0 参数）时由 cobra 提示参数错误并以
// 非零退出码结束（Requirement 10.4）。
var appLogsCmd = &cobra.Command{
	Use:   "logs <app>",
	Short: "查看 Dokku 应用的日志快照",
	Long: `连接当前 profile 指向的 Dokku 主机并读取 <app> 的日志快照。

默认返回 dokku 的日志快照并原样展示；提供 -n <N> 时仅返回最近 N 行。
未提供应用名、N 非正整数、或读取被 Dokku 拒绝时，提示错误并以非零退出码结束，
便于脚本判定成败。

示例用法：
  bk app logs myapp
  bk app logs myapp -n 100`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := appClient(profile)
		if err != nil {
			return err
		}
		defer c.Close()
		return runAppLogs(cmd.Context(), cmd.OutOrStdout(), c, args[0],
			appLogsNum, cmd.Flags().Changed("num"))
	},
}

func init() {
	appLogsCmd.Flags().IntVarP(&appLogsNum, "num", "n", 0,
		"仅返回最近 N 行日志（需为正整数；默认返回 dokku 的日志快照）")
	appCmd.AddCommand(appLogsCmd)
}
