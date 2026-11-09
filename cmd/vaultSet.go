package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"io"
	"strings"
)

type vaultSetter interface {
	Set(app, key, value string) error
}

func runVaultSet(w io.Writer, app string, pairs []string, setter vaultSetter) error {
	type pair struct{ key, value string }
	parsed := make([]pair, 0, len(pairs))
	for _, p := range pairs {
		k, v, err := parseVaultKeyValue(p)
		if err != nil {
			return err
		}
		parsed = append(parsed, pair{k, v})
	}
	for _, p := range parsed {
		if err := setter.Set(app, p.key, p.value); err != nil {
			return fmt.Errorf("写入 secret key %q 失败：%w", p.key, err)
		}
	}
	_, err := fmt.Fprintf(w, "已写入 %d 个密钥到 app %s\n", len(parsed), app)
	return err
}

func vaultSetArgs(cmd *cobra.Command, args []string) error {
	password, _ := cmd.Flags().GetBool("password")
	if !password {
		return vaultArgs(1, -1)(cmd, args)
	}
	if err := vaultArgs(1, 1)(cmd, args); err != nil {
		return err
	}
	_, keys := vaultScope(cmd, args)
	if keys[0] == "" || strings.Contains(keys[0], "=") {
		return fmt.Errorf("--password 仅接受一个非空 KEY，不要提供 KEY=VALUE")
	}
	return nil
}

func runVaultPasswordSet(w io.Writer, app, key string, setter vaultSetter, read func() ([]byte, error)) error {
	password, err := read()
	defer clear(password)
	if err != nil {
		return fmt.Errorf("读取密码失败，未写入密钥")
	}
	if len(password) == 0 {
		return fmt.Errorf("密码不能为空，未写入密钥")
	}
	return runVaultSet(w, app, []string{key + "=" + string(password)}, setter)
}

func readVaultPassword(cmd *cobra.Command) ([]byte, error) {
	input, ok := cmd.InOrStdin().(interface{ Fd() uintptr })
	if !ok || !term.IsTerminal(int(input.Fd())) {
		return nil, fmt.Errorf("--password 需要交互式终端")
	}
	if _, err := fmt.Fprint(cmd.ErrOrStderr(), "请输入密码（输入不回显）："); err != nil {
		return nil, err
	}
	password, err := term.ReadPassword(int(input.Fd()))
	fmt.Fprintln(cmd.ErrOrStderr())
	return password, err
}

var vaultSetCmd = &cobra.Command{
	Use:   "set [app] KEY=VALUE [KEY=VALUE...]",
	Short: "通过 Supabase Vault 加密并写入 secret",
	Long: `写入一个或多个 KEY=VALUE。使用 --password KEY 从终端隐藏输入单个密码。

  bk vault set --global --share --company blacksail --password ALIYUN_DOCKER_MIRROR_PASSWORD
  bk vault set myapp --password DB_PASSWORD`,
	Args: vaultSetArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := newVaultStore(cmd, profile)
		if err != nil {
			return err
		}
		app, pairs := vaultScope(cmd, args)
		password, _ := cmd.Flags().GetBool("password")
		if password {
			return runVaultPasswordSet(cmd.OutOrStdout(), app, pairs[0], s, func() ([]byte, error) { return readVaultPassword(cmd) })
		}
		return runVaultSet(cmd.OutOrStdout(), app, pairs, s)
	},
}

func init() {
	vaultSetCmd.Flags().Bool("password", false, "从交互式终端隐藏输入单个 KEY 的值")
}
