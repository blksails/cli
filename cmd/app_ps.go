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

// app_ps.go 实现 `bk app ps <app>`：在当前 profile 指向的 Dokku 主机上查询并展示
// 应用的进程运行状态（design「app_ps（R7，原文）」/「通用执行流」；Requirement 7.1/7.2/7.3）。
//
// 边界（_Boundary: appPsCmd_）：本文件只承载 ps 子命令与其可测核心 runAppPs，
// 经 init() self-register 到既有 appCmd。复用 app.go 的连接装配 appClient，
// 不修改 app.go / app_render.go / internal/*。

// appPsReader 抽象 ps 所需的唯一读取缝：返回 dokku 的进程状态原文。
// *dokku.Client 通过其 Ps 满足该接口，使 runAppPs 可注入 fake、
// 在不触达真实 SSH/Dokku 的前提下被验证。
type appPsReader interface {
	Ps(context.Context, string) (string, error)
}

// runAppPs 是 ps 的可测核心。
//
// 以应用名调用 Ps，成功时把 dokku 返回的进程状态文本原样写入 w（Requirement 7.1，
// 「原文」展示，不做表格化处理）。Ps 已把 dokku stderr 拼入 error；当应用不存在或
// 查询被拒绝时以 %w 透传，由命令层非零退出（Requirement 7.3）。
func runAppPs(ctx context.Context, w io.Writer, c appPsReader, app string) error {
	out, err := c.Ps(ctx, app)
	if err != nil {
		return fmt.Errorf("查询应用 %q 进程状态失败：%w", app, err)
	}
	_, err = io.WriteString(w, out)
	return err
}

// appPsCmd 是 `bk app ps <app>`。装配按当前 profile 连接的 dokku.Client 后
// 委托 runAppPs；RunE 保持轻薄，查询/展示/退出码语义均落在 runAppPs。
//
// 采用 cobra.ExactArgs(1)：未提供应用名（0 参数）时由 cobra 提示参数错误并以
// 非零退出码结束（Requirement 7.2）。
var appPsCmd = &cobra.Command{
	Use:   "ps <app>",
	Short: "查看 Dokku 应用的进程运行状态",
	Long: `连接当前 profile 指向的 Dokku 主机并查询 <app> 的进程运行状态。

查询成功后原样展示 dokku 返回的进程状态文本。未提供应用名、目标应用不存在或
查询被 Dokku 拒绝时，透传 dokku 的错误信息并以非零退出码结束，便于脚本判定成败。

示例用法：
  bk app ps myapp`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := appClient(profile)
		if err != nil {
			return err
		}
		defer c.Close()
		return runAppPs(cmd.Context(), cmd.OutOrStdout(), c, args[0])
	},
}

func init() {
	appCmd.AddCommand(appPsCmd)
}
