/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"pkg.blksails.net/bk/internal/vault"
)

// vaultGet.go 实现 `bk vault get <app> KEY`：`Store.Get` 取回密文 → `Decrypt` 解密 →
// 仅输出明文 VALUE 本身（design「get 流程」；Requirement 2.1/2.2/2.3/2.4）。
//
// 边界（_Boundary: vaultCmd 组_）：本文件只承载 get 子命令与其可测核心 runVaultGet，经
// init() self-register 到 vault.go 既有 vaultCmd。复用 vault.go 的 newVaultStore /
// vaultMasterKey 与 internal/vault.Decrypt / Store.Get，不修改 vault.go、vaultSet.go、
// internal/vault 或其它文件。

// vaultGetter 抽象 get 所需的唯一读取缝：按 (app,key) 取回密文。
// *vault.Store 通过其 Get 方法满足该接口，使 runVaultGet 可注入 fake、在不触达真实
// Supabase / 主密钥的前提下被验证。
type vaultGetter interface {
	Get(app, key string) (string, error)
}

// runVaultGet 是 vault get 的可测核心。
//
// 安全不变量（Requirement 2.3）：任一失败路径（未找到 / 存储错误 / 解密失败）均返回错误并
// 以非零退出码结束，且绝不向 w 写出任何明文或密文——仅在解密成功后才向 w 写出明文 VALUE。
//
// 流程（design「get 流程」）：
//  1. getter.Get(app,key) 取回密文。若为 vault.ErrNotFound（errors.Is），返回「未找到」类
//     清晰错误（含 app/key 名但不含任何 value，Requirement 2.2）；其它错误包裹后返回
//     （Requirement 2.2 路径外的存储/权限错误，均非零退出）。
//  2. decrypt(masterKey, 密文) 解密。失败（主密钥不符 / 密文被篡改）返回清晰的解密失败错误，
//     且不向 w 写出任何明文/密文（Requirement 2.3）。
//  3. 成功后仅向 w 写出明文 VALUE 本身（fmt.Fprintln），不附带 key 名或额外修饰，便于脚本
//     直接消费（Requirement 2.1/2.4）。
func runVaultGet(w io.Writer, app, key string, masterKey []byte, getter vaultGetter, decrypt func(key []byte, ciphertext string) (string, error)) error {
	ciphertext, err := getter.Get(app, key)
	if err != nil {
		// 未找到：给出「未找到」类清晰提示（含 app/key，但不含 value），非零退出（R2.2）。
		// 以 %w 包裹 vault.ErrNotFound，保留 errors.Is 可识别性，供上层据此区分退出语义。
		if errors.Is(err, vault.ErrNotFound) {
			return fmt.Errorf("未找到 secret：app=%q key=%q：%w", app, key, vault.ErrNotFound)
		}
		// 其它存储/权限错误：包裹原因后返回，同样非零退出。
		return fmt.Errorf("读取 secret key %q（app %q）失败：%w", key, app, err)
	}

	plaintext, err := decrypt(masterKey, ciphertext)
	if err != nil {
		// 解密失败：返回清晰原因，绝不向 w 写出任何明文/密文（R2.3 关键安全断言）。
		return fmt.Errorf("解密 secret key %q（app %q）失败：%w", key, app, err)
	}

	// 成功：仅输出明文 VALUE 本身（无 key 名、无标签），便于管道消费（R2.1/R2.4）。
	_, err = fmt.Fprintln(w, plaintext)
	return err
}

// vaultGetCmd 是 `bk vault get <app> KEY`。装配按当前 profile 认证的 vault.Store 与本机
// 主密钥后委托 runVaultGet；RunE 保持轻薄，取回/解密/输出与退出码语义均落在 runVaultGet。
//
// 采用 cobra.ExactArgs(2)：恰需应用名 + 单个 KEY；不符时由 cobra 提示参数错误并以非零退出码
// 结束。未登录/会话失效时 newVaultStore 透传 AuthedClient 的引导「bk auth login」错误
// （Requirement 7.2）。首用主密钥不存在时 vaultMasterKey 经 LoadOrCreateKey 自动生成并
// 持久化（0600）后继续（Requirement 6.2）。
var vaultGetCmd = &cobra.Command{
	Use:   "get <app> KEY",
	Short: "取回并解密单个 secret，仅输出明文 VALUE",
	Long: `从 Supabase blacksail.secrets 取回 (app, KEY) 对应的密文，用本机主密钥解密，
并仅将明文 VALUE 本身输出到标准输出（不附带 key 名或额外修饰），便于脚本直接消费。

指定的 (app, KEY) 不存在时显示「未找到」提示并以非零退出码结束；取回的密文用当前主密钥
解密失败（主密钥不匹配或密文被篡改）时显示失败原因并以非零退出码结束，且不输出任何明文。

示例用法：
  bk vault get myapp DB_PASSWORD
  export DB_PASSWORD="$(bk vault get myapp DB_PASSWORD)"`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newVaultStore(profile)
		if err != nil {
			return err
		}
		key, err := vaultMasterKey()
		if err != nil {
			return err
		}
		return runVaultGet(cmd.OutOrStdout(), args[0], args[1], key, store, vault.Decrypt)
	},
}

func init() {
	vaultCmd.AddCommand(vaultGetCmd)
}
