/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"pkg.blksails.net/bk/internal/auth"
	"pkg.blksails.net/bk/internal/sshkeys"
)

// sshKeyProvision.go 实现 `bk ssh-key provision`：在本机生成 ed25519 密钥对、以 0600
// 落盘私钥，再把公钥/指纹/名称/主机以 pending 登记到 Supabase，并可选把 .bs.yaml 的
// ssh.identity 指向新私钥（design「provision 序列」；Requirement 1.4/1.5/2.1/2.4/2.5/3.1/3.2/3.3）。
//
// 边界（_Boundary: cmd/sshKeyProvision_）：本文件只承载 provision 子命令与其可测核心
// runProvision。复用 sshKey.go 的辅助（deriveKeyName / privateKeyPath / newSSHKeyStore），
// 不修改 sshKey.go / root.go / internal/*。
//
// 安全不变量（Requirement 10.1/10.2）：私钥仅经 sshkeys.WritePrivateKey 落盘（0600），
// 命令的任何输出/日志都只展示私钥「路径」、公钥与指纹，绝不打印私钥内容。

// keyRegisterer 抽象登记缝：仅需把一条 KeyRecord 登记为 pending 并回写 representation。
// *sshkeys.Store 满足该接口（其 Register 恒置 status=pending）。把它抽成接口使
// runProvision 的登记分支可注入 fake，无需触达真实 Supabase。
type keyRegisterer interface {
	Register(sshkeys.KeyRecord) (sshkeys.KeyRecord, error)
}

// provisionOpts 承载 runProvision 编排所需的全部已解析参数（host/私钥路径/force/派生
// 名称/目标 dokku 用户/是否请求改配置/公钥 comment）。由 cobra 层从标志与会话装配。
type provisionOpts struct {
	host                 string // 目标主机
	keyPath              string // 本机私钥落盘路径
	force                bool   // 允许覆盖已存在的私钥
	name                 string // 派生的可读名称（dokku ssh-keys 名）
	dokkuUser            string // 目标 dokku 用户，默认 "dokku"
	setIdentityRequested bool   // 是否请求把 ssh.identity 指向新私钥
	comment              string // 写入公钥 authorized line 的注释（一般 email+host）
}

// runProvision 是 provision 的可测核心，编排顺序严格对齐 design 的 provision 序列：
//
//  1. 私钥已存在且未 force → 返回错误（提示 --force），不 keygen、不登记。(Req 1.4)
//  2. GenerateKeyPair → WritePrivateKey(0600)；落盘失败 → 返回错误，不登记。(Req 1.5)
//  3. reg.Register(pending)；失败 → 返回错误并提示「私钥已生成但未登记，可重试」(Req 2.4)；
//     成功 → 向 w 确认指纹与状态 pending。(Req 2.5/2.1)
//  4. setIdentityRequested → setIdentity(keyPath)，失败仅告警不回滚 keygen/register (Req 3.3)；
//     未请求 → 向 w 提示私钥路径以便手动配置。(Req 3.2)
//
// w 仅承载面向用户的非敏感输出（路径/公钥/指纹/状态），绝不写入私钥内容（Req 10.2）。
func runProvision(w io.Writer, opts provisionOpts, reg keyRegisterer, setIdentity func(path string) error) error {
	// 1) 已存在且未 force：早退，不生成、不登记，避免覆盖正在使用的私钥。(Req 1.4)
	if !opts.force {
		if _, err := os.Stat(opts.keyPath); err == nil {
			return fmt.Errorf("私钥文件已存在：%s；如需覆盖请加 --force（不会自动登记）", opts.keyPath)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("检查私钥文件失败：%w", err)
		}
	}

	// 2) 生成密钥对并落盘（0600）。落盘失败 → 不登记。(Req 1.5)
	pair, err := sshkeys.GenerateKeyPair(opts.comment)
	if err != nil {
		return fmt.Errorf("生成密钥对失败：%w", err)
	}
	if err := sshkeys.WritePrivateKey(opts.keyPath, pair.PrivatePEM, opts.force); err != nil {
		// 不向 Supabase 登记任何公钥（Req 1.5）。
		return fmt.Errorf("写入私钥失败：%w", err)
	}

	// 3) 登记为 pending。owner 不由客户端发送（DB 端 auth.uid() + RLS）。
	rec, err := reg.Register(sshkeys.KeyRecord{
		Name:        opts.name,
		Host:        opts.host,
		DokkuUser:   opts.dokkuUser,
		PublicKey:   pair.PublicAuthLine,
		Fingerprint: pair.FingerprintSHA,
		Status:      sshkeys.StatusPending,
	})
	if err != nil {
		// 私钥已落盘但未登记：明确告知可重试登记，非零退出（Req 2.4）。
		return fmt.Errorf("公钥登记失败：本地私钥已生成但未登记，可重试 provision；原因：%w", err)
	}

	// 成功：向用户确认指纹与状态（Req 2.5）。绝不打印私钥（Req 10.2）。
	fmt.Fprintf(w, "已登记公钥：name=%s host=%s\n", rec.Name, rec.Host)
	fmt.Fprintf(w, "  指纹：%s\n", rec.Fingerprint)
	fmt.Fprintf(w, "  状态：%s\n", rec.Status)

	// 4) 可选更新 .bs.yaml ssh.identity；失败仅告警，不回滚已完成的 keygen/register（Req 3.1/3.2/3.3）。
	if opts.setIdentityRequested {
		if err := setIdentity(opts.keyPath); err != nil {
			fmt.Fprintf(w, "警告：更新 .bs.yaml ssh.identity 失败（不影响已生成的私钥与已登记的公钥）：%v\n", err)
		} else {
			fmt.Fprintf(w, "已将 .bs.yaml 的 ssh.identity 指向：%s\n", opts.keyPath)
		}
	} else {
		fmt.Fprintf(w, "私钥路径：%s（如需让 bk 直接使用，请加 --set-identity 或手动配置 ssh.identity）\n", opts.keyPath)
	}

	return nil
}

