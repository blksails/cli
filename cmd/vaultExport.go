/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"pkg.blksails.net/bk/internal/vault"
)

// vaultExport.go 实现 `bk vault export <app>`：`Store.List` 取回该 app 下全部完整记录
// （含密文 value）→ 逐条 `Decrypt` 解密 → 全部成功后按 `KEY=VALUE` 每行一条输出为 env
// 文本，便于 `bk vault export app | bk app config:set app` 消费（design「export 流程」；
// Requirement 5.1/5.2/5.3/5.4）。
//
// 边界（_Boundary: vaultCmd 组_）：本文件只承载 export 子命令与其可测核心 runVaultExport，
// 经 init() self-register 到 vault.go 既有 vaultCmd。复用 vault.go 的 newVaultStore /
// vaultMasterKey 与 internal/vault.Decrypt / Store.List，不修改 vault.go、其它 cmd 文件
// 或 internal/vault。

// vaultListerFull 抽象 export 所需的唯一读取缝：取回 app 下全部完整记录（含密文 value）。
// *vault.Store 通过其 List 方法满足该接口，使 runVaultExport 可注入 fake、在不触达真实
// Supabase / 主密钥的前提下被验证。与 list 的 vaultLister（仅 key 名）不同：export 需密文
// 才能逐条解密。
type vaultListerFull interface {
	List(app string) ([]vault.Secret, error)
}

// runVaultExport 是 vault export 的可测核心。
//
// 安全不变量（Requirement 5.3 —— 原子输出、绝不输出部分明文）：DECRYPT-ALL-BEFORE-ANY-
// OUTPUT。先在本地缓冲区把全部记录解密并拼出完整 env 文本；任一条解密失败即返回清晰错误，
// 且绝不向 w 写出任何字节——单条密文被篡改必须使 w 上输出为 0 字节（不泄露任一已解密明文）。
//
// 流程（design「export 流程」）：
//  1. lister.List(app) 取回 app 下全部完整记录（List 已按 key 升序排序，空集返回空切片与
//     nil 错误）。存储/权限错误包裹后返回，非零退出，且不向 w 写出任何内容。
//  2. 空集合（len(secrets)==0）：向 w 写出空内容并返回 nil（零退出，R5.4）。
//  3. DECRYPT-ALL-BEFORE-ANY-OUTPUT：先对每条 secret 调用 decrypt(masterKey, s.Value)
//     并把 `KEY=VALUE` 行累积进本地 buffer；任一失败立即返回清晰错误，w 保持 0 字节
//     （R5.3 关键安全断言：整体非零退出且不输出已部分解密的明文）。
//  4. 仅当全部解密成功后，才一次性把 buffer 写入 w（按 List 返回的稳定顺序，R5.1/R5.2：
//     KEY=VALUE 每行一条的 env 文本，可被 bk app config:set 消费）。
func runVaultExport(w io.Writer, app string, masterKey []byte, lister vaultListerFull, decrypt func(key []byte, ciphertext string) (string, error)) error {
	secrets, err := lister.List(app)
	if err != nil {
		return fmt.Errorf("列出 app %q 的 secret 失败：%w", app, err)
	}

	// 空集不是错误：输出空内容，零退出（R5.4）。绝不向 w 写出任何内容。
	if len(secrets) == 0 {
		return nil
	}

	// DECRYPT-ALL-BEFORE-ANY-OUTPUT：先在本地缓冲区累积完整 env 文本，任一条解密失败即整体
	// 返回错误且不向 w 写出任何字节（R5.3）。关键不变量：不在解密过程中向 w 增量写出。
	var out strings.Builder
	for _, s := range secrets {
		plaintext, derr := decrypt(masterKey, s.Value)
		if derr != nil {
			// 任一条解密失败：返回清晰原因，w 保持 0 字节，绝不泄露已解密明文（R5.3）。
			return fmt.Errorf("解密 secret key %q（app %q）失败：%w", s.Key, app, derr)
		}
		// 每行一条 KEY=VALUE，按 List 返回的稳定顺序累积（R5.1）。
		out.WriteString(s.Key)
		out.WriteByte('=')
		out.WriteString(plaintext)
		out.WriteByte('\n')
	}

	// 仅在全部解密成功后才一次性写出（R5.3：原子输出）。
	_, err = io.WriteString(w, out.String())
	return err
}

// vaultExportCmd 是 `bk vault export <app>`。装配按当前 profile 认证的 vault.Store 与本机
// 主密钥后委托 runVaultExport；RunE 保持轻薄，取回/解密/输出与退出码语义均落在
// runVaultExport。采用 cobra.ExactArgs(1)：恰需应用名；不符时由 cobra 提示参数错误并以
// 非零退出码结束。未登录/会话失效时 newVaultStore 透传 AuthedClient 的引导「bk auth login」
// 错误（Requirement 7.2）。首用主密钥不存在时 vaultMasterKey 经 LoadOrCreateKey 自动生成
// 并持久化（0600）后继续（Requirement 6.2）。
var vaultExportCmd = &cobra.Command{
	Use:   "export <app>",
	Short: "全部解密为 KEY=VALUE env 格式输出",
	Long: `从 Supabase blacksail.secrets 取回指定 app 下属于当前身份的全部 secret，逐条用本机
主密钥解密，并以每行一条 KEY=VALUE 的 env 格式输出到标准输出，便于通过管道注入到 Dokku：

  bk vault export myapp | bk app config:set myapp

任一条记录解密失败（主密钥不匹配或密文被篡改）时报告失败并以非零退出码结束，且绝不输出
已部分解密的明文。指定 app 下没有任何 secret 时输出空内容并正常退出（零退出码）。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		app := args[0]
		store, err := newVaultStore(profile)
		if err != nil {
			return err
		}
		masterKey, err := vaultMasterKey()
		if err != nil {
			return err
		}
		return runVaultExport(cmd.OutOrStdout(), app, masterKey, store, vault.Decrypt)
	},
}

func init() {
	vaultCmd.AddCommand(vaultExportCmd)
}
