/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"pkg.blksails.net/bk/internal/proxyhub"
)

// proxyHub.go：「proxy hub 目录」的 CLI 接线——登录后自动拉取 cli.proxy_hub 缓存到本地，
// 使 `bk proxy forward` 在未配 proxy.* 时回退到该目录（见 proxy.go resolveHubConfig），
// 实现「登录即用」。与「主机目录」（host.go）同构。
//
// 缓存含 token，文件 0600；默认 hub 的证书额外物化到 proxyHubCAPath 供 TLS 校验。

var (
	proxyHubCache  string // ~/.local/bk/proxyhub.json（hub 目录缓存，含 token）
	proxyHubCAPath string // ~/.local/bk/proxyhub-ca.crt（默认 hub 证书，回退时作 --ca）
)

func init() {
	if home, err := os.UserHomeDir(); err == nil {
		proxyHubCache = home + "/.local/bk/proxyhub.json"
		proxyHubCAPath = home + "/.local/bk/proxyhub-ca.crt"
	}
}

func newProxyHubStore(profile string) (*proxyhub.Store, error) {
	client, err := AuthedClientSchema(profile, cliSchema)
	if err != nil {
		return nil, err
	}
	return proxyhub.NewStore(client), nil
}

// fetchAndCacheProxyHub 登录后拉取 hub 目录并缓存；并把默认 hub 的证书写到 proxyHubCAPath
// 供 resolveHubConfig 回退作 --ca。best-effort：失败不影响登录。
func fetchAndCacheProxyHub(profile string) (int, error) {
	store, err := newProxyHubStore(profile)
	if err != nil {
		return 0, err
	}
	list, err := store.List()
	if err != nil {
		return 0, err
	}
	if err := proxyhub.Save(proxyHubCache, profile, list); err != nil {
		return 0, err
	}
	if h, perr := proxyhub.Pick(list, ""); perr == nil && h.CACert != "" && proxyHubCAPath != "" {
		_ = os.WriteFile(proxyHubCAPath, []byte(h.CACert), 0o600)
	}
	return len(list), nil
}

var proxyHubCmd = &cobra.Command{
	Use:   "hub",
	Short: "查看/同步「登录即用」的 proxy hub 目录",
}

var proxyHubLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "列出缓存的 proxy hub 目录",
	RunE: func(cmd *cobra.Command, args []string) error {
		if proxyHubSync {
			if _, err := fetchAndCacheProxyHub(profile); err != nil {
				return fmt.Errorf("同步 hub 目录失败：%w", err)
			}
		}
		list, err := proxyhub.Load(proxyHubCache, profile)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "无缓存的 hub 目录；先 bk auth login（或加 --sync 立即拉取）")
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "Name\tServer\tApp\tDefault")
		for _, h := range list {
			def := ""
			if h.IsDefault {
				def = "*"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", h.Name, h.Server, h.App, def)
		}
		return w.Flush()
	},
}

var proxyHubSync bool

func init() {
	proxyHubLsCmd.Flags().BoolVar(&proxyHubSync, "sync", false, "先从 Supabase 拉取最新目录再展示")
	proxyHubCmd.AddCommand(proxyHubLsCmd)
	proxyCmd.AddCommand(proxyHubCmd)
}
