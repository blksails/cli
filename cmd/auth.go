/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	userEmail    string
	userPassword string
	authConfig   string
)

// authCmd represents the auth command
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "用户认证管理",
	Long: `用户认证管理命令，用于处理用户登录、认证等操作。

支持的功能：
- 用户登录认证
- 管理多个认证配置文件
- 保存和加载认证会话

示例用法：
  bk auth login -u user@example.com -p password
  bk auth login -u user@example.com  # 交互式输入密码`,
	// Run: func(cmd *cobra.Command, args []string) {
	// },
}

func init() {
	rootCmd.AddCommand(authCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// add short flag
	authCmd.PersistentFlags().StringVarP(&userEmail, "username", "u", "", "用户邮箱")
	authCmd.PersistentFlags().StringVarP(&userPassword, "password", "p", "", "用户密码")

	// authCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// authCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
	// home + ".local/bk/auth.json"
	home, err := os.UserHomeDir()
	if err != nil {
		log.Error("failed to get home directory", zap.Error(err))
		return
	}
	authConfig = home + "/.local/bk/auth.json"

}
