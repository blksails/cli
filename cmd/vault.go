package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"pkg.blksails.net/bk/internal/vault"
	"strings"
)

var vaultCmd = &cobra.Command{
	Use: "vault", Short: "Supabase Vault 凭据管理",
	Long: `使用 Supabase 内置 Vault 加密存储凭据，按当前登录用户和 app 隔离。
不需要本机 vault.key；通过受控 RPC 访问，不直接开放 Vault 明文视图。
set/get/list/rm/export/run 的命令用法保持不变。`,
	RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

func init() {
	vaultCmd.AddCommand(vaultSetCmd, vaultGetCmd, vaultExportCmd)
	rootCmd.AddCommand(vaultCmd)
}
func newVaultStore(profile string) (*vault.Store, error) {
	client, err := AuthedClientSchema(profile, "cli")
	if err != nil {
		return nil, err
	}
	return vault.NewStore(client), nil
}
func parseVaultKeyValue(s string) (key, value string, err error) {
	key, value, ok := strings.Cut(s, "=")
	if !ok || key == "" {
		return "", "", fmt.Errorf("参数格式错误：需为非空 KEY=VALUE")
	}
	return key, value, nil
}
