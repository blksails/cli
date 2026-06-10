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

// app_ls.go 实现 `bk app ls`：列举 Dokku 主机上的全部应用
// （design「app_ls（R1）」/「通用执行流」；Requirement 1.1/1.2/1.3/1.4、12.1/12.2）。
//
// 边界（_Boundary: appLsCmd_）：本文件只承载 ls 子命令与其可测核心 runAppLs，
// 经 init() self-register 到既有 appCmd。复用 app.go 的连接装配 appClient、
// app_render.go 的表格渲染 appRenderAppsTable，不修改 app.go / app_render.go / internal/*。

// appLister 抽象 ls 所需的两条读取缝：解析后的应用清单（表格路径）与
// dokku 原始文本（--raw 路径）。*dokku.Client 通过其 AppsList / Run 满足该接口，
// 使 runAppLs 可注入 fake、在不触达真实 SSH/Dokku 的前提下被验证。
type appLister interface {
	AppsList(context.Context) ([]string, error)
	Run(context.Context, ...string) (string, error)
}

// runAppLs 是 ls 的可测核心。
//
// raw=true：直接以 `apps:list` 调用 Run 取 dokku 原始文本并原样写入 w，
// 不做表格化处理（Requirement 12.2）。Run 已把 dokku stderr 拼入 error，
// 出错时以 %w 透传，由命令层非零退出（Requirement 1.4/12.3）。
//
// raw=false：调用 AppsList 取已过滤标题/装饰行的应用名清单（Requirement 1.3，
// 过滤由 internal/dokku.AppsList 完成）。空清单不是错误：appRenderAppsTable 写出
// 友好提示，返回 nil 以零退出（Requirement 1.2/12.1）。非空则表格化呈现
// （Requirement 1.1/12.1）。AppsList 出错以 %w 透传（Requirement 1.4/12.3）。
func runAppLs(ctx context.Context, w io.Writer, c appLister, raw bool) error {
	if raw {
		out, err := c.Run(ctx, "apps:list")
		if err != nil {
			return fmt.Errorf("列举应用失败：%w", err)
		}
		_, err = io.WriteString(w, out)
		return err
	}

	apps, err := c.AppsList(ctx)
	if err != nil {
		return fmt.Errorf("列举应用失败：%w", err)
	}
	// 空清单与非空均由 appRenderAppsTable 处理：空→友好提示，非空→对齐表格。
	appRenderAppsTable(w, apps)
	return nil
}

// appLsCmd 是 `bk app ls`（别名 list）。装配按当前 profile 连接的 dokku.Client 后
// 委托 runAppLs；RunE 保持轻薄，列举/渲染/退出码语义均落在 runAppLs。
// --raw 取自命令组级持久标志 appRaw（app.go）。
var appLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "列出 Dokku 主机上的全部应用",
	Long: `连接当前 profile 指向的 Dokku 主机并列出全部应用名。

默认以易读的表格形式展示应用清单；当主机上没有任何应用时，
给出友好提示并以零退出码结束。使用 --raw 直接输出 dokku 的原始文本。

连接或列举失败时透传 dokku 的错误信息并以非零退出码结束，便于脚本判定成败。

示例用法：
  bk app ls
  bk app ls --raw`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := appClient(profile)
		if err != nil {
			return err
		}
		defer c.Close()
		return runAppLs(cmd.Context(), cmd.OutOrStdout(), c, appRaw)
	},
}

func init() {
	appCmd.AddCommand(appLsCmd)
}
