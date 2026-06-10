package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"pkg.blksails.net/bk/internal/proxy"
)

// proxyForward_integration_test.go 是 task 3.1 的主交付：forward 直连路径的端到端
// 集成验证（Requirements 4.1/4.3、5.2、6.4）。
//
// 与 proxyForward_test.go 的单元用例（注入 fake run 间谍、不触达真实端口）不同，
// 本文件驱动 cmd 层的可测核心 runForward + 真实 proxy.DirectDialer() + 真实
// proxy.Run，在本机回环上完整跑通：
//
//	本地监听端口  --（DirectDialer 直连）-->  本地 echo 服务器
//
// 然后通过本地监听端口写入字节并读回，断言数据经转发双向回显正确（Req 4.1/5.5 数据流），
// 再取消 ctx 断言 runForward 返回 nil（信号触发的零退出，Req 6.4）且本地端口被释放
// （监听器在 ctx 取消后关闭，端口可被重新绑定）。全程不接入任何 Hub（纯直连，Req 5.2）。
//
// 设计依据（design.md「forward 模式」流程图 行 149-154）：当 hub 配置不全且未
// --direct 时，selectForwardDialer 走「否」分支 → DirectDialer → proxy.Run；这是
// 直连路径，零退出，不报错。本测试即覆盖该直连分支的运行期行为。

// SPEC 澄清（design 优先于 tasks.md 散文，见 task 3.1 提示）：
// tasks.md 称「缺 --app 时 mirror 与 forward 均非零退出」，对 FORWARD 不精确。
// 依 Requirement 5.2 与 design.md 流程（行 149-153）：forward 在 hub 配置不全
// （如缺 --app）且无 --direct 时，回退 DirectDialer（直连模式，零退出），并不报错；
// 只有 MIRROR 必须有 --app，缺失即非零退出。下方 TestSelectForwardDialerMissingApp...
// 即固化此 design 主导的区分：forward 缺 app（hubErr != nil）→ 选直连，不报错。

// startEchoServer 在 127.0.0.1 的临时端口上启动一个 TCP echo 服务器：每个入站连接
// 把读到的字节原样写回（io.Copy(conn, conn)）。返回其 host:port 与一个停止函数。
func startEchoServer(t *testing.T) (host string, port int, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动 echo 服务器监听失败: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // 监听器关闭即退出 accept 循环
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c) // 原样回显
			}(conn)
		}
	}()

	stop = func() {
		_ = ln.Close()
		wg.Wait()
	}
	t.Cleanup(stop)

	tcpAddr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", tcpAddr.Port, stop
}

// freeTCPPort 申请一个本机临时端口随即释放，返回端口号供 forward 本地监听使用。
// 存在短暂的 TOCTOU 窗口，但回环上的临时端口分配在测试中足够稳定。
func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("申请临时端口失败: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// waitListenable 轮询直到 addr 可被拨通或超时，用于判断 forward 本地监听器已就绪。
func waitDialable(t *testing.T, addr string, timeout time.Duration) net.Conn {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			return conn
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待 forward 本地端口 %s 就绪超时", addr)
	return nil
}

// TestForwardDirectPathEndToEnd 是 task 3.1 主交付：直连路径端到端集成验证。
//
// 步骤：
//  1. 启动本地 echo 服务器（临时端口）。
//  2. 选一个空闲本地端口，构造转发 "<localPort>:127.0.0.1:<echoPort>"。
//  3. 在 goroutine 中以真实 DirectDialer + 真实 proxy.Run 驱动 runForward（可取消 ctx）。
//  4. 待本地监听就绪，拨入本地端口、写入并读回字节，断言经转发双向回显正确（Req 4.1/5.5）。
//  5. 取消 ctx，断言 runForward 返回 nil（零退出，Req 6.4）。
//  6. 断言本地端口被释放（取消后可重新绑定同端口）。
func TestForwardDirectPathEndToEnd(t *testing.T) {
	echoHost, echoPort, _ := startEchoServer(t)

	localPort := freeTCPPort(t)
	spec := fmt.Sprintf("%d:%s:%d", localPort, echoHost, echoPort)
	localAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var buf bytes.Buffer
	runErrCh := make(chan error, 1)
	go func() {
		// 直连路径：DirectDialer + 真实 proxy.Run（阻塞至 ctx 取消）。传输标签「直连」。
		runErrCh <- runForward(ctx, &buf, proxy.DirectDialer(), "直连", []string{spec}, proxy.Run)
	}()

	// (4) 待本地监听就绪后拨入，验证双向数据流。
	conn := waitDialable(t, localAddr, 3*time.Second)
	defer conn.Close()

	payload := []byte("hello-forward-direct-path")
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("向 forward 本地端口写入失败: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("从 forward 本地端口读回失败: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("经直连转发的回显不匹配：写入 %q，读回 %q", payload, got)
	}
	_ = conn.Close()

	// (5) 取消 ctx → proxy.Run 停止监听、收尾在途 → runForward 返回 nil（Req 6.4）。
	cancel()
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("ctx 取消后 runForward 应返回 nil（零退出，Req 6.4），得到: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后 runForward 未在 3s 内返回（应优雅退出）")
	}

	// (6) 端口释放：取消后应能重新绑定同一本地端口（监听器已关闭，Req 6.2/6.4）。
	if !waitRebindable(t, localAddr, 2*time.Second) {
		t.Fatalf("ctx 取消后本地端口 %s 未被释放（无法重新绑定）", localAddr)
	}
}

// waitRebindable 轮询直到 addr 可被重新 net.Listen 绑定或超时，用于断言端口已释放。
func waitRebindable(t *testing.T, addr string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			_ = ln.Close()
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// 注：FORWARD「缺 --app（hubErr != nil）且未 --direct → 回退 DirectDialer、不报错」
// 这一 design 主导的语义（见文件头 SPEC 澄清，Req 5.2），已由
// proxyForward_test.go:TestSelectForwardDialerIncompleteHubFallback 覆盖，本文件不重复，
// 仅在文件头注释中明确其与 MIRROR 缺 --app 即非零退出的区别。
