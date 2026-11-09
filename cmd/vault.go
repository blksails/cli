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
	Long: `使用服务端加密密钥库，按个人或企业归属以及 app 分组隔离。
使用 --global 选择 global 分组并省略 app；全局指所选归属内共享。
默认归属个人；--share 选择当前登录用户所在企业，服务端校验企业权限。
不需要本机 vault.key；主密钥只保存在服务器。
set/get/list/rm/export/run 的命令用法保持不变。`,
	RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

func init() {
	vaultCmd.PersistentFlags().String("company", "", "指定企业 namespace（配合 --share 或 share 子命令）")
	vaultCmd.PersistentFlags().Bool("global", false, "使用所选归属内的全局密钥分组（无需 app）")
	vaultCmd.AddCommand(vaultSetCmd, vaultGetCmd, vaultExportCmd)
	vaultCmd.PersistentFlags().Bool("personal", false, "使用个人密钥（默认）")
	vaultCmd.PersistentFlags().Bool("share", false, "共享给当前登录身份所在企业（成员可读，owner/admin 可写）")
	vaultCmd.MarkFlagsMutuallyExclusive("personal", "share")
	rootCmd.AddCommand(vaultCmd)
}
func newVaultStore(cmd *cobra.Command, profile string) (*vault.Store, error) {
	ownership := "personal"
	share, _ := cmd.Flags().GetBool("share")
	if share {
		ownership = "company"
	}
	namespace, _ := cmd.Flags().GetString("company")
	if cmd.Flags().Changed("company") && strings.TrimSpace(namespace) == "" {
		return nil, fmt.Errorf("企业 namespace 不能为空")
	}
	if namespace != "" && !share {
		return nil, fmt.Errorf("--company 需配合 --share 使用")
	}
	store, err := newVaultStoreForOwnership(profile, ownership)
	if err != nil {
		return nil, err
	}
	return store, store.SetCompanyNamespace(namespace)
}

func newVaultStoreForOwnership(profile, ownership string) (*vault.Store, error) {
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
			return vault.NewScopedStore(endpoint, current.Session.AccessToken, ownership)
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

// globalVaultApp is the namespace selected by --global.
const globalVaultApp = "global"

func vaultIsGlobal(cmd *cobra.Command) bool {
	global, _ := cmd.Flags().GetBool("global")
	return global
}

func vaultScope(cmd *cobra.Command, args []string) (string, []string) {
	if vaultIsGlobal(cmd) {
		return globalVaultApp, args
	}
	return args[0], args[1:]
}

func vaultArgs(minimum, maximum int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if !vaultIsGlobal(cmd) {
			minimumWithApp := minimum + 1
			if maximum < 0 {
				return cobra.MinimumNArgs(minimumWithApp)(cmd, args)
			}
			return cobra.RangeArgs(minimumWithApp, maximum+1)(cmd, args)
		}
		if maximum < 0 {
			return cobra.MinimumNArgs(minimum)(cmd, args)
		}
		return cobra.RangeArgs(minimum, maximum)(cmd, args)
	}
}

func vaultRunScope(cmd *cobra.Command, args []string) (string, []string, error) {
	expectedPrefix := 1
	if vaultIsGlobal(cmd) {
		expectedPrefix = 0
	}
	dash := cmd.ArgsLenAtDash()
	if len(args) < expectedPrefix+1 || (dash >= 0 && dash != expectedPrefix) {
		return "", nil, fmt.Errorf("用法：bk vault run <app> -- <command> [args...] 或 bk vault run --global -- <command> [args...]")
	}
	app, command := vaultScope(cmd, args)
	return app, command, nil
}
