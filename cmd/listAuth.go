/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"pkg.blksails.net/bk/internal/auth"
)

// listAuthCmd represents the listAuth command. It lists every saved profile in a
// table showing the profile name, user email, last sign-in time and creation
// time, marking the currently active profile (--profile) with a recognizable
// indicator. When no profiles are saved it prints a friendly empty-list message
// and exits zero. Token-bearing fields are never printed
// (Requirements 5.1, 5.2, 5.3, 5.4).
var listAuthCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "列出认证配置",
	Long: `列出所有保存的认证配置文件信息。

显示的信息包括：
- Profile: 配置文件名称（当前生效 profile 以 * 标注）
- Email: 用户邮箱
- Last Sign In At: 最后登录时间
- Created At: 创建时间

不显示 access token / refresh token 等会话敏感字段。
无任何已保存配置时输出友好的空列表提示。

示例用法：
  bk auth list
  bk auth ls`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runListAuth(cmd.OutOrStdout(), authConfig, profile)
	},
}

// runListAuth renders the saved auth profiles from authPath to w as a table.
// The row whose profile equals activeProfile is marked with a "*" indicator in a
// leading column so the active identity is recognizable (Requirement 5.2); other
// rows leave that column blank. When authPath holds no profiles (missing file or
// empty list) it writes a friendly empty-list message and returns nil rather than
// erroring (Requirement 5.3). Only the profile name, email and timestamps are
// printed — never access/refresh tokens (Requirement 5.4).
func runListAuth(w io.Writer, authPath, activeProfile string) error {
	configs, err := auth.LoadAuthConfig(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(w, emptyAuthListMessage)
			return nil
		}
		return fmt.Errorf("加载认证配置失败: %w", err)
	}

	if len(configs) == 0 {
		fmt.Fprintln(w, emptyAuthListMessage)
		return nil
	}

	tw := tabwriter.NewWriter(w, 1, 1, 1, ' ', 0)
	fmt.Fprintln(tw, "Current\tProfile\tEmail\tLast Sign In At\tCreated At")
	for _, config := range configs {
		if config == nil {
			continue
		}
		marker := ""
		if config.Profile == activeProfile {
			marker = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			marker,
			config.Profile,
			config.Session.User.Email,
			config.Session.User.LastSignInAt.Format(time.RFC3339),
			config.Session.User.CreatedAt.Format(time.RFC3339),
		)
	}
	return tw.Flush()
}

// emptyAuthListMessage is shown when there are no saved profiles, guiding the
// user to log in instead of erroring (Requirement 5.3).
const emptyAuthListMessage = "暂无已登录的 profile，运行 bk auth login 登录"

func init() {
	authCmd.AddCommand(listAuthCmd)
}
