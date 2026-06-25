package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// hasSubcommand 报告 parent 是否已挂载名为 name 的子命令。
func hasSubcommand(parent *cobra.Command, name string) bool {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return true
		}
	}
	return false
}

// TestProxyCmdRegisteredOnRoot 验证 proxyCmd 已通过 init() 自注册到 rootCmd
// （Requirement 1.1：在根命令下提供 proxy 父命令）。
func TestProxyCmdRegisteredOnRoot(t *testing.T) {
	if proxyCmd.Use != "proxy" {
		t.Fatalf("proxyCmd.Use = %q, want \"proxy\"", proxyCmd.Use)
	}
	if !hasSubcommand(rootCmd, "proxy") {
		t.Fatalf("proxyCmd 未注册到 rootCmd；rootCmd 子命令应包含 \"proxy\"")
	}
}

// TestProxyCmdHasMirrorAndForward 验证 proxyCmd 下挂载了 mirror 与 forward 两个
// 子命令（Requirement 1.2）。
func TestProxyCmdHasMirrorAndForward(t *testing.T) {
	if !hasSubcommand(proxyCmd, "mirror") {
		t.Errorf("proxyCmd 缺少子命令 \"mirror\"")
	}
	if !hasSubcommand(proxyCmd, "forward") {
		t.Errorf("proxyCmd 缺少子命令 \"forward\"")
	}
}

// TestProxyCmdNoArgsShowsHelpNoError 验证无子命令执行 `bk proxy` 时显示帮助
// 而非报错退出（Requirement 1.3）：RunE 返回 nil 且向输出写入用法。
func TestProxyCmdNoArgsShowsHelpNoError(t *testing.T) {
	var out bytes.Buffer
	proxyCmd.SetOut(&out)
	proxyCmd.SetErr(&out)
	t.Cleanup(func() {
		proxyCmd.SetOut(nil)
		proxyCmd.SetErr(nil)
	})

	if proxyCmd.RunE == nil {
		t.Fatalf("proxyCmd.RunE 为 nil；无子命令时应渲染帮助")
	}
	if err := proxyCmd.RunE(proxyCmd, nil); err != nil {
		t.Fatalf("proxyCmd.RunE(无参数) 返回错误 %v，期望 nil（应显示帮助而非报错）", err)
	}
	got := out.String()
	if !strings.Contains(got, "proxy") {
		t.Errorf("帮助输出未包含命令名 \"proxy\"；输出：\n%s", got)
	}
	if !strings.Contains(got, "mirror") || !strings.Contains(got, "forward") {
		t.Errorf("帮助输出未同时列出 \"mirror\" 与 \"forward\"；输出：\n%s", got)
	}
}

// TestProxyHelpListsSubcommands 验证 `bk proxy --help` 在用法中列出 mirror/forward
// 两个子命令（Requirement 1.2/1.3）。
func TestProxyHelpListsSubcommands(t *testing.T) {
	var out bytes.Buffer
	proxyCmd.SetOut(&out)
	t.Cleanup(func() { proxyCmd.SetOut(nil) })

	if err := proxyCmd.Help(); err != nil {
		t.Fatalf("proxyCmd.Help() 返回错误 %v", err)
	}
	got := out.String()
	for _, name := range []string{"mirror", "forward"} {
		if !strings.Contains(got, name) {
			t.Errorf("--help 输出未列出子命令 %q；输出：\n%s", name, got)
		}
	}
}

// TestProxyPlaceholdersNotImplemented 验证子命令 RunE 已装配且在缺失必填配置时
// 明确报错而非静默成功。注：mirror（task 2.1）与 forward（task 2.2）已以真实逻辑
// 替换占位；mirror 在缺 hub 配置时先行报错，故此处仍以其 RunE 验证「非静默成功」。
// forward 的真实行为（参数校验/解析/运行）由 proxyForward_test.go 覆盖。
func TestProxyPlaceholdersNotImplemented(t *testing.T) {
	// 隔离：把 proxy hub 目录缓存指向不存在的临时路径，确保 resolveHubConfig 无目录可回退，
	// 从而「缺必填配置 → 报错」的前提成立（否则本机若存在 ~/.local/bk/proxyhub.json，
	// 会从目录补全配置使 mirror 继续执行）。
	origCache := proxyHubCache
	proxyHubCache = filepath.Join(t.TempDir(), "no-proxyhub.json")
	t.Cleanup(func() { proxyHubCache = origCache })

	if mirrorCmd.RunE == nil {
		t.Fatalf("mirrorCmd.RunE 为 nil")
	}
	if err := mirrorCmd.RunE(mirrorCmd, nil); err == nil {
		t.Errorf("mirrorCmd 缺必填配置应返回错误，得到 nil")
	}
	if forwardCmd.RunE == nil {
		t.Fatalf("forwardCmd.RunE 为 nil")
	}
}
