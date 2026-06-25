/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"pkg.blksails.net/bk/internal/proxytarget"
)

// proxyTarget.go：`bk proxy target` 命令族——把 yproxy hub 的 forward_targets allowlist
// 中心化到 Supabase cli.proxy_targets（任意已认证用户可 ls；仅管理员可 add/rm），
// 再由 `bk proxy target sync`（见 proxyTargetSync.go）渲染进 hub config.yaml 并重启 hub。

var (
	proxyTargetApp  string // --app：ls/rm 的 app_id 过滤
	proxyTargetNote string // --note：add 的备注
)

// newProxyTargetStore 经认证入口取 cli-schema client，装配 proxytarget.Store（与 newSSHKeyStore 同构）。
func newProxyTargetStore(profile string) (*proxytarget.Store, error) {
	client, err := AuthedClientSchema(profile, cliSchema)
	if err != nil {
		return nil, err
	}
	return proxytarget.NewStore(client), nil
}

var proxyTargetCmd = &cobra.Command{
	Use:   "target",
	Short: "管理 proxy 转发目标 allowlist（中心化于 Supabase，sync 后由 hub 生效）",
	Long: `proxy 转发目标 allowlist 的中心化管理：

- 普通用户：bk proxy target ls [--app <id>]   查看放行清单
- 管理员：  bk proxy target add <app> <host:port> [--note ...]
            bk proxy target rm  <host:port|id> [--app <id>]
            bk proxy target sync                把清单渲染进 hub config.yaml 并重启 hub

allowlist 是 hub 侧防 SSRF 的安全控制：仅 hub 配置内的目标可经隧道转发。
本组命令只改 Supabase 真源，需 sync 后 hub 才生效（hub 无热重载，sync 会重启）。`,
}

var proxyTargetLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "列出转发目标（任意已认证用户）",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newProxyTargetStore(profile)
		if err != nil {
			return err
		}
		targets, err := store.List(proxyTargetApp)
		if err != nil {
			if errors.Is(err, proxytarget.ErrPermission) {
				return fmt.Errorf("列出转发目标失败：%w", err)
			}
			return fmt.Errorf("列出转发目标失败：%w", err)
		}
		if len(targets) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "暂无转发目标，管理员可用 bk proxy target add <app> <host:port> 添加")
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "App\tTarget\tNote")
		for _, t := range targets {
			fmt.Fprintf(w, "%s\t%s\t%s\n", t.AppID, t.Target, t.Note)
		}
		return w.Flush()
	},
}

var proxyTargetAddCmd = &cobra.Command{
	Use:   "add <app> <host:port>",
	Short: "（管理员）新增一个转发目标到 allowlist",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newProxyTargetStore(profile)
		if err != nil {
			return err
		}
		rec, err := store.Add(args[0], args[1], proxyTargetNote)
		if err != nil {
			if errors.Is(err, proxytarget.ErrPermission) {
				return fmt.Errorf("需要管理员权限才能新增转发目标：%w", err)
			}
			return fmt.Errorf("新增转发目标失败：%w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "已添加：app=%s target=%s（运行 bk proxy target sync 使 hub 生效）\n", rec.AppID, rec.Target)
		return nil
	},
}

var proxyTargetRmCmd = &cobra.Command{
	Use:   "rm <host:port|id>",
	Short: "（管理员）从 allowlist 移除一个转发目标",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newProxyTargetStore(profile)
		if err != nil {
			return err
		}
		if err := store.Remove(proxyTargetApp, args[0]); err != nil {
			if errors.Is(err, proxytarget.ErrNotFound) {
				return fmt.Errorf("未找到该转发目标（或无管理员权限）：%w", err)
			}
			if errors.Is(err, proxytarget.ErrPermission) {
				return fmt.Errorf("需要管理员权限才能移除转发目标：%w", err)
			}
			return fmt.Errorf("移除转发目标失败：%w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "已移除：%s（运行 bk proxy target sync 使 hub 生效）\n", args[0])
		return nil
	},
}

func init() {
	proxyTargetLsCmd.Flags().StringVar(&proxyTargetApp, "app", "", "按 app_id 过滤")
	proxyTargetAddCmd.Flags().StringVar(&proxyTargetNote, "note", "", "备注")
	proxyTargetRmCmd.Flags().StringVar(&proxyTargetApp, "app", "", "限定 app_id（按 target 删除时避免误删同名）")

	proxyTargetCmd.AddCommand(proxyTargetLsCmd)
	proxyTargetCmd.AddCommand(proxyTargetAddCmd)
	proxyTargetCmd.AddCommand(proxyTargetRmCmd)
	// sync 子命令在 proxyTargetSync.go 的 init 中注册
	proxyCmd.AddCommand(proxyTargetCmd)
}
