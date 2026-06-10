/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"pkg.blksails.net/bk/internal/auth"
)

// whoamiSkew is the safety margin applied when judging session validity, so a
// session that is about to expire is treated as already expired rather than
// reported as valid up to the last second.
const whoamiSkew = 30 * time.Second

// whoamiCmd represents the whoami command. It shows the current profile's
// identity and session state without ever printing token-bearing fields
// (Requirement 6.1–6.5). A valid session is marked 有效 with its expiry time; an
// expired session is marked 已过期 with a re-login/refresh hint; a profile with
// no saved session reports 未登录 and guides the user to `bk auth login`.
var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "查看当前身份与会话状态",
	Long: `查看当前生效 profile 对应的登录身份与会话状态。

显示内容：
- 当前生效 profile 名与已登录用户邮箱
- 会话有效时标注「有效」并显示过期时间
- 会话已过期时标注「已过期」并提示重新登录或刷新
- 当前 profile 未登录时给出登录引导

输出不包含 access token / refresh token 等会话敏感字段。

会话读取自 ~/.local/bk/auth.json。

示例用法：
  bk auth whoami
  bk auth whoami --profile production`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWhoami(cmd.OutOrStdout(), authConfig, profile, time.Now())
	},
}

// runWhoami loads authPath, finds the active profile's session entry and reports
// its identity and validity to w. The now argument is injected so validity is
// determined deterministically (via auth.IsExpiredAt) and is testable. A missing
// file or absent profile is not an error: it yields a friendly 未登录 message and
// a nil return (Requirement 6.4). The output never includes access/refresh
// tokens (Requirement 6.5).
func runWhoami(w io.Writer, authPath, profile string, now time.Time) error {
	cfg := lookupProfile(authPath, profile)
	if cfg == nil {
		fmt.Fprintf(w, "profile %s 当前未登录，请先运行 `bk auth login` 登录\n", profile)
		return nil
	}

	fmt.Fprintf(w, "profile: %s\n", cfg.Profile)
	fmt.Fprintf(w, "用户邮箱: %s\n", cfg.Session.User.Email)

	if auth.IsExpiredAt(cfg.Session, now, whoamiSkew) {
		fmt.Fprintln(w, "会话状态: 已过期")
		fmt.Fprintln(w, "请运行 `bk auth login` 重新登录，或刷新会话")
		return nil
	}

	fmt.Fprintln(w, "会话状态: 有效")
	fmt.Fprintf(w, "过期时间: %s\n", time.Unix(cfg.Session.ExpiresAt, 0).Format(time.RFC3339))
	return nil
}

// lookupProfile returns the auth config entry for profile, or nil when the file
// is missing/unreadable or the profile has no saved session. A read/parse
// problem is treated as "no session" so whoami stays a friendly 未登录 report
// rather than surfacing a load error (Requirement 6.4).
func lookupProfile(authPath, profile string) *auth.AuthConfig {
	configs, err := auth.LoadAuthConfig(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return nil
	}
	for _, c := range configs {
		if c != nil && c.Profile == profile {
			return c
		}
	}
	return nil
}

func init() {
	authCmd.AddCommand(whoamiCmd)
}
