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

// sshKeyInstall.go 实现 `bk ssh-key install`：管理员把状态为 pending 的公钥代装到 Dokku
// 并回写 installed（design「install（管理员：代装）」序列图；Requirement 5.1–5.6/7.1/7.3/10.4）。
//
// 边界（_Boundary: cmd/sshKeyInstall_）：本文件只承载 install 子命令与其可测核心
// runSSHKeyInstall。复用 sshKey.go 的装配辅助 newSSHKeyStore/newDokkuClient，复用 whoami.go
// 的 lookupProfile 取当前会话身份作为 installed_by；不修改 sshKey.go / root.go / internal/*。
//
// 安全不变量（Requirement 10.2/10.4）：KeyRecord 不含私钥字段，install 只逐字段使用名称与
// 公钥行，绝不 dump 原始结构、绝不打印私钥；安装者(uid)与时间由 Store.MarkInstalled 落库以供审计。

// pendingStore 抽象 install 所需的两类持久操作：读取待安装登记、回写已安装状态。
// *sshkeys.Store 经其 ListPending/MarkInstalled 满足该接口，使 runSSHKeyInstall 可注入
// fake、在不触达 Supabase 的前提下被验证（design「Unit（install 编排，注入 fake Store）」）。
type pendingStore interface {
	ListPending() ([]sshkeys.KeyRecord, error)
	MarkInstalled(id, by string) error
}

// keyInstaller 抽象在 Dokku 主机上对一个命名条目添加/移除公钥的能力。
// *dokku.Client 经其 SSHKeysAdd/SSHKeysRemove 满足该接口，使 runSSHKeyInstall 可注入 fake、
// 在不触达 SSH/网络的前提下被验证（Requirement 9.1/9.2）。
type keyInstaller interface {
	SSHKeysAdd(ctx context.Context, name, publicKey string) (string, error)
	SSHKeysRemove(ctx context.Context, name string) (string, error)
}

// runSSHKeyInstall 是 install 的可测核心，编排严格对应 design 的 install 序列：
//
//  1. store.ListPending()：仅管理员经 RLS 可读全部 pending。被拒（ErrPermission）→ 返回
//     表述为「需要管理员权限」的错误，由命令层非零退出（Requirement 5.1/7.1/7.3）。其它错误透传。
//  2. 列表为空 → 输出无待安装提示并返回 nil（零退出，Requirement 5.5）。
//  3. 逐条：先 inst.SSHKeysRemove(name)（幂等前置，名称可能不存在，其错误忽略，Requirement 9.3），
//     再 inst.SSHKeysAdd(name, publicKey)（Requirement 5.2）。Add 成功 → store.MarkInstalled(id, adminID)
//     回写 installed 与安装者/时间（Requirement 5.3/10.3）；Add 失败或 MarkInstalled 失败 → 记录原因、
//     保持该条 pending、计入失败并继续余下（单条失败不阻断，Requirement 5.4）。
//  4. 汇总成功/失败条目数及每条失败原因（Requirement 5.6）。
//
// adminID 为当前会话身份的 uid，落库为 installed_by 供审计（Requirement 10.3）。
// 输出仅使用名称与汇总信息，绝不打印公钥/私钥（KeyRecord 亦不含私钥，Requirement 10.2/10.4）。
func runSSHKeyInstall(ctx context.Context, w io.Writer, store pendingStore, inst keyInstaller, adminID string, selectors []string, all bool) error {
	pend, err := store.ListPending()
	if err != nil {
		if errors.Is(err, sshkeys.ErrPermission) {
			return fmt.Errorf("需要管理员权限才能代装公钥：%w", err)
		}
		return fmt.Errorf("读取待安装公钥失败：%w", err)
	}

	if len(pend) == 0 {
		fmt.Fprintln(w, "无待安装的公钥登记。")
		return nil
	}

	// 选择要代装的目标。安全默认（既不指定 <名称|指纹> 也不带 --all）：仅列出全部
	// pending 供管理员逐条审核，不代装任何一条——避免「一把梭」把所有 pending 无差别
	// 获批（任何能登录 + provision 的人都不应被自动放行进 Dokku）。
	targets, listedOnly, selErr := selectInstallTargets(w, pend, selectors, all)
	if selErr != nil {
		return selErr
	}
	if listedOnly {
		return nil
	}

	var (
		success  int
		failures []string // 形如 "<name>: <原因>"
	)

	for _, rec := range targets {
		// 幂等前置：先移除同名条目，忽略 not-found 等错误（名称可能尚不存在）。
		_, _ = inst.SSHKeysRemove(ctx, rec.Name)

		if _, addErr := inst.SSHKeysAdd(ctx, rec.Name, rec.PublicKey); addErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", rec.Name, addErr))
			continue
		}

		// Add 成功才回写 installed；回写失败也按失败处理，保持该条 pending。
		if markErr := store.MarkInstalled(rec.ID, adminID); markErr != nil {
			failures = append(failures, fmt.Sprintf("%s: 已装到 Dokku 但回写状态失败：%v", rec.Name, markErr))
			continue
		}
		success++
		fmt.Fprintf(w, "已安装：%s\n", rec.Name)
	}

	fmt.Fprintf(w, "代装完成：成功 %d / 失败 %d\n", success, len(failures))
	if len(failures) > 0 {
		fmt.Fprintln(w, "失败明细（保持 pending，可修复后重试）：")
		for _, f := range failures {
			fmt.Fprintf(w, "  - %s\n", f)
		}
	}
	return nil
}

