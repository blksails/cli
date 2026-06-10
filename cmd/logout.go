/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"pkg.blksails.net/bk/internal/auth"
)

// logoutCmd represents the logout command. It clears the active profile's
// session entry from ~/.local/bk/auth.json while leaving every other profile
// untouched (Requirement 4.1, 4.2). Logging out a profile that has no saved
// session is a friendly no-op that exits zero (Requirement 4.3).
var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "用户登出",
	Long: `用户登出命令，用于清除当前生效 profile 在本机保存的会话凭据。

功能特点：
- 仅清除当前生效 profile（--profile）的会话，其余 profile 不受影响
- 对未登录的 profile 执行登出时给出友好提示并正常退出（零退出码）

会话保存于 ~/.local/bk/auth.json。

示例用法：
  bk auth logout
  bk auth logout --profile production`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLogout(cmd.OutOrStdout(), authConfig, profile)
	},
}

// runLogout removes the given profile's session entry from authPath and reports
// the outcome to w. It detects whether the profile actually had a session so it
// can show a friendly "not logged in" message instead of falsely claiming a
// logout (Requirement 4.3 vs 4.4). It only returns a non-nil error on a real
// persistence failure; an absent profile or missing file is not an error.
func runLogout(w io.Writer, authPath, profile string) error {
	hadSession := profileHasSession(authPath, profile)

	if err := auth.RemoveAuthConfig(authPath, profile); err != nil {
		return fmt.Errorf("登出失败: %w", err)
	}

	if !hadSession {
		fmt.Fprintf(w, "profile %s 当前未登录，无需登出\n", profile)
		return nil
	}

	fmt.Fprintf(w, "已登出 profile %s\n", profile)
	return nil
}

// profileHasSession reports whether authPath currently holds a session entry for
// profile. A missing/unreadable file is treated as "no session" so logout stays
// a friendly no-op rather than surfacing a load error.
func profileHasSession(authPath, profile string) bool {
	configs, err := auth.LoadAuthConfig(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		// Any other read/parse problem: do not assume a session exists.
		return false
	}
	for _, c := range configs {
		if c != nil && c.Profile == profile {
			return true
		}
	}
	return false
}

func init() {
	authCmd.AddCommand(logoutCmd)
}
