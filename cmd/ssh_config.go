/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"github.com/spf13/viper"

	"pkg.blksails.net/bk/internal/config"
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
	return sshConfigFrom(viper.GetViper(), profile)
}

// sshConfigFrom 是 SSHConfig 的可测核心：从注入的 *viper.Viper 读取全局 ssh 块，
// 装配为纯数据的 config.SSHSettings，再委托 SSHSettings.ToSSHConfig() 完成默认值
// 补齐与必填校验。把 *viper.Viper 作为入参注入，使测试可用内存 yaml 构造 viper，
// 无需触达文件系统或全局 viper 状态。
//
// 本函数为纯读：仅调用 viper 的 Get* 读取，不写回任何键，也不访问 auth.json。
func sshConfigFrom(v *viper.Viper, profile string) (sshx.Config, error) {
	_ = profile // 全局 ssh 块；profile 随签名保留以供向前兼容（见文件头注释）。

	settings := config.SSHSettings{
		Host:     v.GetString("ssh.host"),
		User:     v.GetString("ssh.user"),
		Port:     v.GetInt("ssh.port"),
		Identity: v.GetString("ssh.identity"),
		Insecure: v.GetBool("ssh.insecure"),
	}
	return settings.ToSSHConfig()
}
