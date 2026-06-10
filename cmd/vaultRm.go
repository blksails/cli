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

// vaultRm.go 实现 `bk vault rm <app> KEY`：`Store.Remove` 删除目标记录 → 确认已删除
// （design「rm 流程」；Requirement 4.1/4.2/4.3/4.4）。
//
// 边界（_Boundary: vaultCmd 组_）：本文件只承载 rm 子命令与其可测核心 runVaultRm，经
// init() self-register 到 vault.go 既有 vaultCmd。复用 vault.go 的 newVaultStore 与
// internal/vault.Store.Remove，不修改 vault.go、其它子命令文件、internal/vault 或其它文件。

// vaultRemover 抽象 rm 所需的唯一删除缝：按 (app,key) 删除记录。
// *vault.Store 通过其 Remove 方法满足该接口，使 runVaultRm 可注入 fake、在不触达真实
// Supabase / RLS 的前提下被验证。
type vaultRemover interface {
	Remove(app, key string) error
}

// runVaultRm 是 vault rm 的可测核心。
//
// 幂等语义（Requirement 4.3，与 get 的关键差异）：与 get 在未找到时非零退出不同，rm 对
// 不存在的 (app,key) 视为幂等成功——向 w 写出友好提示并返回 nil（零退出）。
//
// 流程（design「rm 流程」）：
//  1. remover.Remove(app,key) 删除目标记录。store 的 .Eq(app).Eq(key) 双过滤 + RLS 保证
//     仅命中目标 key、其余 key 不受影响（Requirement 4.4）。
//  2. 若为 vault.ErrNotFound（errors.Is）：记录不存在，向 w 写友好提示「未找到，可能已删除」
//     并返回 nil（零退出，Requirement 4.3）。
//  3. 其它错误（存储/权限等）：包裹后返回，非零退出。
//  4. 成功：向 w 写出已删除确认（含 app/key 名，Requirement 4.2）。
func runVaultRm(w io.Writer, app, key string, remover vaultRemover) error {
	err := remover.Remove(app, key)
	if err != nil {
		// 未找到：幂等友好提示并零退出（R4.3）——不作为错误向上传播。
		if errors.Is(err, vault.ErrNotFound) {
			_, werr := fmt.Fprintf(w, "未找到 app %q 的密钥 %q，可能已删除\n", app, key)
			return werr
		}
		// 其它存储/权限错误：包裹原因后返回，非零退出。
		return fmt.Errorf("删除 app %q 的密钥 %q 失败：%w", app, key, err)
	}

	// 成功：确认目标 key 已删除（R4.2）。
	_, err = fmt.Fprintf(w, "已删除 app %q 的密钥 %q\n", app, key)
	return err
}

// vaultRmCmd 是 `bk vault rm <app> KEY`。装配按当前 profile 认证的 vault.Store 后委托
// runVaultRm；RunE 保持轻薄，删除/提示与退出码语义均落在 runVaultRm。
//
// 采用 cobra.ExactArgs(2)：恰需应用名 + 单个 KEY；不符时由 cobra 提示参数错误并以非零退出码
// 结束。未登录/会话失效时 newVaultStore 透传 AuthedClient 的引导「bk auth login」错误
// （Requirement 7.2）。
var vaultRmCmd = &cobra.Command{
	Use:   "rm <app> KEY",
	Short: "删除该 app 下指定的单个 secret",
	Long: `从 Supabase blacksail.secrets 删除 (app, KEY) 对应的记录，删除成功后给出确认。

指定的 (app, KEY) 不存在时以友好提示告知该 key 当前不存在，并以零退出码正常结束（幂等，
不报错）；仅删除目标 key，同一 app 下的其余 key 不受影响。

示例用法：
  bk vault rm myapp DB_PASSWORD`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newVaultStore(profile)
		if err != nil {
			return err
		}
		return runVaultRm(cmd.OutOrStdout(), args[0], args[1], store)
	},
}

func init() {
	vaultCmd.AddCommand(vaultRmCmd)
}