// provisionCmd 是 `bk ssh-key provision`。装配真实依赖后委托 runProvision：私钥路径由
// privateKeyPath(host) 解析；登记 store 由 newSSHKeyStore(profile) 装配；setIdentity 走
// viperSetIdentity 写回 .bs.yaml；归属邮箱取自当前 profile 会话（缺失时回退 --email）。
var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "在本机生成 SSH 密钥对并登记公钥（pending）",
	Long: `在本机生成一对 ed25519 密钥，私钥以 0600 落盘且永不离开本机，
仅把公钥、指纹、名称与目标主机登记到 Supabase（初始状态 pending），
等待管理员代装到 Dokku。

可选：
  --host          目标主机（默认取 .bs.yaml 的 ssh.host）
  --force         覆盖已存在的私钥（默认拒绝覆盖正在使用的私钥）
  --set-identity  把 .bs.yaml 的 ssh.identity 指向新生成的私钥`,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := provisionHost
		if host == "" {
			host = viper.GetString("ssh.host")
		}
		if host == "" {
			return errors.New("未指定目标主机：请用 --host 指定，或在 .bs.yaml 配置 ssh.host")
		}

		keyPath, err := privateKeyPath(host)
		if err != nil {
			return err
		}

		email := provisionEmail
		if email == "" {
			email, err = sessionEmail(authConfig, profile)
			if err != nil {
				return fmt.Errorf("无法确定归属邮箱：%w；可用 --email 指定", err)
			}
		}

		store, err := newSSHKeyStore(profile)
		if err != nil {
			return err
		}

		opts := provisionOpts{
			host:                 host,
			keyPath:              keyPath,
			force:                provisionForce,
			name:                 deriveKeyName(email, host),
			dokkuUser:            "dokku",
			setIdentityRequested: provisionSetIdentity,
			comment:              email + " " + host,
		}

		return runProvision(cmd.OutOrStdout(), opts, store, viperSetIdentity)
	},
}

var (
	provisionHost        string
	provisionForce       bool
	provisionSetIdentity bool
	provisionEmail       string
)

// sessionEmail 从指定 profile 的本地会话（auth.json）取归属邮箱，供派生密钥名称与公钥
// comment 使用。仅读取本机会话，不触网。
func sessionEmail(authPath, profile string) (string, error) {
	configs, err := auth.LoadAuthConfig(authPath)
	if err != nil {
		return "", fmt.Errorf("读取会话失败（请先 bk auth login）：%w", err)
	}
	for _, c := range configs {
		if c != nil && c.Profile == profile {
			if c.Session.User.Email == "" {
				return "", fmt.Errorf("profile %q 会话无邮箱信息", profile)
			}
			return c.Session.User.Email, nil
		}
	}
	return "", fmt.Errorf("未找到 profile %q 的会话（请先 bk auth login）", profile)
}

// viperSetIdentity 把全局 viper 的 ssh.identity 设为 path 并写回 .bs.yaml（Requirement 3.1）。
// 这是 provision 的真实 setIdentity 缝；失败由 runProvision 以告警呈现、不回滚（Req 3.3）。
func viperSetIdentity(path string) error {
	v := viper.GetViper()
	v.Set("ssh.identity", path)
	if v.ConfigFileUsed() == "" {
		// 无既有 .bs.yaml 时尽量安全写入（不覆盖已存在文件）。
		if err := v.SafeWriteConfig(); err != nil {
			return fmt.Errorf("写入配置失败：%w", err)
		}
		return nil
	}
	if err := v.WriteConfig(); err != nil {
		return fmt.Errorf("写入配置失败：%w", err)
	}
	return nil
}

func init() {
	sshKeyCmd.AddCommand(provisionCmd)

	provisionCmd.Flags().StringVar(&provisionHost, "host", "",
		"目标主机（默认取 .bs.yaml 的 ssh.host）")
	provisionCmd.Flags().BoolVar(&provisionForce, "force", false,
		"覆盖已存在的私钥（默认拒绝覆盖正在使用的私钥）")
	provisionCmd.Flags().BoolVar(&provisionSetIdentity, "set-identity", false,
		"把 .bs.yaml 的 ssh.identity 指向新生成的私钥")
	provisionCmd.Flags().StringVar(&provisionEmail, "email", "",
		"归属邮箱（默认取当前 profile 会话邮箱；用于派生密钥名称）")
}
