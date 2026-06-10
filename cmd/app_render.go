/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

// app_render.go 承载 app 命令组的纯逻辑助手：KEY=VALUE / process=count 解析、
// 应用清单与环境变量的对齐表格渲染，以及统一的失败呈现与退出码语义
// （design「render（含解析与退出码助手）」；Requirement 5.1/5.3、8.1/8.2、12.1/12.3/12.4）。
//
// 边界（_Boundary: render_）：本文件仅包含可测纯助手，不装配连接、不调用远端、
// 不引入子命令。各 app 子命令（后续任务）复用这些助手完成解析/渲染/退出。
//
// 设计约束：助手保持纯/可测——解析器返回 (值, error)，渲染器写入调用方提供的
// io.Writer，失败助手把 error 写入给定 writer（stderr）并通过 appExitCode 暴露
// 非零退出码，而非在可测核心内直接调用 os.Exit。

// appParseKeyValues 把 `KEY=VALUE` 列表解析为映射（Requirement 5.1）。
// 空列表（含 nil）返回可读错误（5.2 由 cmd 层据此非零退出）；任一项不含 '='
// 或键为空返回可读错误（5.3）。允许 `KEY=`（空值）与值中含 '='（按首个 '=' 切分）。
func appParseKeyValues(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, fmt.Errorf("缺少配置项：至少需要一个 KEY=VALUE 参数")
	}
	result := make(map[string]string, len(pairs))
	for _, p := range pairs {
		key, val, ok := strings.Cut(p, "=")
		if !ok {
			return nil, fmt.Errorf("参数 %q 格式错误：需为 KEY=VALUE 形式", p)
		}
		if key == "" {
			return nil, fmt.Errorf("参数 %q 格式错误：键不能为空", p)
		}
		result[key] = val
	}
	return result, nil
}

// appParseProcessCount 解析 `process=count`（Requirement 8.1）。校验 process 非空、
// count 为非负整数（8.2）；缺 '='、空 process、空/非整数/负数 count 均返回可读错误。
func appParseProcessCount(arg string) (process string, count int, err error) {
	proc, cntStr, ok := strings.Cut(arg, "=")
	if !ok {
		return "", 0, fmt.Errorf("参数 %q 格式错误：需为 <process>=<count> 形式", arg)
	}
	if proc == "" {
		return "", 0, fmt.Errorf("参数 %q 格式错误：进程名不能为空", arg)
	}
	if cntStr == "" {
		return "", 0, fmt.Errorf("参数 %q 格式错误：缺少副本数", arg)
	}
	n, convErr := strconv.Atoi(cntStr)
	if convErr != nil {
		return "", 0, fmt.Errorf("参数 %q 格式错误：副本数 %q 不是整数", arg, cntStr)
	}
	if n < 0 {
		return "", 0, fmt.Errorf("参数 %q 格式错误：副本数不能为负数", arg)
	}
	return proc, n, nil
}

// appRenderTable 把行数据以对齐表格写入 w（design Service Interface：renderTable）。
// 复用 text/tabwriter，与既有 cmd 层渲染（如 sshKeyList）保持一致的视觉风格。
func appRenderTable(w io.Writer, headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 1, 1, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
}

// emptyAppsListMessage 在应用清单为空时展示友好提示而非空白（Requirement 1.2/12.1）。
const emptyAppsListMessage = "暂无应用"

// emptyConfigMessage 在应用无任何环境变量时展示友好提示而非空白（Requirement 4.2/12.1）。
const emptyConfigMessage = "该应用暂无环境变量"

// appRenderAppsTable 把应用清单 []string 渲染为对齐表格写入 w（Requirement 1.1/12.1）。
// 空清单不是错误：写出友好提示（调用方据此零退出）。
func appRenderAppsTable(w io.Writer, apps []string) {
	if len(apps) == 0 {
		fmt.Fprintln(w, emptyAppsListMessage)
		return
	}
	rows := make([][]string, 0, len(apps))
	for _, a := range apps {
		rows = append(rows, []string{a})
	}
	appRenderTable(w, []string{"App"}, rows)
}

// appRenderConfigTable 把环境变量映射 map[string]string 渲染为对齐的 KEY/VALUE 表格
// 写入 w（Requirement 4.1/12.1）。键按字典序排序以保证输出稳定。空映射不是错误：
// 写出友好提示（调用方据此零退出）。
func appRenderConfigTable(w io.Writer, env map[string]string) {
	if len(env) == 0 {
		fmt.Fprintln(w, emptyConfigMessage)
		return
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([][]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, []string{k, env[k]})
	}
	appRenderTable(w, []string{"KEY", "VALUE"}, rows)
}

// appFailMessage 把 error（已含 dokku stderr）写入给定 writer（stderr）（Requirement 12.3）。
// 拆分自退出逻辑以便单测；与 appExitCode 配合由 cmd 层完成「呈现 + 非零退出」。
func appFailMessage(w io.Writer, err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(w, err.Error())
}

// appExitCode 把 error 映射为退出码：nil→0（成功路径），非 nil→1（失败路径，
// 始终非零，便于脚本可靠判定成败）（Requirement 12.3/12.4）。
func appExitCode(err error) int {
	if err == nil {
		return 0
	}
	return 1
}
