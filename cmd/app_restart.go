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

// app_restart.go 实现 `bk app restart <app>`：在当前 profile 指向的 Dokku 主机上重启应用
// （design「app_restart（R9）」/「通用执行流」；Requirement 9.1/9.2/9.3）。
//
// 边界（_Boundary: appRestartCmd_）：本文件只承载 restart 子命令与其可测核心 runAppRestart，
// 经 init() self-register 到既有 appCmd。复用 app.go 的连接装配 appClient，
// 不修改 app.go / app_render.go / internal/*。

// appRestarter 抽象 restart 所需的唯一执行缝：在远端重启应用并返回 dokku 的结果文本。
// *dokku.Client 通过其 PsRestart 满足该接口，使 runAppRestart 可注入 fake、
// 在不触达真实 SSH/Dokku 的前提下被验证。
type appRestarter interface {
	PsRestart(context.Context, string) (string, error)
}

// runAppRestart 是 restart 的可测核心。
//
// 以应用名调用 PsRestart 重启应用，成功时把 dokku 返回的结果文本原样写入 w
// 以展示重启结果（Requirement 9.1）。PsRestart 已把 dokku stderr 拼入 error；
// 当应用不存在或重启被拒绝时以 %w 透传，由命令层非零退出（Requirement 9.3）。
func runAppRestart(ctx context.Context, w io.Writer, c appRestarter, app string) error {
	out, err := c.PsRestart(ctx, app)
	if err != nil {
		return fmt.Errorf("重启应用 %q 失败：%w", app, err)
	}
	_, err = io.WriteString(w, out)
	return err
}

// appRestartCmd 是 `bk app restart <app>`。装配按当前 profile 连接的 dokku.Client 后
// 委托 runAppRestart；RunE 保持轻薄，重启/展示/退出码语义均落在 runAppRestart。
//
// 采用 cobra.ExactArgs(1)：未提供应用名（0 参数）时由 cobra 提示参数错误并以
// 非零退出码结束（Requirement 9.2）。
var appRestartCmd = &cobra.Command{
	Use:   "restart <app>",
	Short: "重启 Dokku 应用",
	Long: `连接当前 profile 指向的 Dokku 主机并重启名为 <app> 的应用。

重启成功后展示 dokku 返回的结果文本。未提供应用名、目标应用不存在或
重启被 Dokku 拒绝时，透传 dokku 的错误信息并以非零退出码结束，便于脚本判定成败。

示例用法：
  bk app restart myapp`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := appClient(profile)
		if err != nil {
			return err
		}
		defer c.Close()
		return runAppRestart(cmd.Context(), cmd.OutOrStdout(), c, args[0])
	},
}

func init() {
	appCmd.AddCommand(appRestartCmd)
}
