/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// init.go 实现 `bk init`：新用户一键初始化。把登录后零散的开箱步骤收敛为一条命令：
//   1. 校验已登录；
//   2. 同步在线主机目录与 proxy hub 目录到本地缓存（bk app / bk proxy 登录即用）；
//   3. 设置 ssh.insecure=true（首次连接信任主机，避免 known_hosts 缺失报错）；
//   4. 生成并登记 SSH 密钥对（私钥不离机，--set-identity 写回 ssh.identity），等待管理员代装；
//   5. 打印后续步骤。
//
// 幂等：已配 ssh.identity 则跳过密钥生成；可 --no-provision 跳过。host 经 SSHConfig 解析
// （会回退到主机目录），故新用户即便没在 .bs.yaml 配 ssh.host 也能 provision。

var initNoProvision bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "一键初始化新用户环境（同步目录 + 首次连接配置 + 生成 SSH 密钥）",
	Long: `新用户开箱即用：在 bk auth login 之后运行一次，自动完成剩余配置。

  bk auth login <你的邮箱>   # 先登录（零配置：Supabase 端点/密钥已内置）
  bk init                    # 同步目录 + 写首次连接配置 + 生成并登记 SSH 密钥

完成后：
  · bk app ls / bk proxy forward 登录即用（主机与 hub 坐标来自在线目录）；
  · 你的公钥已登记为 pending，待管理员 bk ssh-key install 代装后即可 git push 部署。`,
	RunE: runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	w := cmd.OutOrStdout()
	v := viper.GetViper()

	// 1) 登录校验。
	c := lookupProfile(authConfig, profile)
	if c == nil || c.Session.User.ID == "" {
		return fmt.Errorf("尚未登录，请先运行 `bk auth login <你的邮箱>`")
	}
	if email, err := sessionEmail(authConfig, profile); err == nil {
		fmt.Fprintf(w, "✔ 已登录：%s\n", email)
	} else {
		fmt.Fprintln(w, "✔ 已登录")
	}

	// 2) 同步在线目录到本地缓存（best-effort）。
	if n, err := fetchAndCacheHosts(profile); err == nil {
		fmt.Fprintf(w, "✔ 主机目录已同步（%d 条）\n", n)
	} else {
		fmt.Fprintf(w, "… 主机目录同步失败（稍后可 `bk host ls --sync`）：%v\n", err)
	}
	if n, err := fetchAndCacheProxyHub(profile); err == nil {
		fmt.Fprintf(w, "✔ proxy hub 目录已同步（%d 条）\n", n)
	} else {
		fmt.Fprintf(w, "… proxy hub 目录同步失败（稍后可 `bk proxy hub ls --sync`）：%v\n", err)
	}

	// 3) 首次信任主机：仅在 ssh.insecure 未被显式设置时才置 true（不覆盖用户显式的 false）。
	configDirty := false
	if !v.IsSet("ssh.insecure") {
		v.Set("ssh.insecure", true)
		configDirty = true
		fmt.Fprintln(w, "✔ 设置 ssh.insecure=true（首次连接信任主机）")
	} else {
		fmt.Fprintf(w, "✔ ssh.insecure 已配置（%t），保持不变\n", v.GetBool("ssh.insecure"))
	}

	// 4) 生成并登记 SSH 密钥（除非 --no-provision 或已配 identity）。
	switch {
	case initNoProvision:
		fmt.Fprintln(w, "· 跳过 SSH 密钥生成（--no-provision）")
		if configDirty {
			writeBsConfig(w)
		}
	case v.GetString("ssh.identity") != "":
		fmt.Fprintf(w, "✔ 已配置 ssh.identity（%s），跳过密钥生成\n", v.GetString("ssh.identity"))
		if configDirty {
			writeBsConfig(w)
		}
	default:
		// 经 SSHConfig 解析 host（会回退到主机目录），再委托 provision --set-identity；
		// provision 的 set-identity 会把 ssh.identity 与上面设的 ssh.insecure 一并写回 .bs.yaml。
		sshcfg, err := SSHConfig(profile)
		if err != nil || sshcfg.Host == "" {
			fmt.Fprintf(w, "… 未能确定目标主机，跳过密钥生成（可手动 `bk ssh-key provision --set-identity`）：%v\n", err)
			if configDirty {
				writeBsConfig(w)
			}
			break
		}
		provisionHost = sshcfg.Host
		provisionSetIdentity = true
		fmt.Fprintf(w, "→ 为主机 %s 生成并登记 SSH 密钥…\n", sshcfg.Host)
		if err := provisionCmd.RunE(provisionCmd, nil); err != nil {
			fmt.Fprintf(w, "… 生成/登记密钥失败（可重试 `bk ssh-key provision --set-identity`）：%v\n", err)
		}
	}

	// 5) 后续步骤。
	fmt.Fprintln(w, "\n下一步：")
	fmt.Fprintln(w, "  1.（管理员）代装你的公钥：bk ssh-key install <你的名称> --sudo --config ~/.bs.admin.yaml")
	fmt.Fprintln(w, "  2. 之后即可使用：")
	fmt.Fprintln(w, "       bk app ls")
	fmt.Fprintln(w, "       bk proxy forward 8233:dokku.temporal.main.ui:8080   # 浏览器开 http://127.0.0.1:8233")
	fmt.Fprintln(w, "       git push 部署（私钥已就绪）")
	return nil
}

// writeBsConfig 把当前 viper 设置写回 .bs.yaml（无既有文件则安全创建），与 viperSetIdentity 同款。
// best-effort：失败仅提示。
func writeBsConfig(w io.Writer) {
	v := viper.GetViper()
	var err error
	if v.ConfigFileUsed() == "" {
		err = v.SafeWriteConfig()
	} else {
		err = v.WriteConfig()
	}
	if err != nil {
		fmt.Fprintf(w, "… 写入 .bs.yaml 失败（可手动设 ssh.insecure: true）：%v\n", err)
	}
}

func init() {
	initCmd.Flags().BoolVar(&initNoProvision, "no-provision", false, "不自动生成/登记 SSH 密钥")
	rootCmd.AddCommand(initCmd)
}
