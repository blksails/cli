package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// proxyHelp_test.go 覆盖 task 3.2 PART C：mirror / forward 两子命令的 `--help`
// 须包含「用法行 + 关键标志 + 至少一个示例」（Requirement 1.4）。
//
// 验证策略：经 cobra 命令树渲染各子命令的帮助（cmd.Help() 写入捕获缓冲区），
// 断言输出同时包含 Usage 行、关键标志名与一段 Example 文本。Example 文本来自
// cobra 的 Example 字段——该字段缺失即帮助不含示例段，使本测试在 PART A
// 添加 Example 之前失败（TDD RED）。

// helpOutput 经 cobra 渲染 cmd 的帮助并返回完整文本。
// 使用 SetOut 重定向到缓冲区，t.Cleanup 还原，避免污染全局命令状态。
func helpOutput(t *testing.T, name string) string {
	t.Helper()
	cmd, _, err := rootCmd.Find([]string{"proxy", name})
	if err != nil {
		t.Fatalf("在命令树中查找 proxy %s 失败: %v", name, err)
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	t.Cleanup(func() { cmd.SetOut(nil) })
	if err := cmd.Help(); err != nil {
		t.Fatalf("proxy %s --help 渲染失败: %v", name, err)
	}
	return out.String()
}

// TestMirrorHelpHasUsageFlagsAndExample 验证 `bk proxy mirror --help` 含用法、
// 关键标志与至少一个示例（Requirement 1.4）。
func TestMirrorHelpHasUsageFlagsAndExample(t *testing.T) {
	out := helpOutput(t, "mirror")

	// 用法行：cobra 渲染的 "Usage:" 区块含命令路径 "proxy mirror"。
	if !strings.Contains(out, "Usage:") {
		t.Errorf("mirror --help 缺少 Usage 用法行；输出：\n%s", out)
	}
	if !strings.Contains(out, "proxy mirror") {
		t.Errorf("mirror --help 用法行未含命令路径 'proxy mirror'；输出：\n%s", out)
	}

	// 关键标志：mirror 专属与共享 hub 标志均应出现在 Flags/Global Flags 段。
	for _, flag := range []string{"--target", "--method", "--path", "--header", "--server", "--token", "--app"} {
		if !strings.Contains(out, flag) {
			t.Errorf("mirror --help 缺少关键标志 %q；输出：\n%s", flag, out)
		}
	}

	// 至少一个示例：cobra Example 字段渲染为 "Examples:" 段。
	if !strings.Contains(out, "Examples:") {
		t.Errorf("mirror --help 缺少 Examples 示例段（须设置 cobra Example 字段）；输出：\n%s", out)
	}
	// 示例须是一条可执行的 bk proxy mirror 调用。
	if !strings.Contains(out, "bk proxy mirror") {
		t.Errorf("mirror --help 示例段未含可执行示例 'bk proxy mirror ...'；输出：\n%s", out)
	}
}

// TestForwardHelpHasUsageFlagsAndExample 验证 `bk proxy forward --help` 含用法、
// 关键标志与至少一个示例（Requirement 1.4）。
func TestForwardHelpHasUsageFlagsAndExample(t *testing.T) {
	out := helpOutput(t, "forward")

	if !strings.Contains(out, "Usage:") {
		t.Errorf("forward --help 缺少 Usage 用法行；输出：\n%s", out)
	}
	if !strings.Contains(out, "proxy forward") {
		t.Errorf("forward --help 用法行未含命令路径 'proxy forward'；输出：\n%s", out)
	}

	// 关键标志：forward 专属 --direct 与共享 hub 标志。
	for _, flag := range []string{"--direct", "--server", "--token", "--app"} {
		if !strings.Contains(out, flag) {
			t.Errorf("forward --help 缺少关键标志 %q；输出：\n%s", flag, out)
		}
	}

	if !strings.Contains(out, "Examples:") {
		t.Errorf("forward --help 缺少 Examples 示例段（须设置 cobra Example 字段）；输出：\n%s", out)
	}
	if !strings.Contains(out, "bk proxy forward") {
		t.Errorf("forward --help 示例段未含可执行示例 'bk proxy forward ...'；输出：\n%s", out)
	}
}
