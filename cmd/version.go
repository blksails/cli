/*
Copyright © 2025 BlackSails
*/
package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// 以下变量在构建时通过 -ldflags -X 注入（见 .goreleaser.yaml 与 Makefile）。
// 默认值用于 `go run` / `go install` 等未注入版本信息的场景。
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// versionString 返回适合展示的版本号（去掉 goreleaser 默认带的可能空值）。
func versionString() string {
	return version
}

// versionCmd 展示完整的版本/构建信息。
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "显示版本与构建信息",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("bk %s\n", version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", date)
		fmt.Printf("  go:     %s\n", runtime.Version())
		fmt.Printf("  os/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
