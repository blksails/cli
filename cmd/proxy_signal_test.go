package cmd

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

// TestSignalContext_CancelsOnSIGTERM 验证：在信号监听处于激活状态时收到
// SIGTERM，signalContext 返回的 ctx 会被取消，且 ctx.Err() 为 context.Canceled
// （requirements 6.1：中断信号→取消运行 context）。
//
// 注意：必须保证发出信号时处理器仍处于激活状态——先 raise，再在 ctx.Done()
// 之后才调用 stop()，否则 stop() 释放处理器会让默认行为（终止进程）杀掉测试。
func TestSignalContext_CancelsOnSIGTERM(t *testing.T) {
	ctx, stop := signalContext(context.Background())
	defer stop()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("failed to send SIGTERM to self: %v", err)
	}

	select {
	case <-ctx.Done():
		// 处理器仍激活，安全停止。
	case <-time.After(2 * time.Second):
		t.Fatal("ctx.Done() did not close within 2s after SIGTERM")
	}

	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx.Err() = %v, want context.Canceled", err)
	}
}

// TestSignalContext_StopReleasesHandler 验证：stop() 可在未收到信号时安全释放
// 信号处理器，且不会 panic；释放后 ctx 仍可由后续 stop() 重复调用而不出错
// （requirements 6.3：RunE 可经 defer 释放信号监听）。
func TestSignalContext_StopReleasesHandler(t *testing.T) {
	ctx, stop := signalContext(context.Background())

	// 释放处理器前 ctx 不应被取消。
	select {
	case <-ctx.Done():
		t.Fatalf("ctx was cancelled before any signal: %v", ctx.Err())
	default:
	}

	// 多次调用 stop() 应安全（幂等），不 panic。
	stop()
	stop()
}

// TestSignalContext_ParentCancelPropagates 验证：父 context 取消时派生 ctx
// 一并取消（context 派生语义保持）。
func TestSignalContext_ParentCancelPropagates(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, stop := signalContext(parent)
	defer stop()

	cancelParent()

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("ctx.Done() did not close within 2s after parent cancel")
	}

	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx.Err() = %v, want context.Canceled", err)
	}
}

// 确保 os.Interrupt 的引用在编译期可用（signalContext 应同时监听
// os.Interrupt/SIGINT 与 SIGTERM）。
var _ = os.Interrupt
