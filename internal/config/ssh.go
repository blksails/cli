package config

import (
	"fmt"
	"strings"

	"pkg.blksails.net/bk/internal/sshx"
)

// SSHSettings 描述 `.bs.yaml` 中 `ssh` 配置块的已读取值（纯数据，无 viper 依赖）。
//
// 字段对应配置键：
//   - Host     ← ssh.host     （必填）
//   - User     ← ssh.user     （可选；留空则由下游 dokku.New 默认为 dokku，不硬编码 root）
//   - Port     ← ssh.port     （默认 22）
//   - Identity ← ssh.identity （私钥路径，可选）
//   - Insecure ← ssh.insecure （true 跳过 known_hosts 校验）
//
// 该结构与 ToSSHConfig 构成一个纯函数层：调用方（cmd 层）先用 viper 读取这些值，
// 再传入此处映射为 sshx.Config，以便独立单测且不耦合 viper/cobra。
type SSHSettings struct {
	Host     string `mapstructure:"host"`
	User     string `mapstructure:"user"`
	Port     int    `mapstructure:"port"`
	Identity string `mapstructure:"identity"`
	Insecure bool   `mapstructure:"insecure"`
}

// defaultSSHPort 是未显式配置 ssh.port 时采用的默认端口。
const defaultSSHPort = 22

// ToSSHConfig 将已读取的 ssh 块值映射为 sshx.Config，补齐默认值并做必填校验。
//
// 行为约定（Requirements 2.2/2.3/2.4/2.5/2.6、9.2/9.4）：
//   - Host 缺失或仅含空白 → 返回明确错误（不构造无效连接配置）。
//   - Port 为 0（未配置）→ 填默认 22；显式值透传。
//   - User 未配置 → 保持为空（不硬编码 root），由下游 dokku.New 应用领域默认值 dokku。
//   - Insecure → 原样透传：true 跳过主机密钥校验；未设置/false 保留 known_hosts 校验。
//   - 字段映射：host→Host、user→User、port→Port、identity→IdentityFile、insecure→Insecure。
//
// 本函数为纯函数：无副作用、不读取 viper、不访问文件系统。
func (s SSHSettings) ToSSHConfig() (sshx.Config, error) {
	if strings.TrimSpace(s.Host) == "" {
		return sshx.Config{}, fmt.Errorf("config: 未配置 SSH 主机地址（ssh.host）")
	}

	port := s.Port
	if port == 0 {
		port = defaultSSHPort
	}

	return sshx.Config{
		Host:         s.Host,
		User:         s.User, // 未配置时保持为空，由下游 dokku.New 默认为 dokku
		Port:         port,
		IdentityFile: s.Identity,
		Insecure:     s.Insecure,
	}, nil
}
