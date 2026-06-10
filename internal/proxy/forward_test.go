package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestParseForward(t *testing.T) {
	cases := []struct {
		spec       string
		wantLocal  int
		wantHost   string
		wantRemote int
		wantErr    bool
	}{
		{"8080:app.internal:80", 8080, "app.internal", 80, false},
		{"5432:5432", 5432, "127.0.0.1", 5432, false},
		{"abc:80", 0, "", 0, true},
		{"8080", 0, "", 0, true},
		{"8080:host:bad", 0, "", 0, true},
	}
	for _, c := range cases {
		f, err := ParseForward(c.spec)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseForward(%q) 期望出错，但成功", c.spec)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseForward(%q) 意外出错: %v", c.spec, err)
			continue
		}
		if f.LocalPort != c.wantLocal || f.RemoteHost != c.wantHost || f.RemotePort != c.wantRemote {
			t.Errorf("ParseForward(%q) = %+v, want local=%d host=%s remote=%d",
				c.spec, f, c.wantLocal, c.wantHost, c.wantRemote)
		}
	}
}

// TestForwardWithDirectDialer 用直连 Dialer 验证转发核心与传输机制无关：
// 起一个 echo 服务作为「远端」，经 proxy 转发后本地连接应能回显。
func TestForwardWithDirectDialer(t *testing.T) {
	// 远端 echo 服务
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动 echo 服务失败: %v", err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()

	// 本地监听端口（让系统分配）
	lp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占位本地端口失败: %v", err)
	}
	localPort := lp.Addr().(*net.TCPAddr).Port
	lp.Close()

	remotePort := echo.Addr().(*net.TCPAddr).Port
	fwd := Forward{LocalAddr: "127.0.0.1", LocalPort: localPort, RemoteHost: "127.0.0.1", RemotePort: remotePort}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = Run(ctx, DirectDialer(), []Forward{fwd}) }()

	// 等待本地监听就绪
	var conn net.Conn
	for i := 0; i < 50; i++ {
		conn, err = net.Dial("tcp", fwd.local())
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("连接本地转发端口失败: %v", err)
	}
	defer conn.Close()

	msg := []byte("hello-proxy")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	buf := make([]byte, len(msg))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("读取回显失败: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("回显不一致: got %q want %q", buf, msg)
	}
}

// rejectDialer 是始终返回固定拨号错误的 Dialer，用于断言 per-connection 拨号错误
// 被 onDialError 回调如实透出（R5.4），且整体转发不因单连接被拒而终止（R4.6）。
type rejectDialer struct{ err error }

func (d rejectDialer) DialContext(_ context.Context, _, _ string) (net.Conn, error) {
	return nil, d.err
}

// TestRunWithDialErrorHandlerSurfacesDialError 验证：当某个入站连接拨远端失败时，
// RunWithDialErrorHandler 以远端地址与拨号错误调用 onDialError（R5.4 透出原因），
// 且单连接被拒不终止整体转发——转发持续运行直到 ctx 取消才返回 nil（R4.6 隔离）。
func TestRunWithDialErrorHandlerSurfacesDialError(t *testing.T) {
	// 本地监听端口（让系统分配）。
	lp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占位本地端口失败: %v", err)
	}
	localPort := lp.Addr().(*net.TCPAddr).Port
	lp.Close()

	fwd := Forward{LocalAddr: "127.0.0.1", LocalPort: localPort, RemoteHost: "192.0.2.1", RemotePort: 9}
	rejectErr := errors.New("yamuxproxy: forward to 192.0.2.1:9 rejected: target_not_allowed")

	var mu sync.Mutex
	var gotAddr string
	var gotErr error
	gotCh := make(chan struct{}, 1)
	handler := func(remoteAddr string, e error) {
		mu.Lock()
		gotAddr, gotErr = remoteAddr, e
		mu.Unlock()
		select {
		case gotCh <- struct{}{}:
		default:
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- RunWithDialErrorHandler(ctx, rejectDialer{err: rejectErr}, []Forward{fwd}, handler)
	}()

	// 待本地监听就绪后拨入，触发一次失败拨号。
	var conn net.Conn
	for i := 0; i < 100; i++ {
		conn, err = net.Dial("tcp", fwd.local())
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("连接本地转发端口失败: %v", err)
	}
	conn.Close()

	// onDialError 应被以远端地址 + 拨号错误调用（R5.4）。
	select {
	case <-gotCh:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("onDialError 未在 3s 内被调用（拨号错误未透出）")
	}
	mu.Lock()
	if gotAddr != fwd.remote() {
		t.Errorf("onDialError 收到的远端地址错误: got %q want %q", gotAddr, fwd.remote())
	}
	if !errors.Is(gotErr, rejectErr) {
		t.Errorf("onDialError 应收到原始拨号错误，得到: %v", gotErr)
	}
	mu.Unlock()

	// R4.6 隔离：单连接被拒不终止整体转发——仍在运行，取消后才返回 nil。
	cancel()
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("单连接被拒不应使整体失败（R4.6）；取消后应返回 nil，得到: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("取消后 RunWithDialErrorHandler 未在 3s 内返回")
	}
}

// TestRunNilHandlerSilent 验证 Run（等价于 RunWithDialErrorHandler 的 nil handler）
// 保持原有静默行为：拨号失败仅关闭该连接、不 panic、不影响整体，取消后返回 nil。
func TestRunNilHandlerSilent(t *testing.T) {
	lp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占位本地端口失败: %v", err)
	}
	localPort := lp.Addr().(*net.TCPAddr).Port
	lp.Close()

	fwd := Forward{LocalAddr: "127.0.0.1", LocalPort: localPort, RemoteHost: "192.0.2.1", RemotePort: 9}

	ctx, cancel := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- Run(ctx, rejectDialer{err: errors.New("dial failed")}, []Forward{fwd}) }()

	var conn net.Conn
	for i := 0; i < 100; i++ {
		conn, err = net.Dial("tcp", fwd.local())
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("连接本地转发端口失败: %v", err)
	}
	// 静默行为：连接应被立即关闭（读到 EOF/零字节），无回显。
	conn.SetReadDeadline(time.Now().Add(time.Second))
	b := make([]byte, 1)
	if n, rerr := conn.Read(b); rerr == nil && n > 0 {
		t.Errorf("nil handler 时拨号失败不应有回显，读到 %d 字节", n)
	}
	conn.Close()

	cancel()
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("nil handler 取消后应返回 nil，得到: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("取消后 Run 未在 3s 内返回")
	}
}
