/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"pkg.blksails.net/bk/internal/dokku"
	"pkg.blksails.net/bk/internal/sshkeys"
)

// sshKey.go 提供「SSH 密钥发放」命令组的父命令与公共装配辅助层（design：File Structure
// Plan cmd/sshKey.go；Components「cmd 层」）。
//
// 边界（_Boundary: cmd/sshKey 公共层_）：本文件只负责
//   - 注册 `ssh-key` 父命令到既有 rootCmd（不改 root.go；经 init() 追加，design 行 143）。
//   - 暴露供 provision/list/install/revoke 子命令复用的纯辅助与薄装配辅助；
//     子命令各自在其文件的 init() 里 self-register 到 sshKeyCmd（本文件不挂子命令）。
//
// 依赖方向（design 行 38/146）：cmd/sshKey* → internal/sshkeys、internal/dokku、
// AuthedClient/SSHConfig；internal/* 不反向依赖 cmd。装配（取 client→构造 Store /
// 取 SSH 配置→构造 dokku.Client）刻意集中在 cmd 层完成。

// sshKeyCmd 是 `bk ssh-key` 命令组的父命令。本身不执行动作，仅承载子命令与
// 命令组级持久标志（如 --sudo，供 install/revoke 的 dokku 装配读取）。
var sshKeyCmd = &cobra.Command{
	Use:   "ssh-key",
	Short: "SSH 密钥发放（生成/登记/代装/吊销）",
	Long: `管理到 Dokku 主机的 SSH 接入密钥。

普通用户：
  bk ssh-key provision   在本机生成密钥对并登记公钥（pending）
  bk ssh-key list        查看自己登记的密钥与状态

管理员：
  bk ssh-key install     把 pending 公钥代装到 Dokku 并回写 installed
  bk ssh-key revoke      吊销密钥并从 Dokku 移除`,
}

// sshKeySudo 控制装配 dokku.Client 时是否以 `dokku <args>` 形式执行（普通管理员账号
// 需 sudo 包装；标准 dokku 强制命令账号则为 false）。作为命令组级持久标志，install/
// revoke 通过 newDokkuClient 间接读取。
var sshKeySudo bool

func init() {
	rootCmd.AddCommand(sshKeyCmd)

	sshKeyCmd.PersistentFlags().BoolVar(&sshKeySudo, "sudo", false,
		"以 sudo 方式执行 dokku 命令（普通管理员账号使用；默认按 dokku 强制命令执行）")
}

// deriveKeyName 由归属邮箱与目标主机派生稳定、确定、对文件系统与 dokku 都安全的密钥名称，
// 形如 `bk-<email-localpart>-<host>`（Requirement 2.1：登记需一个可读名称；5.2：以该名称
// 在 Dokku 执行 ssh-keys:add）。这是 dokku ssh-keys 的条目名称。
//
// 清洗规则：取邮箱 @ 之前的 local-part；对 local-part 与 host 分别小写并把任何非
// [a-z0-9] 字符折叠为单个 '-'，再去除首尾 '-'。同输入恒得同输出（确定性）。
func deriveKeyName(email, host string) string {
	local := email
	if i := strings.IndexByte(email, '@'); i >= 0 {
		local = email[:i]
	}
	return "bk-" + sanitizeSegment(local) + "-" + sanitizeSegment(host)
}

// privateKeyPath 解析目标主机对应的本机私钥路径：`<home>/.local/bk/keys/<host>.key`
// （design 行 53/110/118：私钥落 ~/.local/bk/keys/，与 auth.json/vault.key 同根；
// Requirement 10.1：私钥仅存在于本机文件）。host 经同样清洗以得到安全文件名。
func privateKeyPath(host string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("解析用户主目录失败: %w", err)
	}
	name := sanitizeSegment(host) + ".key"
	return filepath.Join(home, ".local", "bk", "keys", name), nil
}

// sanitizeSegment 把任意输入折叠为只含 [a-z0-9-] 的安全片段：小写化，连续的非
// 字母数字字符压成单个 '-'，并去掉首尾 '-'。为派生名称与文件名提供确定、安全的基元。
func sanitizeSegment(s string) string {
	var b strings.Builder
	lastDash := true // 置 true 以抑制前导 '-'
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// newSSHKeyStore 经认证入口取 client，装配 sshkeys.Store（Requirement 2.2/4.4：写入与
// 读取均以当前 Supabase 身份经 AuthedClient 走 PostgREST，owner=auth.uid() 由 DB 与
// RLS 约束）。薄装配：仅串联 AuthedClient + sshkeys.NewStore，不含业务判定。
// cliSchema 是 bk CLI 工具专属数据所在的独立 Supabase schema（与应用域 blacksail 隔离）。
// ssh_keys 表位于此 schema（见 migrations/ssh_keys.sql）。
const cliSchema = "cli"

func newSSHKeyStore(profile string) (*sshkeys.Store, error) {
	client, err := AuthedClientSchema(profile, cliSchema)
	if err != nil {
		return nil, err
	}
	return sshkeys.NewStore(client), nil
}

// newDokkuClient 经 SSH 配置入口取 sshx.Config，装配 dokku.Client（Requirement 5.2/7.1：
// 管理员借既有 SSH 接入在 Dokku 主机执行命令）。Sudo 取自命令组级 --sudo 持久标志。
// 调用方在用完后须 Close。
func newDokkuClient(profile string) (*dokku.Client, error) {
	cfg, err := SSHConfig(profile)
	if err != nil {
		return nil, err
	}
	return dokku.New(dokku.Config{SSH: cfg, Sudo: sshKeySudo})
}
