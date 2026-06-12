package sshx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Result 是一次远端命令执行的结果。
type Result struct {
	Stdout string
	Stderr string
}

// Run 在远端执行一条命令并返回结果。命令以单个字符串发送，由远端 shell 解析；
// 调用方应使用 RunArgs 来安全拼接带参数的命令。
//
// ctx 取消时会主动关闭会话以中断执行。
func (c *Client) Run(ctx context.Context, command string) (Result, error) {
	return c.run(ctx, command, nil)
}

// RunArgs 安全拼接命令与参数（对每个 arg 做 shell 转义）后执行。
// 例如 RunArgs(ctx, "apps:create", "my app") 会发送 apps:create 'my app'。
func (c *Client) RunArgs(ctx context.Context, args ...string) (Result, error) {
	return c.Run(ctx, ShellJoin(args))
}

// RunArgsStdin 同 RunArgs，但把 stdin 作为远端命令的标准输入透传给远端会话。
// 命令拼接复用 ShellJoin；输出捕获与 ctx 取消语义与 Run/RunArgs 完全一致。
// stdin 为 nil 时等价于 RunArgs。既有 Run/RunArgs 行为不受影响。
func (c *Client) RunArgsStdin(ctx context.Context, stdin io.Reader, args ...string) (Result, error) {
	return c.run(ctx, ShellJoin(args), stdin)
}

// RunStream 安全拼接命令与参数后执行，并把远端 stdout 实时写入 w（不缓冲），
// 适用于 `dokku logs --tail` 等持续输出的命令。stderr 仍缓冲，仅用于失败时的
// 错误信息。ctx 取消时主动关闭会话以中断流式执行（如用户 Ctrl-C）。
func (c *Client) RunStream(ctx context.Context, w io.Writer, args ...string) error {
	return c.runStream(ctx, ShellJoin(args), w)
}

// runStream 是 RunStream 的会话执行逻辑：把远端 stdout 直连到 w 实时输出，
// stderr 缓冲，并以 ctx 取消时主动关闭会话来中断执行。
func (c *Client) runStream(ctx context.Context, command string, w io.Writer) error {
	session, err := c.conn.NewSession()
	if err != nil {
		return fmt.Errorf("sshx: 创建会话失败: %w", err)
	}
	defer session.Close()

	var stderr bytes.Buffer
	session.Stdout = w
	session.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		return ctx.Err()
	case err := <-done:
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				return fmt.Errorf("%w: %s", err, msg)
			}
			return err
		}
		return nil
	}
}

// run 是 Run / RunArgsStdin 共享的会话执行逻辑：开会话、捕获 stdout/stderr、
// 可选设置 stdin，并以 ctx 取消时主动关闭会话来中断执行。
func (c *Client) run(ctx context.Context, command string, stdin io.Reader) (Result, error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return Result{}, fmt.Errorf("sshx: 创建会话失败: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if stdin != nil {
		session.Stdin = stdin
	}

	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		return Result{Stdout: stdout.String(), Stderr: stderr.String()}, ctx.Err()
	case err := <-done:
		res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = strings.TrimSpace(stdout.String())
			}
			if msg != "" {
				return res, fmt.Errorf("%w: %s", err, msg)
			}
			return res, err
		}
		return res, nil
	}
}

// ShellJoin 将参数拼成可被远端 POSIX shell 安全解析的单行命令。
func ShellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// shellQuote 对单个参数做 POSIX 单引号转义。
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// 仅含安全字符则无需引号。
	if isShellSafe(s) {
		return s
	}
	// 用单引号包裹，内部的单引号替换为 '\'' 。
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isShellSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
		default:
			switch r {
			case '-', '_', '.', '/', ':', '=', '@', '%', '+':
			default:
				return false
			}
		}
	}
	return true
}
