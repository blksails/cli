/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"pkg.blksails.net/bk/internal/vault"
)

// vaultSet.go 实现 `bk vault set <app> KEY=VALUE [KEY=VALUE...]`：用本机主密钥逐一加密
// VALUE 后将密文 upsert 到 Supabase blacksail.secrets（design「set 流程」；
// Requirement 1.1/1.2/1.3/1.4/1.5/1.7/6.2）。
//
// 边界（_Boundary: vaultCmd 组_）：本文件只承载 set 子命令与其可测核心 runVaultSet，经
// init() self-register 到 vault.go 既有 vaultCmd。复用 vault.go 的 newVaultStore /
// vaultMasterKey / parseVaultKeyValue 与 internal/vault.Encrypt / Store.Set，不修改
// vault.go、internal/vault 或其它文件。

// vaultSetter 抽象 set 所需的唯一写入缝：把 (app,key) 的密文 upsert 入库。
// *vault.Store 通过其 Set 方法满足该接口，使 runVaultSet 可注入 fake、在不触达真实
// Supabase / 主密钥的前提下被验证。
type vaultSetter interface {
	Set(app, key, ciphertext string) error
}

// runVaultSet 是 vault set 的可测核心。
//
// 安全不变量：在任何输出（含成功确认）中绝不回显 secret 的明文 VALUE（Requirement 1.7/6.2）——
// 确认行仅给出写入的 key 数量与 app 名。
//
// 整体写入语义（Requirement 1.4/1.7）：先把全部 pairs 逐个经 parseVaultKeyValue 解析；
// 任一项格式非法（缺 '=' / 键为空）即返回错误且不调用 setter.Set，从而不写入任何记录。
// 全部合法后再逐对加密并写入：对每对 (k,v) 调 encrypt(key, v) 得密文（Requirement 1.5 的
// 含 '=' 分隔已由 parseVaultKeyValue 保证）；encrypt 失败即返回（停在首个失败，不再续写）；
// 否则 setter.Set(app, k, 密文) 完成 upsert（Requirement 1.1/1.2/1.3）。全部成功后向 w 写出
// 确认信息，声明写入的 key 数量。
func runVaultSet(w io.Writer, app string, pairs []string, key []byte, setter vaultSetter, encrypt func(key []byte, plaintext string) (string, error)) error {
	// 阶段一：先整体解析，任一非法则整体失败、不写入（R1.4/R1.7）。
	type kv struct{ key, value string }
	parsed := make([]kv, 0, len(pairs))
	for _, p := range pairs {
		k, v, err := parseVaultKeyValue(p)
		if err != nil {
			return err
		}
		parsed = append(parsed, kv{key: k, value: v})
	}

	// 阶段二：逐对加密并写入。停在首个 encrypt 失败，不续写后续（R1.1/R1.2/R1.5）。
	for _, p := range parsed {
		ciphertext, err := encrypt(key, p.value)
		if err != nil {
			// 不回显明文 VALUE：错误仅暴露 key 名（R1.7/R6.2）。
			return fmt.Errorf("加密 key %q 失败：%w", p.key, err)
		}
		if err := setter.Set(app, p.key, ciphertext); err != nil {
			return fmt.Errorf("写入 secret key %q 到 app %q 失败：%w", p.key, app, err)
		}
	}

	// 成功确认：仅含数量与 app，绝不含明文 VALUE（R1.2/R1.7/R6.2）。
	_, err := fmt.Fprintf(w, "已写入 %d 个密钥到 app %s\n", len(parsed), app)
	return err
}

// vaultSetCmd 是 `bk vault set <app> KEY=VALUE [KEY=VALUE...]`。装配按当前 profile 认证的
// vault.Store 与本机主密钥后委托 runVaultSet；RunE 保持轻薄，解析/加密/写入/确认与退出码
// 语义均落在 runVaultSet。
//
// 采用 cobra.MinimumNArgs(2)：至少需应用名 + 1 个 KEY=VALUE；不足时由 cobra 提示参数错误
// 并以非零退出码结束。未登录/会话失效时 newVaultStore 透传 AuthedClient 的引导
// 「bk auth login」错误（Requirement 7.2）。首用主密钥不存在时 vaultMasterKey 经
// LoadOrCreateKey 自动生成并持久化（0600）后继续（Requirement 6.2）。
var vaultSetCmd = &cobra.Command{
	Use:   "set <app> KEY=VALUE [KEY=VALUE...]",
	Short: "加密并写入一个或多个 secret（upsert）",
	Long: `用本机主密钥对每个 VALUE 加密（AES-256-GCM），把密文以 owner/app/key 维度 upsert
到 Supabase blacksail.secrets，按当前登录身份（--profile）多端共享。

至少需要提供应用名与一个 KEY=VALUE。VALUE 中含 '=' 时仅以第一个 '=' 分隔，其后内容
作为完整 VALUE。任一参数不符合 KEY=VALUE 形式时显示格式错误并以非零退出码结束，且不
写入任何记录。全部成功后确认写入的 key 数量——任何输出均不回显明文 VALUE。

示例用法：
  bk vault set myapp DB_PASSWORD=s3cr3t
  bk vault set myapp A=1 B=2`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newVaultStore(profile)
		if err != nil {
			return err
		}
		key, err := vaultMasterKey()
		if err != nil {
			return err
		}
		return runVaultSet(cmd.OutOrStdout(), args[0], args[1:], key, store, vault.Encrypt)
	},
}

func init() {
	vaultCmd.AddCommand(vaultSetCmd)
}
