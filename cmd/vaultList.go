/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// vaultList.go 实现 `bk vault list <app>`：`Store.ListKeys` 取回 key 名 → 每行一个 key
// 稳定顺序输出，绝不输出值（design「list 流程」；Requirement 3.1/3.2/3.3/3.4）。
//
// 边界（_Boundary: vaultCmd 组_）：本文件只承载 list 子命令与其可测核心 runVaultList，经
// init() self-register 到 vault.go 既有 vaultCmd。复用 vault.go 的 newVaultStore 与
// internal/vault.Store.ListKeys，不修改 vault.go、其它 cmd 文件或 internal/vault。
//
// 注意：list 仅暴露 key 名，结构上不取回任何密文/明文（ListKeys 仅 Select("key")），故
// 无需本机主密钥、无需解密——与 get/export 不同，本命令不调用 vaultMasterKey。
type vaultLister interface {
	ListKeys(app string) ([]string, error)
}

// runVaultList 是 vault list 的可测核心。
//
// 安全不变量（Requirement 3.2/3.4）：绝不向 w 写出任何 value——ListKeys 仅返回 key 名，
// 本函数也只逐行写 key 名本身，不附带值、标签或额外修饰。
//
// 流程（design「list 流程」）：
//  1. lister.ListKeys(app) 取回按稳定升序排序的 key 名（ListKeys 已 sort，空集返回空切片
//     与 nil 错误，非 ErrNotFound）。存储/权限错误包裹后返回，非零退出。
//  2. 空集合（len(keys)==0）：向 w 写出友好空提示并返回 nil（零退出，R3.3）。
//  3. 否则按返回顺序每行一个 key 写出（fmt.Fprintln，R3.1/R3.4：稳定顺序、每行一个 key，
//     便于脚本消费）。
func runVaultList(w io.Writer, app string, lister vaultLister) error {
	keys, err := lister.ListKeys(app)
	if err != nil {
		return fmt.Errorf("列出 app %q 的 secret key 失败：%w", app, err)
	}

	// 空集不是错误：给出友好提示，零退出（R3.3）。
	if len(keys) == 0 {
		_, err = fmt.Fprintf(w, "app %q 暂无密钥\n", app)
		return err
	}

	// 每行一个 key，按 ListKeys 返回的稳定顺序输出，绝不附带任何 value（R3.1/R3.2/R3.4）。
	for _, key := range keys {
		if _, err = fmt.Fprintln(w, key); err != nil {
			return err
		}
	}
	return nil
}

// vaultListCmd 是 `bk vault list <app>`。装配按当前 profile 认证的 vault.Store 后委托
// runVaultList；RunE 保持轻薄。采用 cobra.ExactArgs(1)：恰需应用名。未登录/会话失效时
// newVaultStore 透传 AuthedClient 的引导「bk auth login」错误（Requirement 7.2）。
// 不取主密钥——list 不解密（仅列 key 名）。
var vaultListCmd = &cobra.Command{
	Use:   "list <app>",
	Short: "列出该 app 下的 key 名（不显示值）",
	Long: `从 Supabase blacksail.secrets 列出指定 app 下属于当前身份的全部 secret 的 key 名，
以每行一个 key 的稳定顺序输出，便于脚本消费。仅展示 key 名，绝不展示任何 secret 的值。

指定 app 下没有任何 secret 时显示友好提示并正常退出（不报错）。

示例用法：
  bk vault list myapp`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newVaultStore(profile)
		if err != nil {
			return err
		}
		return runVaultList(cmd.OutOrStdout(), args[0], store)
	},
}

func init() {
	vaultCmd.AddCommand(vaultListCmd)
}
