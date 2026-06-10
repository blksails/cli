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

// app_create.go 实现 `bk app create <app>`：在当前 profile 指向的 Dokku 主机上创建应用
// （design「app_create（R2）」/「通用执行流」；Requirement 2.1/2.2/2.3）。
//
// 边界（_Boundary: appCreateCmd_）：本文件只承载 create 子命令与其可测核心 runAppCreate，
// 经 init() self-register 到既有 appCmd。复用 app.go 的连接装配 appClient，
// 不修改 app.go / app_render.go / internal/*。

// appCreator 抽象 create 所需的唯一写入缝：在远端创建应用并返回 dokku 的结果文本。
// *dokku.Client 通过其 AppsCreate 满足该接口，使 runAppCreate 可注入 fake、
// 在不触达真实 SSH/Dokku 的前提下被验证。
type appCreator interface {
	AppsCreate(context.Context, string) (string, error)
}

// runAppCreate 是 create 的可测核心。
//
// 以应用名调用 AppsCreate 创建应用，成功时把 dokku 返回的结果文本原样写入 w
// 以展示创建结果（Requirement 2.1）。AppsCreate 已把 dokku stderr 拼入 error；
// 当应用已存在或创建被拒绝时以 %w 透传，由命令层非零退出（Requirement 2.3）。
func runAppCreate(ctx context.Context, w io.Writer, c appCreator, name string) error {
	out, err := c.AppsCreate(ctx, name)
	if err != nil {
		return fmt.Errorf("创建应用 %q 失败：%w", name, err)
	}
	_, err = io.WriteString(w, out)
	return err
}

// appCreateCmd 是 `bk app create <app>`。装配按当前 profile 连接的 dokku.Client 后
// 委托 runAppCreate；RunE 保持轻薄，创建/展示/退出码语义均落在 runAppCreate。
//
// 采用 cobra.ExactArgs(1)：未提供应用名（0 参数）时由 cobra 提示参数错误并以
// 非零退出码结束（Requirement 2.2）。
var appCreateCmd = &cobra.Command{
	Use:   "create <app>",
	Short: "在 Dokku 主机上创建一个新应用",
	Long: `连接当前 profile 指向的 Dokku 主机并创建名为 <app> 的应用。

创建成功后展示 dokku 返回的结果文本。未提供应用名、目标应用已存在或
创建被 Dokku 拒绝时，透传 dokku 的错误信息并以非零退出码结束，便于脚本判定成败。

示例用法：
  bk app create myapp`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := appClient(profile)
		if err != nil {
			return err
		}
		defer c.Close()
		return runAppCreate(cmd.Context(), cmd.OutOrStdout(), c, args[0])
	},
}

func init() {
	appCmd.AddCommand(appCreateCmd)
}
