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

// app_config_set.go 实现 `bk app config:set <app> KEY=VALUE [KEY=VALUE...]`：
// 批量设置应用环境变量（design「app_config_set（R5）」/「通用执行流」；
// Requirement 5.1/5.2/5.3/5.4/5.5/5.6）。
//
// 边界（_Boundary: appConfigSetCmd_）：本文件只承载 config:set 子命令与其可测核心
// runAppConfigSet，经 init() self-register 到既有 appCmd。复用 app.go 的连接装配
// appClient、app_render.go 的 KV 解析助手 appParseKeyValues，
// 不修改 app.go / app_render.go / internal/*。

// appConfigSetter 抽象 config:set 所需的唯一写入缝：批量设置环境变量并返回
// dokku 的结果文本。*dokku.Client 通过其 ConfigSet 满足该接口，使 runAppConfigSet
// 可注入 fake、在不触达真实 SSH/Dokku 的前提下被验证。
type appConfigSetter interface {
	ConfigSet(context.Context, string, map[string]string, bool) (string, error)
}

// runAppConfigSet 是 config:set 的可测核心。
//
// 先以共享助手 appParseKeyValues 解析 pairs：缺项（空列表）或任一项格式非法
// （不含 '=' / 键为空）时返回解析错误，命令层据此非零退出且不触达远端
// （Requirement 5.2/5.3）。解析成功后以解析得到的映射与 noRestart 调用 ConfigSet
// 设置变量（Requirement 5.1/5.4），成功时把 dokku 返回的结果文本原样写入 w。
// ConfigSet 已把 dokku stderr 拼入 error；当设置被拒绝时以 %w 透传，由命令层
// 非零退出（Requirement 5.6）。
func runAppConfigSet(ctx context.Context, w io.Writer, c appConfigSetter, app string, pairs []string, noRestart bool) error {
	kv, err := appParseKeyValues(pairs)
	if err != nil {
		return err
	}
	out, err := c.ConfigSet(ctx, app, kv, noRestart)
	if err != nil {
		return fmt.Errorf("设置应用 %q 环境变量失败：%w", app, err)
	}
	_, err = io.WriteString(w, out)
	return err
}

// appConfigSetNoRestart 承载 config:set 的本地 --no-restart 标志；透传到 ConfigSet。
var appConfigSetNoRestart bool

// appConfigSetCmd 是 `bk app config:set <app> KEY=VALUE [KEY=VALUE...]`。装配按当前
// profile 连接的 dokku.Client 后委托 runAppConfigSet；RunE 保持轻薄，解析/设置/
// 展示/退出码语义均落在 runAppConfigSet。
//
// 采用 cobra.MinimumNArgs(2)：至少需应用名 + 1 个 KEY=VALUE；不足时由 cobra 提示
// 参数错误并以非零退出码结束（Requirement 5.2/5.5）。--no-restart 透传到客户端
// 以不触发应用重启的方式设置（Requirement 5.4）。
var appConfigSetCmd = &cobra.Command{
	Use:   "config:set <app> KEY=VALUE [KEY=VALUE...]",
	Short: "批量设置 Dokku 应用的环境变量",
	Long: `连接当前 profile 指向的 Dokku 主机，将一个或多个 KEY=VALUE 设置到应用 <app>。

至少需要提供应用名与一个 KEY=VALUE。成功后展示 dokku 返回的结果文本。
未提供应用名或任何配置项、某个参数不符合 KEY=VALUE 形式、或设置被 Dokku 拒绝时，
透传错误信息并以非零退出码结束，便于脚本判定成败。

使用 --no-restart 以不触发应用重启的方式设置环境变量。

示例用法：
  bk app config:set myapp KEY=value
  bk app config:set myapp A=1 B=2 --no-restart`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := appClient(profile)
		if err != nil {
			return err
		}
		defer c.Close()
		return runAppConfigSet(cmd.Context(), cmd.OutOrStdout(), c, args[0], args[1:], appConfigSetNoRestart)
	},
}

func init() {
	appConfigSetCmd.Flags().BoolVar(&appConfigSetNoRestart, "no-restart", false, "以不触发应用重启的方式设置环境变量")
	appCmd.AddCommand(appConfigSetCmd)
}
