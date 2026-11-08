package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"io"
)

type vaultGetter interface {
	Get(app, key string) (string, error)
}

func runVaultGet(w io.Writer, app, key string, getter vaultGetter) error {
	value, err := getter.Get(app, key)
	if err != nil {
		return fmt.Errorf("读取 secret key %q 失败：%w", key, err)
	}
	_, err = fmt.Fprintln(w, value)
	return err
}

var vaultGetCmd = &cobra.Command{Use: "get [app] KEY", Short: "读取当前用户的 secret，仅输出值", Args: vaultArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
	s, e := newVaultStore(cmd, profile)
	if e != nil {
		return e
	}
	app, keys := vaultScope(cmd, args)
	return runVaultGet(cmd.OutOrStdout(), app, keys[0], s)
}}
