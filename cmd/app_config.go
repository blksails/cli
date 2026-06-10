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

// app_config.go 实现 `bk app config <app>`：查看单个应用的全部环境变量
// （design「app_config（R4）」/「通用执行流」；Requirement 4.1/4.2/4.3/4.4、12.1/12.2）。
//
// 边界（_Boundary: appConfigCmd_）：本文件只承载 config 子命令与其可测核心 runAppConfig，
// 经 init() self-register 到既有 appCmd。复用 app.go 的连接装配 appClient、
// app_render.go 的表格渲染 appRenderConfigTable，不修改 app.go / app_render.go / internal/*。

// appConfigReader 抽象 config 所需的两条读取缝：解析后的环境变量映射（表格路径）与
// dokku 原始文本（--raw 路径）。*dokku.Client 通过其 ConfigGet / Run 满足该接口，
// 使 runAppConfig 可注入 fake、在不触达真实 SSH/Dokku 的前提下被验证。
type appConfigReader interface {
	ConfigGet(context.Context, string) (map[string]string, error)
	Run(context.Context, ...string) (string, error)
}

// runAppConfig 是 config 的可测核心。
//
// raw=true：以 `config:show <app>` 调用 Run 取 dokku 原始文本并原样写入 w，
// 不做表格化处理（Requirement 12.2）。该子命令与 dokku.ConfigGet 内部所用一致，
// 保证 raw 与表格视图取自同一数据源。Run 已把 dokku stderr 拼入 error，
// 出错时以 %w 透传，由命令层非零退出（Requirement 4.4/12.3）。
//
// raw=false：调用 ConfigGet 取已解析的环境变量映射（Requirement 4.1）。空映射不是
// 错误：appRenderConfigTable 写出友好提示，返回 nil 以零退出（Requirement 4.2/12.1）。
// 非空则表格化呈现（Requirement 4.1/12.1）。ConfigGet 出错以 %w 透传
// （Requirement 4.4/12.3）。
func runAppConfig(ctx context.Context, w io.Writer, c appConfigReader, app string, raw bool) error {
	if raw {
		out, err := c.Run(ctx, "config:show", app)
		if err != nil {
			return fmt.Errorf("读取应用配置失败：%w", err)
		}
		_, err = io.WriteString(w, out)
		return err
	}

	env, err := c.ConfigGet(ctx, app)
	if err != nil {
		return fmt.Errorf("读取应用配置失败：%w", err)
	}
	// 空映射与非空均由 appRenderConfigTable 处理：空→友好提示，非空→对齐 KEY/VALUE 表格。
	appRenderConfigTable(w, env)
	return nil
}

// appConfigCmd 是 `bk app config <app>`。装配按当前 profile 连接的 dokku.Client 后
// 委托 runAppConfig；RunE 保持轻薄，读取/渲染/退出码语义均落在 runAppConfig。
// 缺少应用名参数由 cobra.ExactArgs(1) 拦截并非零退出（Requirement 4.3）。
// --raw 取自命令组级持久标志 appRaw（app.go）。
var appConfigCmd = &cobra.Command{
	Use:   "config <app>",
	Short: "查看指定应用的全部环境变量",
	Long: `连接当前 profile 指向的 Dokku 主机并以键值对表格展示指定应用的全部环境变量。

默认以易读的 KEY/VALUE 表格形式展示；当该应用没有任何环境变量时，
给出友好提示并以零退出码结束。使用 --raw 直接输出 dokku 的原始文本。

未提供应用名、目标应用不存在或读取被 Dokku 拒绝时，透传 dokku 的错误信息并以
非零退出码结束，便于脚本判定成败。

示例用法：
  bk app config myapp
  bk app config myapp --raw`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		c, err := appClient(profile)
		if err != nil {
			return err
		}
		defer c.Close()
		return runAppConfig(cmd.Context(), cmd.OutOrStdout(), c, name, appRaw)
	},
}

func init() {
	appCmd.AddCommand(appConfigCmd)
}
