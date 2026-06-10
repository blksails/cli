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

	"pkg.blksails.net/bk/internal/vault"
)

// vault.go 提供 Secret Vault 命令组的父命令与公共装配辅助层（design：File Structure
// Plan cmd/vault.go；Components「vaultCmd 组」）。
//
// 边界（_Boundary: vaultCmd 组_）：本文件只负责
//   - 注册 `vault` 父命令到既有 rootCmd（不改 root.go；经 init() 追加，design 行 140/232）。
//   - 暴露供 set/get/list/rm/export 子命令复用的纯辅助与薄装配辅助；子命令各自在其
//     文件的 init() 里 self-register 到 vaultCmd（本文件不挂子命令）。
//
// 依赖方向（design 依赖说明）：cmd/vault* → internal/vault.Store、internal/vault(crypto)、
// AuthedClient；internal/vault 不反向依赖 cmd。装配（取 client→构造 Store / 解析主密钥
// 路径→LoadOrCreateKey）刻意集中在 cmd 层完成。
//
// 注意：secrets 表位于应用域 `blacksail` schema（AuthedClient 的默认 schema），
// 故装配 Store 用 plain AuthedClient(profile)，与 ssh-key 的独立 `cli` schema 不同。

// vaultCmd 是 `bk vault` 命令组的父命令。本身不执行动作，仅承载子命令；给定 RunE
// 让 cobra 在尚无子命令时仍渲染帮助（完成态：`bk vault --help` 显示 vault 命令）。
var vaultCmd = &cobra.Command{
	Use:   "vault",
	Short: "Secret Vault（本机加密、Supabase 共享）",
	Long: `管理加密 secret：以本机主密钥加密 VALUE，密文存入 Supabase blacksail.secrets，
按当前登录身份（--profile）多端共享。主密钥仅存于本机 ~/.local/bk/vault.key。

  bk vault set <app> KEY=VALUE...   加密并写入（upsert）一个或多个 secret
  bk vault get <app> KEY            取回并解密单个 secret，仅输出明文
  bk vault list <app>               列出该 app 下的 key 名（不显示值）
  bk vault rm <app> KEY             删除单个 secret
  bk vault export <app>             全部解密为 KEY=VALUE env 格式输出`,
	// 无子命令参数时渲染帮助，保证 `bk vault` / `bk vault --help` 列出用法。
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(vaultCmd)
}

// newVaultStore 经认证入口取 client，装配 vault.Store（Requirement 7.1/7.3：读写均以
// 当前 Supabase 身份经 AuthedClient 走 PostgREST，owner=auth.uid() 由 DB 与 RLS 约束）。
// 薄装配：仅串联 AuthedClient + vault.NewStore，不含业务判定。
//
// 用 plain AuthedClient（默认 blacksail schema）——secrets 表位于应用域 blacksail。
// 未登录/会话失效时 AuthedClient 已返回包裹 ErrReloginRequired、引导 `bk auth login`
// 的明确错误（Requirement 7.2），此处直接透传。
func newVaultStore(profile string) (*vault.Store, error) {
	client, err := AuthedClient(profile)
	if err != nil {
		return nil, err
	}
	return vault.NewStore(client), nil
}

// vaultMasterKey 解析本机主密钥默认路径 `~/.local/bk/vault.key`（Requirement 6.1：
// 与 auth.json/keys 同根），并委托 vaultMasterKeyAt 取密钥。
func vaultMasterKey() ([]byte, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("解析用户主目录失败: %w", err)
	}
	return vaultMasterKeyAt(filepath.Join(home, ".local", "bk", "vault.key"))
}

// vaultMasterKeyAt 是 vaultMasterKey 的可测核心：把主密钥路径作为参数注入，便于以
// 临时路径覆盖「首用生成 0600 / 幂等加载 / 损坏不覆盖」三条路径。
//
// 实际的生成与校验逻辑由 internal/vault.LoadOrCreateKey 承担（Requirement 6.2：文件
// 不存在则生成 32 字节随机密钥、建目录 0700、写文件 0600；Requirement 6.5：文件存在
// 但格式无效/长度异常时返回明确错误且不覆盖生成）。此处不重复实现校验。
func vaultMasterKeyAt(path string) ([]byte, error) {
	return vault.LoadOrCreateKey(path)
}

// parseVaultKeyValue 解析单个 `KEY=VALUE` 参数（Requirement 1.4/1.5）：仅以首个 '='
// 切分，故 VALUE 中含 '='（如 "URL=http://x?a=b"）被完整保留；缺 '=' 或 key 为空返回
// 清晰的格式错误（调用方据此整体非零退出、不写入）。允许空 VALUE（"KEY="）。
//
// 与 cmd/app_render.go 的 appParseKeyValues 语义一致但 vault-local，刻意不跨 spec 复用
// dokku-management 的助手，保持 secret-vault 解耦。
func parseVaultKeyValue(s string) (key, value string, err error) {
	key, value, ok := strings.Cut(s, "=")
	if !ok {
		return "", "", fmt.Errorf("参数 %q 格式错误：需为 KEY=VALUE 形式", s)
	}
	if key == "" {
		return "", "", fmt.Errorf("参数 %q 格式错误：键不能为空", s)
	}
	return key, value, nil
}
