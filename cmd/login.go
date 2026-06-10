/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"os"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/supabase-community/gotrue-go/types"
	"github.com/supabase-community/supabase-go"
	"go.uber.org/zap"
	"golang.org/x/term"
	"pkg.blksails.net/bk/internal/auth"
)

// loginCmd represents the login command
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "用户登录",
	Long: `用户登录命令，用于验证用户身份并获取访问令牌。

功能特点：
- 支持邮箱密码登录
- 交互式密码输入（不显示密码字符）
- 自动保存认证会话到配置文件
- 支持多个配置文件管理

认证信息将保存到 ~/.local/bk/auth.json 文件中。

示例用法：
  bk auth login -u user@example.com -p password
  bk auth login -u user@example.com  # 交互式输入密码
  bk auth login -u user@example.com --profile production`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// 如果没有设置密码参数，则从控制台获取密码（交互式、不回显）。
		if userPassword == "" {
			pass, err := readPasswordInteractive()
			if err != nil {
				return fmt.Errorf("读取密码失败: %w", err)
			}
			userPassword = pass
		}

		return runLoginWith(authConfig, profile, userEmail, userPassword, supabaseSignIn)
	},
}

// readPasswordInteractive prompts for a password on stderr and reads it from the
// terminal without echoing the typed characters (Requirement 3.2). The prompt is
// written to stderr so it does not pollute stdout for scripted consumers.
func readPasswordInteractive() (string, error) {
	fmt.Fprint(os.Stderr, "Enter password: ")
	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr) // 换行
	if err != nil {
		return "", err
	}
	return string(bytePassword), nil
}

// supabaseSignIn is the production sign-in seam: it builds the default Supabase
// client and performs an email/password sign-in. It is assigned to a function
// variable so tests can inject a fake without contacting Supabase.
func supabaseSignIn(email, password string) (types.Session, error) {
	apiUrl := viper.GetString("api_endpoint")
	apiKey := viper.GetString("api_key")
	// Do not log the api_key in plaintext; if it must appear in diagnostics it
	// is masked (Requirements 11.1, 11.4).
	log.Info("connecting to api", zap.String("apiUrl", apiUrl), zap.String("apiKey", auth.MaskToken(apiKey)))

	client, err := supabase.NewClient(apiUrl, apiKey, &supabase.ClientOptions{Schema: schema})
	if err != nil {
		return types.Session{}, fmt.Errorf("初始化客户端失败: %w", err)
	}
	return client.SignInWithEmailPassword(email, password)
}

// runLoginWith performs the sign-in via the injected signIn seam and, only on
// success, persists the resulting session to the active profile. On any failure
// it returns a clear, non-nil error and never writes or truncates the existing
// auth.json (Requirements 3.1, 3.3, 3.5). Returning an error makes the cobra
// command exit non-zero while keeping the flow testable.
func runLoginWith(authPath, profile, email, password string, signIn func(email, password string) (types.Session, error)) error {
	session, err := signIn(email, password)
	if err != nil {
		// Failure guard: do NOT touch auth.json. Surface a clear reason.
		return fmt.Errorf("登录失败: %w", err)
	}

	// Do not print the full session (it carries access/refresh tokens). Only
	// non-sensitive fields are surfaced to the user (Requirements 11.1, 11.2).
	fmt.Println(loginSuccessMessage(profile, session.User.Email))

	if err := persistLogin(authPath, profile, session); err != nil {
		return fmt.Errorf("保存会话失败: %w", err)
	}
	log.Info("add auth config success", zap.String("profile", profile))
	return nil
}

// persistLogin maps a Supabase session onto the local auth config and persists
// it for the given profile. It relies on auth.AddAuthConfig, which overwrites an
// existing entry with the same profile (no duplicate append) and auto-creates
// the parent directory when missing (Requirements 3.3, 3.4, 3.6).
func persistLogin(authPath, profile string, session types.Session) error {
	return auth.AddAuthConfig(authPath, &auth.AuthConfig{
		Profile: profile,
		Session: auth.Session{
			AccessToken:  session.AccessToken,
			RefreshToken: session.RefreshToken,
			TokenType:    session.TokenType,
			ExpiresIn:    int64(session.ExpiresIn),
			ExpiresAt:    session.ExpiresAt,
			User: auth.User{
				ID:    session.User.ID.String(),
				Role:  session.User.Role,
				Email: session.User.Email,
			},
		},
	})
}

// loginSuccessMessage builds the message shown to the user after a successful
// login. It deliberately includes only non-sensitive information (the active
// profile name and the user's email) and never any token-bearing field
// (Requirements 3.3, 11.1, 11.2).
func loginSuccessMessage(profile, email string) string {
	return fmt.Sprintf("Login successful (profile: %s, user: %s)", profile, email)
}

func init() {
	authCmd.AddCommand(loginCmd)
}
