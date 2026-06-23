/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/viper"

	"pkg.blksails.net/bk/internal/config"
	"pkg.blksails.net/bk/internal/hosts"
	"pkg.blksails.net/bk/internal/sshx"
)

// ssh_config.go 提供 cli-foundation 的稳定共享入口 SSHConfig：按 profile 读取
// `.bs.yaml` 的 ssh 块并映射为 internal/sshx.Config（Requirement 9.1–9.6）。
//
// 配置形状（design：Data Models / `.bs.yaml` 配置结构）：ssh 块是顶层、全局的——
// 键为 `ssh.host`、`ssh.user`、`ssh.port`、`ssh.identity`、`ssh.insecure`，并非
// per-profile 子树。`profile` 入参随签名冻结而保留，与 AuthedClient(profile) 形成
// 一致的调用约定并为未来 per-profile ssh 块留出向前兼容空间；当前实现读取的是全局
// ssh 块。profile 间的隔离体现在 auth/session 层：本入口是纯读函数，不读取、不修改
// `~/.local/bk/auth.json`，因此对某个 profile 的 SSH 读取绝不触碰其它 profile 的
// 会话数据（Requirement 7.1/7.3）。
//
// 边界（Requirement 9.6 / Boundary）：本入口仅供 dokku-management 消费。
// port-proxy 不依赖该 SSH 连接配置入口——其远端可达性由独立传输机制决定，
// 使用自有的 proxy.* 配置，而非这里的 ssh.* 块。此为文档级确认，无代码连线。

// SSHConfig 按 profile 读取 .bs.yaml 的 ssh 块并映射为 sshx.Config。
//
// 行为（Requirement 9.2/9.3/9.4）：
//   - 字段映射：ssh.host→Host、ssh.user→User、ssh.port→Port、
//     ssh.identity→IdentityFile、ssh.insecure→Insecure。
//   - ssh.host 缺失或为空 → 返回 internal/config 给出的明确错误（不构造无效配置）。
//   - ssh.port 未配置 → 填默认 22；ssh.user 未配置 → 保持为空（不硬编码 root），
//     由下游消费方 dokku.New 默认为 "dokku"。
//
// 签名冻结，供下游 spec（dokku-management）安全依赖（Requirement 9.1/9.5）。
func SSHConfig(profile string) (sshx.Config, error) {
	return sshConfigFrom(viper.GetViper(), profile, func(p string) ([]hosts.Host, error) {
		return hosts.Load(hostsCache, p)
	})
}

// sshConfigFrom 是 SSHConfig 的可测核心：从注入的 *viper.Viper 读取全局 ssh 块，
// 装配为纯数据的 config.SSHSettings，再委托 SSHSettings.ToSSHConfig() 完成默认值
// 补齐与必填校验。把 *viper.Viper 作为入参注入，使测试可用内存 yaml 构造 viper，
// 无需触达文件系统或全局 viper 状态。
//
// 本函数为纯读：仅调用 viper 的 Get* 读取，不写回任何键，也不访问 auth.json。
// 解析优先级（本地 .bs.yaml 优先）：
//  1. 本地显式配了 ssh.host（非空）→ 完全使用本地 ssh 块（历史行为不变）。
//  2. 本地未配 ssh.host → 回退到登录后缓存的在线主机目录：按 ssh.host_name 选择，
//     未指定则取 is_default（或唯一一条）；host/user/port 取在线记录，identity/insecure
//     仍取本地（私钥与本机安全选项不入库，只能本地提供）。本地若另配了 ssh.user/ssh.port
//     则继续覆盖在线值（保持本地优先的一致语义）。
//  3. 既无本地 host 也无可用缓存 → 退回 ToSSHConfig() 给出「未配置 ssh.host」的明确错误。
//
// loadHosts 作为注入缝（生产为 hosts.Load(hostsCache,·)），使分层逻辑可在内存中被测试。
func sshConfigFrom(v *viper.Viper, profile string, loadHosts func(string) ([]hosts.Host, error)) (sshx.Config, error) {
	local := config.SSHSettings{
		Host:     v.GetString("ssh.host"),
		User:     v.GetString("ssh.user"),
		Port:     v.GetInt("ssh.port"),
		Identity: v.GetString("ssh.identity"),
		Insecure: v.GetBool("ssh.insecure"),
	}

	// 本地优先：显式配了 ssh.host 就完全用本地块。
	if strings.TrimSpace(local.Host) != "" {
		return local.ToSSHConfig()
	}

	// 回退：用登录后缓存的在线主机目录。
	wantName := v.GetString("ssh.host_name")
	if loadHosts != nil {
		list, err := loadHosts(profile)
		if err == nil && len(list) > 0 {
			h, perr := hosts.Pick(list, wantName)
			if perr == nil {
				merged := config.SSHSettings{
					Host:     h.Host,
					User:     firstNonEmpty(local.User, h.SSHUser),
					Port:     firstNonZeroInt(local.Port, h.SSHPort),
					Identity: local.Identity, // identity 只来自本地
					Insecure: local.Insecure, // insecure 只来自本地
				}
				return merged.ToSSHConfig()
			}
			if errors.Is(perr, hosts.ErrNotFound) {
				if wantName != "" {
					return sshx.Config{}, fmt.Errorf(
						"缓存主机目录中未找到名为 %q 的主机；运行 `bk host ls` 查看可用项，或在 .bs.yaml 配置 ssh.host", wantName)
				}
				return sshx.Config{}, fmt.Errorf(
					"缓存中有多个主机且未指定默认；请在 .bs.yaml 设置 ssh.host_name 指定其一（运行 `bk host ls` 查看）")
			}
		}
	}

	// 都没有 → 回退本地（会因 host 空给出既有的明确错误，并建议先登录同步或配置 ssh.host）。
	return local.ToSSHConfig()
}

// firstNonZeroInt 返回第一个非零整数。
func firstNonZeroInt(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}
