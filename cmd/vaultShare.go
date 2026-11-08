package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

type vaultSharer interface {
	Share(app string, keys []string) (int, error)
}

func runVaultShare(w io.Writer, app string, keys []string, store vaultSharer) error {
	count, err := store.Share(app, keys)
	if err != nil {
		return fmt.Errorf("共享密钥失败：%w", err)
	}
	_, err = fmt.Fprintf(w, "已将 %d 个密钥转为企业共享，个人条目已移除\n", count)
	return err
}

var vaultShareCmd = &cobra.Command{
	Use:   "share [app] KEY [KEY...]",
	Short: "将选定的个人密钥转为企业共享",
	Long: `将选定的个人密钥转为当前登录身份所在企业的同名 app 分组，并移除个人条目。
使用 --global 选择全局分组。企业有效成员可读，只有 owner/admin 可共享。
操作在服务端事务中完成；任一 key 不存在、源值变化或企业已有同名 key 时整批失败。

  bk vault ls --global
  bk vault share --global TOKEN API_KEY
  bk vault ls --global --share
  bk vault share myapp DB_PASSWORD`,
	Args: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("share") || cmd.Flags().Changed("personal") {
			return fmt.Errorf("share 子命令固定从个人转为企业共享，无需 --share 或 --personal")
		}
		if err := vaultArgs(1, -1)(cmd, args); err != nil {
			return err
		}
		_, keys := vaultScope(cmd, args)
		if len(keys) > 100 {
			return fmt.Errorf("每次最多共享 100 个 key")
		}
		for _, key := range keys {
			if key == "" {
				return fmt.Errorf("密钥名不能为空")
			}
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := newVaultStoreForOwnership(profile, "personal")
		if err != nil {
			return err
		}
		app, keys := vaultScope(cmd, args)
		return runVaultShare(cmd.OutOrStdout(), app, keys, store)
	},
}

func init() { vaultCmd.AddCommand(vaultShareCmd) }
