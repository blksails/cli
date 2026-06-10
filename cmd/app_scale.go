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

// app_scale.go 实现 `bk app scale <app> <process=count>`：
// 调整某应用某类进程的副本数（design「app_scale（R8）」/「通用执行流」；
// Requirement 8.1/8.2/8.3/8.4）。
//
// 边界（_Boundary: appScaleCmd_）：本文件只承载 scale 子命令与其可测核心
// runAppScale，经 init() self-register 到既有 appCmd。复用 app.go 的连接装配
// appClient、app_render.go 的 process=count 解析助手 appParseProcessCount，
// 不修改 app.go / app_render.go / internal/*。

// appScaler 抽象 scale 所需的唯一写入缝：调整进程副本数并返回 dokku 的结果文本。
// *dokku.Client 通过其 PsScale 满足该接口，使 runAppScale 可注入 fake、在不触达
// 真实 SSH/Dokku 的前提下被验证。
type appScaler interface {
	PsScale(context.Context, string, string, int) (string, error)
}

// runAppScale 是 scale 的可测核心。
//
// 先以共享助手 appParseProcessCount 解析 procCount：缺 '='、空进程名、空/非整数/
// 负数副本数均返回解析错误，命令层据此非零退出且不触达远端（Requirement 8.2）。
// 解析成功后以进程名与副本数调用 PsScale 调整副本数（Requirement 8.1），成功时把
// dokku 返回的结果文本原样写入 w。PsScale 已把 dokku stderr 拼入 error；当扩缩容
// 被拒绝时以 %w 透传，由命令层非零退出（Requirement 8.4）。
func runAppScale(ctx context.Context, w io.Writer, c appScaler, app, procCount string) error {
	proc, count, err := appParseProcessCount(procCount)
	if err != nil {
		return err
	}
	out, err := c.PsScale(ctx, app, proc, count)
	if err != nil {
		return fmt.Errorf("调整应用 %q 进程 %q 副本数失败：%w", app, proc, err)
	}
	_, err = io.WriteString(w, out)
	return err
}

// appScaleCmd 是 `bk app scale <app> <process=count>`。装配按当前 profile 连接的
// dokku.Client 后委托 runAppScale；RunE 保持轻薄，解析/调整/展示/退出码语义均落在
// runAppScale。
//
// 采用 cobra.ExactArgs(2)：恰需应用名 + 1 个 process=count；参数数不为 2 时由
// cobra 提示参数错误并以非零退出码结束（Requirement 8.3）。
var appScaleCmd = &cobra.Command{
	Use:   "scale <app> <process=count>",
	Short: "调整 Dokku 应用某类进程的副本数",
	Long: `连接当前 profile 指向的 Dokku 主机，将应用 <app> 指定进程的副本数调整为 <count>。

需要同时提供应用名与一个 <process>=<count>。成功后展示 dokku 返回的结果文本。
未提供应用名或扩缩容参数、参数不符合 <process>=<count> 形式或 <count> 不是非负整数、
或扩缩容被 Dokku 拒绝时，透传错误信息并以非零退出码结束，便于脚本判定成败。

示例用法：
  bk app scale myapp web=3
  bk app scale myapp worker=0`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := appClient(profile)
		if err != nil {
			return err
		}
		defer c.Close()
		return runAppScale(cmd.Context(), cmd.OutOrStdout(), c, args[0], args[1])
	},
}

func init() {
	appCmd.AddCommand(appScaleCmd)
}
