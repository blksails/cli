package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"io"
	"pkg.blksails.net/bk/internal/vault"
	"strings"
)

type vaultListerFull interface {
	List(app string) ([]vault.Secret, error)
}

func runVaultExport(w io.Writer, app string, lister vaultListerFull) error {
	rows, err := lister.List(app)
	if err != nil {
		return fmt.Errorf("读取 app %q 的 secrets 失败：%w", app, err)
	}
	var out strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&out, "%s=%s\n", row.Key, row.Value)
	}
	_, err = io.WriteString(w, out.String())
	return err
}

var vaultExportCmd = &cobra.Command{Use: "export [app]", Short: "以 KEY=VALUE 格式输出当前用户的 app secrets", Args: vaultArgs(0, 0), RunE: func(cmd *cobra.Command, args []string) error {
	s, e := newVaultStore(cmd, profile)
	if e != nil {
		return e
	}
	app, _ := vaultScope(cmd, args)
	return runVaultExport(cmd.OutOrStdout(), app, s)
}}
