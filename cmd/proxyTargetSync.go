/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"bytes"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"pkg.blksails.net/bk/internal/proxytarget"
	"pkg.blksails.net/bk/internal/sshx"
)

// proxyTargetSync.go：`bk proxy target sync`（管理员）——把 Supabase cli.proxy_targets 的
// 转发目标渲染进 hub 主机上的 config.yaml（保留其余字段），并 `dokku <plugin>:restart <service>`
// 使其生效。Supabase 为唯一真源，sync 单向覆盖 hub config 的 forward_targets。
//
// 连接：经 SSHConfig(profile) 接入 hub 主机，需 **root**（要读写 /var/lib/dokku/... 与跑 dokku）。
// 即用 user:root 的 profile/配置运行（如 `--config ~/.bs.admin.yaml`）。dokku 用户（force command）
// 无法 cat/写文件，会以「is not a dokku command」失败。
//
// 配置键（proxy.* of .bs.yaml，均有默认）：
//   - proxy.hub_plugin       dokku 插件名（默认 proxyhub）
//   - proxy.hub_service      实例名（默认 proxyhub1）
//   - proxy.hub_config_path  直接指定 config.yaml 路径（默认按 plugin/service 推导）

func hubConfigLocation() (plugin, service, cfgPath string) {
	plugin = viperOrDefault("proxy.hub_plugin", "proxyhub")
	service = viperOrDefault("proxy.hub_service", "proxyhub1")
	cfgPath = viper.GetString("proxy.hub_config_path")
	if cfgPath == "" {
		cfgPath = fmt.Sprintf("/var/lib/dokku/services/%s/%s/config.yaml", plugin, service)
	}
	return
}

func viperOrDefault(key, def string) string {
	if v := viper.GetString(key); v != "" {
		return v
	}
	return def
}

var proxyTargetSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "（管理员）把转发目标渲染进 hub config.yaml 并重启 hub 使其生效",
	Long: `读取 Supabase 中的全部转发目标，按 app_id 分组渲染进 hub 主机的 config.yaml
（仅替换各 app 的 forward_targets，保留 mirror app 与其它字段），随后重启 hub 使其生效。

需以 root 接入 hub 主机（读写 /var/lib/dokku/... 与执行 dokku 命令），即用 user:root 的
配置运行，例如：

  bk proxy target sync --config ~/.bs.admin.yaml

写入前会备份 config.yaml（同目录 .bak-<时间戳>）；无变更时不重启。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runProxyTargetSync(cmd)
	},
}

func runProxyTargetSync(cmd *cobra.Command) error {
	w := cmd.OutOrStdout()
	ctx := cmd.Context()

	// 1) 读 Supabase 全部目标，按 app_id 分组。
	store, err := newProxyTargetStore(profile)
	if err != nil {
		return err
	}
	targets, err := store.List("")
	if err != nil {
		return fmt.Errorf("读取转发目标失败：%w", err)
	}
	byApp := map[string][]string{}
	for _, t := range targets {
		byApp[t.AppID] = append(byApp[t.AppID], t.Target)
	}

	// 2) 连接 hub 主机（需 root）。
	sshCfg, err := SSHConfig(profile)
	if err != nil {
		return fmt.Errorf("SSH 配置无效：%w", err)
	}
	plugin, service, cfgPath := hubConfigLocation()

	conn, err := sshx.Dial(sshCfg)
	if err != nil {
		return fmt.Errorf("连接 hub 主机失败（sync 需 root SSH，建议 --config 指向 user:root 的配置）：%w", err)
	}
	defer conn.Close()

	// 3) 读现有 config.yaml。
	res, err := conn.RunArgs(ctx, "cat", cfgPath)
	if err != nil {
		return fmt.Errorf("读取 hub config.yaml 失败（%s；确认以 root 接入且实例存在）：%w", cfgPath, err)
	}
	current := []byte(res.Stdout)

	// 4) 渲染（仅改 forward_targets，保留其余字段）。
	rendered, warnings, changed, err := proxytarget.RenderForwardTargets(current, byApp)
	if err != nil {
		return err
	}
	for _, app := range warnings {
		fmt.Fprintf(w, "⚠ 目标含 app %q，但 hub config.yaml 无此 app，已跳过（请先在 hub 配置里建该 forwarder app）\n", app)
	}

	if !changed {
		fmt.Fprintln(w, "hub 转发目标无变更，未写入也未重启。")
		return nil
	}

	// 5) 备份 + 写入。
	backup := cfgPath + ".bak-sync"
	if _, err := conn.RunArgs(ctx, "cp", "-a", cfgPath, backup); err != nil {
		return fmt.Errorf("备份 hub config.yaml 失败：%w", err)
	}
	if _, err := conn.RunArgsStdin(ctx, bytes.NewReader(rendered), "tee", cfgPath); err != nil {
		return fmt.Errorf("写入 hub config.yaml 失败（备份在 %s）：%w", backup, err)
	}

	// 6) 重启 hub 使配置生效。
	if _, err := conn.RunArgs(ctx, "dokku", plugin+":restart", service); err != nil {
		return fmt.Errorf("重启 hub 失败（config 已写入，备份在 %s，可手动恢复）：%w", backup, err)
	}

	fmt.Fprintf(w, "已同步 %d 个转发目标到 hub（%s/%s）并重启生效（备份 %s）。\n",
		len(targets), plugin, service, backup)
	return nil
}

func init() {
	proxyTargetCmd.AddCommand(proxyTargetSyncCmd)
}
