/*
Copyright © 2025 BlackSails
*/
package cmd

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"pkg.blksails.net/bk/internal/hosts"
)

// host.go 实现「Dokku 主机目录」的 CLI 接线：
//   - hostsCache 缓存文件路径（与 auth.json 同目录）。
//   - newHostStore：经认证入口（schema=cli）装配 hosts.Store。
//   - fetchAndCacheHosts：登录成功后调用，拉取在线目录并写入本地缓存（best-effort）。
//   - `bk host ls`：展示当前 profile 缓存的主机目录。
//
// 在线目录只下发可公开的连接坐标（host/user/port）；私钥与本机安全选项（identity/insecure）
// 始终由本地 .bs.yaml 提供。SSH 配置解析中本地 .bs.yaml 优先，缓存目录作为未配置时的回退
// （见 ssh_config.go）。

// hostsCache 在 init 中被设为 ~/.local/bk/hosts.json（与 authConfig 同目录）。
var hostsCache string

func init() {
	if home, err := os.UserHomeDir(); err == nil {
		hostsCache = home + "/.local/bk/hosts.json"
	}
}

// newHostStore 经认证入口取 client（schema=cli）装配 hosts.Store。
func newHostStore(profile string) (*hosts.Store, error) {
	client, err := AuthedClientSchema(profile, cliSchema)
	if err != nil {
		return nil, err
	}
	return hosts.NewStore(client), nil
}

// fetchAndCacheHosts 拉取在线主机目录并写入本机缓存。供登录成功后调用。
// 返回拉取到的条数与可能的错误；调用方按需决定是否致命（登录路径里做 best-effort）。
func fetchAndCacheHosts(profile string) (int, error) {
	store, err := newHostStore(profile)
	if err != nil {
		return 0, err
	}
	list, err := store.List()
	if err != nil {
		return 0, err
	}
	if err := hosts.Save(hostsCache, profile, list); err != nil {
		return 0, err
	}
	return len(list), nil
}

// hostCmd 是 `bk host` 命令族。
var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "Dokku 主机目录（登录后自动同步，供 SSH 连接自动取用）",
}

// hostLsCmd 展示当前 profile 缓存的主机目录。--sync 时先从在线刷新缓存再展示。
var hostLsSync bool

var hostLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "列出已缓存的主机目录（--sync 先从线上刷新）",
	RunE: func(cmd *cobra.Command, args []string) error {
		if hostLsSync {
			n, err := fetchAndCacheHosts(profile)
			if err != nil {
				return fmt.Errorf("从线上同步主机目录失败：%w", err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "已同步 %d 条主机记录到本地缓存。\n", n)
		}
		list, err := hosts.Load(hostsCache, profile)
		if err != nil {
			return err
		}
		return renderHostList(cmd.OutOrStdout(), list)
	},
}

// renderHostList 以表格输出主机目录；空目录给出友好提示。
func renderHostList(w io.Writer, list []hosts.Host) error {
	if len(list) == 0 {
		fmt.Fprintln(w, "暂无缓存的主机目录。登录后会自动同步，或运行 `bk host ls --sync` 手动拉取。")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tHOST\tUSER\tPORT\tDEFAULT\tDESCRIPTION")
	for _, h := range list {
		user := h.SSHUser
		if user == "" {
			user = "dokku"
		}
		port := h.SSHPort
		if port == 0 {
			port = 22
		}
		def := ""
		if h.IsDefault {
			def = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", h.Name, h.Host, user, port, def, h.Description)
	}
	return tw.Flush()
}

func init() {
	hostLsCmd.Flags().BoolVar(&hostLsSync, "sync", false, "先从线上刷新主机目录缓存再展示")
	hostCmd.AddCommand(hostLsCmd)
	rootCmd.AddCommand(hostCmd)
}
