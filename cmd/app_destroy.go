/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// app_destroy.go 承载 app destroy 的二次确认纯逻辑助手。
//
// 边界（_Boundary: confirmDestroy_）：本文件当前仅包含可无副作用单测的
// confirmDestroy 纯助手——不装配连接、不调用远端、不引入 cobra 子命令。
// app destroy 子命令（后续任务）将扩展本文件以复用此确认助手
// （design「destroy 二次确认流（R3）」/「confirmDestroy」；Requirement 3.1、3.2）。

// confirmDestroy 提示用户确认销毁 app：将含应用名的警示文本写入 out，
// 从 in 读取一行输入，去除首尾空白后——若等于应用名（大小写敏感，精确匹配）
// 或为肯定词（"y"/"yes"，大小写不敏感）则返回 true，否则（含空输入/EOF）返回 false。
//
// 本助手无副作用：返回 false 时调用方据此中止销毁且不得触达远端（Requirement 3.2）。
func confirmDestroy(in io.Reader, out io.Writer, app string) (bool, error) {
	// 展示将被销毁的应用名并要求交互式确认（Requirement 3.1）。
	fmt.Fprintf(out, "警告：即将销毁应用 %q，此操作不可逆。\n", app)
	fmt.Fprintf(out, "请输入应用名 %q 或 y/yes 确认销毁（其它输入将取消）：", app)

	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}

	answer := strings.TrimSpace(line)
	if answer == app {
		return true, nil
	}
	switch strings.ToLower(answer) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// appDestroyForce 承载 destroy 子命令的本地 --force 标志（默认 false）。
// 置为 true 时跳过二次确认直接销毁（Requirement 3.3）。
var appDestroyForce bool

// appDestroyer 抽象 destroy 所需的唯一写入缝：在远端销毁应用并返回 dokku 的结果文本。
// *dokku.Client 通过其 AppsDestroy 满足该接口，使 runAppDestroy 可注入 fake、
// 在不触达真实 SSH/Dokku 的前提下被验证。
type appDestroyer interface {
	AppsDestroy(context.Context, string) (string, error)
}

// runAppDestroy 是 destroy 的可测核心。
//
// 未带 --force（force=false）时先调用 confirmDestroy 做二次确认：确认未通过则
// 返回错误中止销毁、绝不调用 c.AppsDestroy（Requirement 3.2：不触达远端），
// 由命令层以非零退出码结束。
//
// 带 --force 或确认通过时以应用名调用 AppsDestroy 销毁应用，成功时把 dokku 返回的
// 结果文本原样写入 w 以展示结果并零退出（Requirement 3.3/3.4）。AppsDestroy 已把
// dokku stderr 拼入 error；当目标不存在或销毁被拒绝时以 %w 透传，由命令层非零退出
// （Requirement 3.6）。
//
// in 用于二次确认读取：生产路径由命令层传入 cmd.InOrStdin()，测试路径注入 reader。
func runAppDestroy(ctx context.Context, in io.Reader, w io.Writer, c appDestroyer, name string, force bool) error {
	if !force {
		confirmed, err := confirmDestroy(in, w, name)
		if err != nil {
			return fmt.Errorf("读取销毁确认失败：%w", err)
		}
		if !confirmed {
			// Requirement 3.2：拒绝即中止、不触达远端，并以非零退出码结束。
			return fmt.Errorf("已取消销毁应用 %q（未确认）", name)
		}
	}

	out, err := c.AppsDestroy(ctx, name)
	if err != nil {
		return fmt.Errorf("销毁应用 %q 失败：%w", name, err)
	}
	_, err = io.WriteString(w, out)
	return err
}

// appDestroyCmd 是 `bk app destroy <app>`（高危）。装配按当前 profile 连接的
// dokku.Client 后委托 runAppDestroy；RunE 保持轻薄，确认/销毁/展示/退出码语义均落在
// runAppDestroy。二次确认从 cmd.InOrStdin() 读取真实标准输入（测试时由核心注入 reader）。
//
// 采用 cobra.ExactArgs(1)：未提供应用名（0 参数）时由 cobra 提示参数错误并以
// 非零退出码结束（Requirement 3.5）。
var appDestroyCmd = &cobra.Command{
	Use:   "destroy <app>",
	Short: "销毁 Dokku 主机上的一个应用（高危，默认需二次确认）",
	Long: `连接当前 profile 指向的 Dokku 主机并销毁名为 <app> 的应用。

这是不可逆的高危操作：默认会展示将被销毁的应用名并要求交互式二次确认，
拒绝或输入不匹配时中止销毁、不对远端做任何更改，并以非零退出码结束。
使用 --force 可跳过交互式确认直接销毁（适用于脚本）。

未提供应用名、目标应用不存在或销毁被 Dokku 拒绝时，透传 dokku 的错误信息并以
非零退出码结束，便于脚本判定成败。

示例用法：
  bk app destroy myapp
  bk app destroy myapp --force`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := appClient(profile)
		if err != nil {
			return err
		}
		defer c.Close()
		return runAppDestroy(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), c, args[0], appDestroyForce)
	},
}

func init() {
	appDestroyCmd.Flags().BoolVar(&appDestroyForce, "force", false, "跳过二次确认直接销毁应用")
	appCmd.AddCommand(appDestroyCmd)
}
