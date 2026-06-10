/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"pkg.blksails.net/bk/internal/sshkeys"
)

// sshKeyList.go 实现 `bk ssh-key list`：列出当前用户登记的全部密钥及其状态
// （design「cmd 层：sshKeyList」；Requirement 4.1/4.2/4.3）。
//
// 边界（_Boundary: cmd/sshKeyList_）：本文件只承载 list 子命令与其可测核心 runSSHKeyList。
// 复用 sshKey.go 的装配辅助 newSSHKeyStore，不修改 sshKey.go / root.go / internal/*。
//
// 安全不变量（Requirement 4.2）：list 只展示登记元数据（名称/指纹/主机/状态/相关时间）。
// KeyRecord 本身不含任何私钥字段（types.go），且 runSSHKeyList 只逐列渲染白名单字段、
// 绝不 dump 原始结构，故输出永不泄露私钥内容。

// keyLister 抽象「列出当前用户密钥」这一读取缝：RLS 自动按 owner=auth.uid() 限定，
// 故仅返回当前用户记录（Requirement 4.4）。*sshkeys.Store 通过其 ListMine 满足该接口，
// 使 runSSHKeyList 可注入 fake、在不触达 Supabase 的前提下被验证。
type keyLister interface {
	ListMine() ([]sshkeys.KeyRecord, error)
}

// emptySSHKeyListMessage 在当前用户尚无任何登记密钥时展示，引导其运行 provision，
// 而非以错误形式呈现空集（Requirement 4.3）。
const emptySSHKeyListMessage = "暂无已登记的 SSH 密钥，运行 bk ssh-key provision 生成"

// runSSHKeyList 是 list 的可测核心：调用 lister.ListMine() 取当前用户的全部记录，
// 以表格渲染 名称/指纹/主机/状态/创建时间（含已安装/已吊销时间，缺省以 "-" 占位）到 w
// （Requirement 4.1）。空集不是错误：写出友好空提示并返回 nil（Requirement 4.3）。
// ListMine 出错（如权限不足）以 %w 透传，由命令层非零退出（Requirement 7.x）。
//
// 输出只逐列展示登记元数据这一白名单，绝不打印私钥（KeyRecord 也不含私钥字段，
// Requirement 4.2）。
func runSSHKeyList(w io.Writer, lister keyLister) error {
	recs, err := lister.ListMine()
	if err != nil {
		return fmt.Errorf("列出 SSH 密钥失败：%w", err)
	}

	if len(recs) == 0 {
		fmt.Fprintln(w, emptySSHKeyListMessage)
		return nil
	}

	tw := tabwriter.NewWriter(w, 1, 1, 2, ' ', 0)
	fmt.Fprintln(tw, "Name\tFingerprint\tHost\tStatus\tCreated\tInstalled\tRevoked")
	for _, r := range recs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Name,
			r.Fingerprint,
			r.Host,
			r.Status,
			dashIfEmpty(r.CreatedAt),
			dashIfEmpty(r.InstalledAt),
			dashIfEmpty(r.RevokedAt),
		)
	}
	return tw.Flush()
}

// dashIfEmpty 把空时间字段渲染为 "-"，保持表格列对齐且不向用户展示空白歧义。
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// sshKeyListCmd 是 `bk ssh-key list`（别名 ls）。装配真实 Store 后委托 runSSHKeyList：
// 读取 store 由 newSSHKeyStore(profile) 经当前 Supabase 身份装配，RLS 仅返回当前用户记录。
var sshKeyListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "查看自己登记的 SSH 密钥与状态",
	Long: `列出当前用户登记的全部 SSH 密钥及其状态。

显示的信息包括：
- Name:        密钥名称（bk-<email>-<host>）
- Fingerprint: 公钥指纹（SHA256:...）
- Host:        目标主机
- Status:      状态（pending / installed / revoked）
- Created:     登记时间
- Installed:   安装时间（未安装为 -）
- Revoked:     吊销时间（未吊销为 -）

仅返回当前用户拥有的记录（由 RLS 按 owner 限定），输出不含任何私钥内容。
当前无任何登记密钥时给出友好的空列表提示。

示例用法：
  bk ssh-key list
  bk ssh-key ls`,
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newSSHKeyStore(profile)
		if err != nil {
			return err
		}
		return runSSHKeyList(os.Stdout, store)
	},
}

func init() {
	sshKeyCmd.AddCommand(sshKeyListCmd)
}