// selectInstallTargets 依据 selectors 与 all 从 pending 列表挑出本次要代装的记录。
// 返回 (targets, listedOnly, err)：
//   - all=true → 全部 pending。
//   - 指定了 <名称|指纹> → 仅精确匹配（按 Name 或 Fingerprint）的 pending；无任一匹配则报错。
//   - 两者皆无 → 安全默认：把全部 pending 列出供审核，listedOnly=true（本次不代装任何）。
//
// 这是「不要无差别代装所有 pending」的安全闸门：管理员必须显式选择某条或显式 --all，
// 防止任意已登录用户经 provision 写入的 pending 被自动放行进 Dokku。
func selectInstallTargets(w io.Writer, pend []sshkeys.KeyRecord, selectors []string, all bool) (targets []sshkeys.KeyRecord, listedOnly bool, err error) {
	if all {
		return pend, false, nil
	}
	if len(selectors) > 0 {
		matched := make(map[string]bool, len(selectors))
		for _, rec := range pend {
			for _, sel := range selectors {
				if rec.Name == sel || rec.Fingerprint == sel {
					targets = append(targets, rec)
					matched[sel] = true
					break
				}
			}
		}
		for _, sel := range selectors {
			if !matched[sel] {
				fmt.Fprintf(w, "跳过：未找到待代装(pending)记录匹配 %q。\n", sel)
			}
		}
		if len(targets) == 0 {
			return nil, false, fmt.Errorf("没有匹配的待代装公钥；运行 `bk ssh-key install`（不带参数）可查看全部 pending")
		}
		return targets, false, nil
	}
	// 安全默认：仅列出 pending 供审核，不代装任何一条。
	fmt.Fprintf(w, "共有 %d 条待代装(pending)公钥，请审核后指定要代装的 <名称|指纹>，或用 --all 代装全部：\n", len(pend))
	for _, rec := range pend {
		fmt.Fprintf(w, "  - %s  %s  host=%s\n", rec.Name, rec.Fingerprint, rec.Host)
	}
	fmt.Fprintln(w, "示例： bk ssh-key install <名称>    或    bk ssh-key install --all")
	return nil, true, nil
}

// currentSessionUID 返回当前生效 profile 的会话用户 uid（auth.uid()），用作 installed_by。
// 复用 whoami.go 的 lookupProfile 从 auth.json 读取会话；缺失会话返回引导登录错误，使
// install 不会以空 by 回写（Requirement 7.1/10.3）。
func currentSessionUID(profile string) (string, error) {
	cfg := lookupProfile(authConfig, profile)
	if cfg == nil || cfg.Session.User.ID == "" {
		return "", fmt.Errorf("profile %s 未登录或会话缺少用户身份，请先运行 `bk auth login`", profile)
	}
	return cfg.Session.User.ID, nil
}

// sshKeyInstallCmd 是 `bk ssh-key install`（管理员代装）。装配真实 Store + dokku.Client 与
// 当前会话 uid 后委托 runSSHKeyInstall。用 RunE 使权限/连接错误以非零退出（Requirement 7.1）。
var sshKeyInstallAll bool

var sshKeyInstallCmd = &cobra.Command{
	Use:   "install [<指纹|名称>...]",
	Short: "（管理员）把指定的（或 --all 全部）pending 公钥代装到 Dokku 并回写 installed",
	Long: `读取状态为 pending 的公钥登记，借管理员既有的 SSH 接入在 Dokku 主机上为指定记录
执行 ssh-keys 添加（先移除同名再添加，幂等），成功后把该记录回写为 installed 并记录
安装者与安装时间。

选择要代装哪些（默认不做无差别代装，避免任意 provision 的 pending 被自动放行）：
- 指定一个或多个 <名称|指纹>：仅代装精确匹配的 pending 记录。
- --all：代装全部 pending。
- 不带任何参数且不带 --all：仅列出全部 pending 供审核，不代装任何一条。

行为：
- 非管理员被 RLS 拒绝时提示需要管理员权限并以非零退出码结束。
- 无任何待安装记录时给出友好提示并以零退出码结束。
- 单条失败（连接/命令/重复）只记录原因、保持该条 pending，并继续处理其余记录。
- 结束时汇总成功与失败的条目数。

输出不包含任何私钥内容。

示例用法：
  bk ssh-key install                       # 列出全部 pending 供审核（不代装）
  bk ssh-key install bk-alice-1-2-3-4 --sudo   # 仅代装指定名称
  bk ssh-key install SHA256:xxxx --sudo        # 仅代装指定指纹
  bk ssh-key install --all --sudo              # 代装全部 pending`,
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newSSHKeyStore(profile)
		if err != nil {
			return err
		}
		adminID, err := currentSessionUID(profile)
		if err != nil {
			return err
		}
		inst, err := newDokkuClient(profile)
		if err != nil {
			return err
		}
		defer inst.Close()
		return runSSHKeyInstall(cmd.Context(), os.Stdout, store, inst, adminID, args, sshKeyInstallAll)
	},
}

func init() {
	sshKeyInstallCmd.Flags().BoolVar(&sshKeyInstallAll, "all", false,
		"代装全部 pending 公钥（默认需指定 <名称|指纹>，或用本标志显式代装全部）")
	sshKeyCmd.AddCommand(sshKeyInstallCmd)
}
