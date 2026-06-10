// Package proxy 提供传输无关的本地端口转发核心。
//
// 设计：proxy 只负责「本地监听 → 接受连接 → 经 Dialer 到达远端 → 双向拷贝」这套
// 与具体传输机制无关的逻辑。「如何到达远端」被抽象为 Dialer 接口，由调用方注入，
// 隧道机制（自定义协议、SSH、yamux 等）后续单独设计，不在本包内耦合。
//
// 默认提供 DirectDialer（直连 net.Dialer）作为占位实现，便于本地联调与测试。
package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Dialer 抽象「到达远端目标」的传输机制。后续自定义隧道只需实现此接口即可接入，
// proxy 核心无需改动。net.Dialer 原生满足该签名。
type Dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// DirectDialer 是默认的直连实现（不经任何隧道），主要用于本地联调与测试，
// 也是「机制未接入」时的安全回退。
func DirectDialer() Dialer {
	return &net.Dialer{Timeout: 15 * time.Second}
}

// Forward 描述一条端口转发规则：本地 LocalAddr:LocalPort 转发到远端 RemoteHost:RemotePort。
// RemoteHost 的可达性由注入的 Dialer 决定（直连或经隧道）。
type Forward struct {
	LocalAddr  string // 本地监听地址，默认 127.0.0.1
	LocalPort  int
	RemoteHost string
	RemotePort int
}

// ParseForward 解析 "8080:app.internal:80" 或 "8080:80"（远端主机为默认目标）形式的转发表达式。
func ParseForward(spec string) (Forward, error) {
	parts := strings.Split(spec, ":")
	var f Forward
	f.LocalAddr = "127.0.0.1"
	switch len(parts) {
	case 2: // local:remotePort
		lp, err := strconv.Atoi(parts[0])
		if err != nil {
			return f, fmt.Errorf("无效的本地端口 %q", parts[0])
		}
		rp, err := strconv.Atoi(parts[1])
		if err != nil {
			return f, fmt.Errorf("无效的远端端口 %q", parts[1])
		}
		f.LocalPort, f.RemoteHost, f.RemotePort = lp, "127.0.0.1", rp
	case 3: // local:remoteHost:remotePort
		lp, err := strconv.Atoi(parts[0])
		if err != nil {
			return f, fmt.Errorf("无效的本地端口 %q", parts[0])
		}
		rp, err := strconv.Atoi(parts[2])
		if err != nil {
			return f, fmt.Errorf("无效的远端端口 %q", parts[2])
		}
		f.LocalPort, f.RemoteHost, f.RemotePort = lp, parts[1], rp
	default:
		return f, fmt.Errorf("无效的转发表达式 %q，应为 local:remote 或 local:host:remote", spec)
	}
	return f, nil
}

// local 返回本地监听地址。
func (f Forward) local() string {
	return net.JoinHostPort(f.LocalAddr, strconv.Itoa(f.LocalPort))
}

// remote 返回远端目标地址。
func (f Forward) remote() string {
	return net.JoinHostPort(f.RemoteHost, strconv.Itoa(f.RemotePort))
}

// String 返回人类可读的转发描述。
func (f Forward) String() string {
	return fmt.Sprintf("%s -> %s", f.local(), f.remote())
}

// Run 启动全部端口转发并阻塞，直到 ctx 取消或出错。dialer 为 nil 时使用 DirectDialer。
//
// Run 等价于 RunWithDialErrorHandler 传入 nil 处理器：per-connection 拨远端失败被
// 静默隔离（仅关闭该连接、不影响整体转发）。需要把单连接拨号失败原因呈现给用户时
// （如隧道被 Hub 拒绝，Requirement 5.4），改用 RunWithDialErrorHandler 注入处理器。
func Run(ctx context.Context, dialer Dialer, forwards []Forward) error {
	return RunWithDialErrorHandler(ctx, dialer, forwards, nil)
}

// RunWithDialErrorHandler 与 Run 相同，但额外接受 onDialError：当某个入站连接经
// dialer 拨远端失败时，以远端地址与错误回调它（onDialError 非 nil 时）。回调用于
// 把「单连接拨号失败」的原因呈现给用户（Requirement 5.4，如隧道被 Hub 拒绝），
// 而拨号失败本身仍按 Requirement 4.6 做单连接隔离：仅终止该连接，不影响其它在途
// 连接与后续监听，亦不从本函数返回。onDialError 为 nil 时行为与历史 Run 完全一致
// （静默隔离），故既有调用方与直连路径不受影响。
func RunWithDialErrorHandler(ctx context.Context, dialer Dialer, forwards []Forward, onDialError func(remoteAddr string, err error)) error {
	if len(forwards) == 0 {
		return fmt.Errorf("proxy: 未指定任何转发规则")
	}
	if dialer == nil {
		dialer = DirectDialer()
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(forwards))
	for _, f := range forwards {
		go func() { errCh <- runOne(ctx, dialer, f, onDialError) }()
	}

	var firstErr error
	for range forwards {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
			cancel()
		}
	}
	return firstErr
}

// runOne 运行单条转发：本地监听并为每个入站连接经 dialer 建立到远端的连接。
func runOne(ctx context.Context, dialer Dialer, f Forward, onDialError func(remoteAddr string, err error)) error {
	listener, err := net.Listen("tcp", f.local())
	if err != nil {
		return fmt.Errorf("proxy: 监听本地 %s 失败: %w", f.local(), err)
	}
	defer listener.Close()

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	var wg sync.WaitGroup
	for {
		local, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				wg.Wait()
				return nil // ctx 取消属正常退出
			}
			return fmt.Errorf("proxy: accept 失败: %w", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			handleConn(ctx, dialer, local, f.remote(), onDialError)
		}()
	}
}

func handleConn(ctx context.Context, dialer Dialer, local net.Conn, remoteAddr string, onDialError func(remoteAddr string, err error)) {
	defer local.Close()

	remote, err := dialer.DialContext(ctx, "tcp", remoteAddr)
	if err != nil {
		// 单连接失败不影响整体转发（Requirement 4.6）：仅关闭该连接并返回。
		// 但先把失败原因透出给上层呈现给用户（Requirement 5.4，如隧道被 Hub 拒绝）。
		if onDialError != nil {
			onDialError(remoteAddr, err)
		}
		return
	}
	defer remote.Close()

	done := make(chan struct{}, 2)
	go func() { io.Copy(remote, local); done <- struct{}{} }()
	go func() { io.Copy(local, remote); done <- struct{}{} }()
	<-done // 任一方向结束即关闭两端
}
