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

// app_config_unset.go 实现 `bk app config:unset <app> KEY [KEY...]`：
// 删除应用的一个或多个环境变量键（design「app_config_unset」/「通用执行流」；
// Requirement 6.1/6.2/6.3/6.4）。
//
// 边界（_Boundary: appConfigUnsetCmd_）：本文件只承载 config:unset 子命令与其可测核心
// runAppConfigUnset，经 init() self-register 到既有 appCmd。复用 app.go 的连接装配
// appClient，不修改 app.go / app_render.go / internal/*。

// appConfigUnsetter 抽象 config:unset 所需的唯一写入缝：删除指定键并返回 dokku 的
// 结果文本。*dokku.Client 通过其 ConfigUnset 满足该接口，使 runAppConfigUnset
// 可注入 fake、在不触达真实 SSH/Dokku 的前提下被验证。
type appConfigUnsetter interface {
	ConfigUnset(context.Context, string, ...string) (string, error)
}

// runAppConfigUnset 是 config:unset 的可测核心。
//
// 以应用名与待删除键调用 ConfigUnset 删除变量（Requirement 6.1），成功时把 dokku
// 返回的结果文本原样写入 w 以展示删除结果。ConfigUnset 已把 dokku stderr 拼入
// error；当删除被拒绝时以 %w 透传，由命令层非零退出（Requirement 6.4）。
func runAppConfigUnset(ctx context.Context, w io.Writer, c appConfigUnsetter, app string, keys []string) error {
	out, err := c.ConfigUnset(ctx, app, keys...)
	if err != nil {
		return fmt.Errorf("删除应用 %q 环境变量失败：%w", app, err)
	}
	_, err = io.WriteString(w, out)
	return err
}

// appConfigUnsetCmd 是 `bk app config:unset <app> KEY [KEY...]`。装配按当前 profile
// 连接的 dokku.Client 后委托 runAppConfigUnset；RunE 保持轻薄，删除/展示/退出码
// 语义均落在 runAppConfigUnset。
//
// 采用 cobra.MinimumNArgs(2)：至少需应用名 + 1 个待删除键；不足时由 cobra 提示参数
// 错误并以非零退出码结束（Requirement 6.2/6.3）。
var appConfigUnsetCmd = &cobra.Command{
	Use:   "config:unset <app> KEY [KEY...]",
	Short: "删除 Dokku 应用的一个或多个环境变量",
	Long: `连接当前 profile 指向的 Dokku 主机，从应用 <app> 删除一个或多个环境变量键。

至少需要提供应用名与一个待删除的键。成功后展示 dokku 返回的结果文本。
未提供应用名或任何待删除键、或删除被 Dokku 拒绝时，透传错误信息并以非零退出码结束，
便于脚本判定成败。

示例用法：
  bk app config:unset myapp KEY
  bk app config:unset myapp A B`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := appClient(profile)
		if err != nil {
			return err
		}
		defer c.Close()
		return runAppConfigUnset(cmd.Context(), cmd.OutOrStdout(), c, args[0], args[1:])
	},
}

func init() {
	appCmd.AddCommand(appConfigUnsetCmd)
}
