package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"io"
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

var vaultSetCmd = &cobra.Command{Use: "set [app] KEY=VALUE [KEY=VALUE...]", Short: "通过 Supabase Vault 加密并写入 secret", Args: vaultArgs(1, -1), RunE: func(cmd *cobra.Command, args []string) error {
	s, e := newVaultStore(cmd, profile)
	if e != nil {
		return e
	}
	app, pairs := vaultScope(cmd, args)
	if len(pairs) == 0 {
		return fmt.Errorf("缺少 KEY=VALUE")
	}
	return runVaultSet(cmd.OutOrStdout(), app, pairs, s)
}}
