/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"pkg.blksails.net/bk/internal/sshkeys"
)

// sshKeyRevoke.go 实现 `bk ssh-key revoke <指纹|名称>`：管理员吊销某条登记——借既有 SSH 接入
// 在 Dokku 移除公钥，移除成功后把记录回写 revoked 并记录吊销者/时间（design「revoke（管理员：吊销）」
// 序列图，与 install 序列同构；Requirement 6.1–6.4/7.1）。
//
// 边界（_Boundary: cmd/sshKeyRevoke_）：本文件只承载 revoke 子命令与其可测核心 runSSHKeyRevoke。
// 复用 sshKey.go 的装配辅助 newSSHKeyStore/newDokkuClient，复用 sshKeyInstall.go 的 currentSessionUID
// 取当前会话身份作为 revoked_by；不修改 sshKey.go / sshKeyInstall.go / root.go / internal/*。
//
// 安全不变量（Requirement 10.2/10.4）：KeyRecord 不含私钥字段，revoke 只逐字段使用名称定位 Dokku
// 条目，绝不 dump 原始结构、绝不打印任何密钥；吊销者(uid)与时间由 Store.MarkRevoked 落库供审计。

// revokeStore 抽象 revoke 所需的两类持久操作：按指纹/名称定位记录、回写已吊销状态。
// *sshkeys.Store 经其 Find/MarkRevoked 满足该接口，使 runSSHKeyRevoke 可注入 fake、
// 在不触达 Supabase 的前提下被验证（design「Unit（revoke 编排，注入 fake Store）」）。
type revokeStore interface {
	Find(ref string) (sshkeys.KeyRecord, error)
	MarkRevoked(id, by string) error
}

// keyRemover 抽象在 Dokku 主机上移除一个命名条目公钥的能力。
// *dokku.Client 经其 SSHKeysRemove 满足该接口，使 runSSHKeyRevoke 可注入 fake、
// 在不触达 SSH/网络的前提下被验证（Requirement 9.1/9.2）。
type keyRemover interface {
	SSHKeysRemove(ctx context.Context, name string) (string, error)
}

// runSSHKeyRevoke 是 revoke 的可测核心，编排严格对应 design 的 revoke 序列：
//
//  1. store.Find(ref)：按指纹或名称定位记录（Requirement 6.1）。
//     - ErrNotFound（目标不存在）→ 输出友好提示并返回 nil（零退出，幂等，Requirement 6.3）。
//     - ErrPermission（非管理员被 RLS 拒绝）→ 返回表述为「需要管理员权限」的错误，由命令层非零退出
//     （Requirement 7.1）。其它错误透传。
//  2. 记录已是 revoked → 输出友好提示并返回 nil（零退出，幂等，Requirement 6.3）。
//  3. rem.SSHKeysRemove(rec.Name)：借既有 SSH 接入在 Dokku 移除公钥（Requirement 6.1）。
//     失败 → 返回非 nil 错误（非零退出）且不调用 MarkRevoked，避免把未真正移除的记录误标 revoked
//     （Requirement 6.4）。
//  4. 移除成功 → store.MarkRevoked(rec.ID, adminID) 回写 revoked 与吊销者/时间（Requirement 6.2），
//     并向用户确认。
//
// adminID 为当前会话身份的 uid，落库为 revoked_by 供审计。输出仅使用名称与汇总信息，绝不打印密钥。
func runSSHKeyRevoke(ctx context.Context, w io.Writer, store revokeStore, rem keyRemover, ref, adminID string) error {
	rec, err := store.Find(ref)
	if err != nil {
		if errors.Is(err, sshkeys.ErrNotFound) {
			fmt.Fprintln(w, "未找到该密钥（可能已删除），无需吊销。")
			return nil
		}
		if errors.Is(err, sshkeys.ErrPermission) {
			return fmt.Errorf("需要管理员权限才能吊销密钥：%w", err)
		}
		return fmt.Errorf("定位密钥记录失败：%w", err)
	}

	if rec.Status == sshkeys.StatusRevoked {
		fmt.Fprintf(w, "密钥 %s 已吊销，无需重复操作。\n", rec.Name)
		return nil
	}

	if _, err := rem.SSHKeysRemove(ctx, rec.Name); err != nil {
		// 移除失败：保持原状态、非零退出，绝不误标 revoked（Requirement 6.4）。
		return fmt.Errorf("在 Dokku 移除密钥 %s 失败：%w", rec.Name, err)
	}

	if err := store.MarkRevoked(rec.ID, adminID); err != nil {
		return fmt.Errorf("已在 Dokku 移除密钥 %s，但回写吊销状态失败：%w", rec.Name, err)
	}

	fmt.Fprintf(w, "已吊销：%s（已从 Dokku 移除并记录吊销者与时间）。\n", rec.Name)
	return nil
}

// sshKeyRevokeCmd 是 `bk ssh-key revoke <指纹|名称>`（管理员吊销）。装配真实 Store + dokku.Client
// 与当前会话 uid 后委托 runSSHKeyRevoke。用 RunE 使权限/连接/移除错误以非零退出（Requirement 6.4/7.1）。
var sshKeyRevokeCmd = &cobra.Command{
	Use:   "revoke <指纹|名称>",
	Short: "（管理员）吊销密钥并从 Dokku 移除",
	Long: `按指纹或名称定位一条密钥登记，借管理员既有的 SSH 接入在 Dokku 主机执行 ssh-keys 移除，
成功后把该记录回写为 revoked 并记录吊销者与吊销时间。

行为：
- 非管理员被 RLS 拒绝时提示需要管理员权限并以非零退出码结束。
- 目标不存在或已是 revoked 时给出友好提示并以零退出码结束（幂等）。
- Dokku 侧移除失败时显示清晰错误并以非零退出码结束，且不将状态误标为 revoked。

输出不包含任何密钥内容。

示例用法：
  bk ssh-key revoke bk-alice-host
  bk ssh-key revoke SHA256:abcd... --sudo`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newSSHKeyStore(profile)
		if err != nil {
			return err
		}
		adminID, err := currentSessionUID(profile)
		if err != nil {
			return err
		}
		rem, err := newDokkuClient(profile)
		if err != nil {
			return err
		}
		defer rem.Close()
		return runSSHKeyRevoke(cmd.Context(), os.Stdout, store, rem, args[0], adminID)
	},
}

func init() {
	sshKeyCmd.AddCommand(sshKeyRevokeCmd)
}
