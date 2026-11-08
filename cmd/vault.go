package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"os"
	"pkg.blksails.net/bk/internal/auth"
	"pkg.blksails.net/bk/internal/vault"
	"strings"
)

var vaultCmd = &cobra.Command{
	Use: "vault", Short: "BlackSail 统一密钥库",
	Long: `使用服务端加密密钥库，按当前登录用户和 app 隔离。
不需要本机 vault.key；主密钥只保存在服务器。
set/get/list/rm/export/run 的命令用法保持不变。`,
	RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

func init() {
	vaultCmd.AddCommand(vaultSetCmd, vaultGetCmd, vaultExportCmd)
	rootCmd.AddCommand(vaultCmd)
}
func newVaultStore(profile string) (*vault.Store, error) {
	if _, err := AuthedClient(profile); err != nil {
		return nil, err
	}
	configs, err := auth.LoadAuthConfig(authConfig)
	if err != nil {
		return nil, fmt.Errorf("无法读取登录状态")
	}
	endpoint := os.Getenv("BK_VAULT_URL")
	if endpoint == "" {
		endpoint = "https://blksails.cn/api/vault"
	}
	for _, current := range configs {
		if current != nil && current.Profile == profile {
			return vault.NewStore(endpoint, current.Session.AccessToken)
		}
	}
	return nil, fmt.Errorf("请先运行 bk auth login")
}
func parseVaultKeyValue(s string) (key, value string, err error) {
	key, value, ok := strings.Cut(s, "=")
	if !ok || key == "" {
		return "", "", fmt.Errorf("参数格式错误：需为非空 KEY=VALUE")
	}
	return key, value, nil
}
