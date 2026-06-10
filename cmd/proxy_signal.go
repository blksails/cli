package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// proxy_signal.go 提供 `bk proxy` 命令族的信号驱动可取消 context 助手
// （design：Component signalContext 行 298；requirements 6.1/6.2/6.3）。
//
// 边界（task 1.3）：本文件只提供 signalContext 助手本身；mirror/forward
// 命令在各自的 RunE 中调用它并 `defer stop()`（task 2.x），本文件不改动
// proxy.go / proxyMirror.go / proxyForward.go / internal/*。

// signalContext 基于父 context 派生一个在收到 SIGINT（os.Interrupt / Ctrl-C）
// 或 SIGTERM 时被取消的 context，供 mirror 与 forward 命令在阻塞运行期间
// 响应中断信号（requirements 6.1）。
//
// 信号触发后返回的 ctx 被取消（ctx.Err() == context.Canceled），既有核心
// （mirror.Run / proxy.Run）据「ctx 取消即正常退出」语义停止监听并收尾在途
// 连接（requirements 6.2/6.3）。
//
// 返回的 stop 函数释放信号监听并恢复默认信号行为；调用方应在 RunE 中以
// `ctx, stop := signalContext(cmd.Context()); defer stop()` 使用，从而在命令
// 结束时释放处理器（requirements 6.3）。stop 可安全地多次调用。
func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}
