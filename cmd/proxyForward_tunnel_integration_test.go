package cmd

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"pkg.blksails.net/bk/internal/proxy"
	"pkg.blksails.net/bk/internal/tunnel"
	"pkg.blksails.net/yamuxproxy"
)

// proxyForward_tunnel_integration_test.go 是 task 3.2 PART B 的主交付：
// forward 经 yamux 隧道的全链路集成验证（Requirements 5.1/5.4/5.5）。
//
// 与 proxyForward_integration_test.go（直连路径）不同，本文件在进程内启动一个
// 真实的 yamuxproxy.Hub（开启 forwarding：MaxForwarders>0 且 ForwardTargets
// allowlist 含 echo 地址），并驱动 cmd 层的真实路径：
//
//	selectForwardDialer(hub, nil, false, tunnel.New)  →  隧道 Dialer + closer
//	runForward(ctx, …, dialer, "隧道", specs, proxy.Run)
//
// 全链路：本地监听端口 --(隧道 Dialer)--> yamux 隧道 --> Hub --> echo 目标。
//
// 覆盖两个场景：
//   - 隧道可用（TestForwardThroughHubTunnelEndToEnd）：目标在 allowlist 内，
//     经本地端口写入字节并读回，断言双向数据流（FULL LINK，R5.1）；取消 ctx 后
//     runForward 返回 nil（信号触发零退出，R6.x），关闭隧道 closer。
//   - 隧道被拒（TestForwardThroughHubRejectSurfacesReason）：目标不在 allowlist 内，
//     断言隧道 Dialer 的 DialContext 透出含 Hub 拒绝原因的错误（R5.4）。
//
// SPEC 澄清（design.md 行 163/291，R4.6）：forward 拨远端被 Hub 拒绝属「运行期
// 单连接错误」，由既有核心 proxy.Run/handleConn 做单连接隔离（拨号失败仅关闭该
// 连接、不终止整体转发，亦不从 proxy.Run 返回）。因此 Hub 拒绝原因在「隧道 Dialer
// 的 DialContext」层如实透出（R5.4 的实质），而不会从 runForward 返回——后者按
// R4.6 继续运行。本文件据此在 Dialer 层断言拒绝原因，并在 cmd 层断言被拒连接被
// 立即关闭（本地侧读到 EOF/重置、零字节），二者共同固化「被拒场景」的可观测行为。

// selfSignedHubTLS 为进程内 Hub 生成自签名 TLS 证书（与 internal/tunnel 的
// tunnel_test.go:selfSignedTLS 等价：客户端以 Insecure=true 跳过校验）。
func selfSignedHubTLS(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("生成证书失败: %v", err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair 失败: %v", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
}

// startForwardHub 启动一个进程内 Hub，为 app "dev" 开启 forwarding，allowlist 为
// allowTargets（含/不含 echo 地址由调用方决定）。返回 Hub 与停止函数（已注册 Cleanup）。
func startForwardHub(t *testing.T, allowTargets []string) (*yamuxproxy.Hub, func()) {
	t.Helper()
	hub, err := yamuxproxy.NewHub(yamuxproxy.HubOptions{
		ListenAddress: "127.0.0.1:0",
		TLSConfig:     selfSignedHubTLS(t),
		SharedToken:   "tok",
		Apps: []yamuxproxy.AppConfig{{
			ID:                     "dev",
			MaxProducers:           1,
			MaxConsumers:           1,
			MaxRulesPerConsumer:    1,
			MaxInflightPerConsumer: 1,
			MaxForwarders:          2,            // 开启 forwarding（R5.1/5.5）
			ForwardTargets:         allowTargets, // allowlist（R5.4/5.5 由 Hub 执行）
		}},
	})
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	if err := hub.Start(context.Background()); err != nil {
		t.Fatalf("hub.Start: %v", err)
	}
	stop := func() { _ = hub.Stop(context.Background()) }
	t.Cleanup(stop)
	return hub, stop
}

// TestForwardThroughHubTunnelEndToEnd 是 PART B 主交付（隧道可用场景，R5.1）：
// 经 cmd 层真实路径 selectForwardDialer(tunnel.New) + runForward(proxy.Run)
// 跑通「本地端口 → 隧道 → Hub → echo」全链路，断言双向数据回显正确；取消 ctx 后
// runForward 返回 nil（R6.x），关闭隧道 closer。
func TestForwardThroughHubTunnelEndToEnd(t *testing.T) {
	echoHost, echoPort, _ := startEchoServer(t) // 复用 proxyForward_integration_test.go 的 echo 助手
	echoAddr := net.JoinHostPort(echoHost, strconv.Itoa(echoPort))

	hub, _ := startForwardHub(t, []string{echoAddr}) // echo 在 allowlist 内

	// cmd 层：由 hubConfig 经 selectForwardDialer(tunnel.New) 建立隧道 Dialer。
	hub2 := hubConfig{
		Server:   hub.Addr().String(),
		Token:    "tok",
		App:      "dev",
		Insecure: true, // 跳过自签名 Hub 证书校验（仅测试）
	}
	dialer, closer, transport, err := selectForwardDialer(hub2, nil, false, tunnel.New)
	if err != nil {
		t.Fatalf("selectForwardDialer 建立隧道失败: %v", err)
	}
	if transport != "隧道" {
		t.Fatalf("传输标签应为 隧道，得到: %q", transport)
	}
	if closer == nil {
		t.Fatal("隧道分支应返回非 nil closer（供退出时关闭 yamux 会话）")
	}
	t.Cleanup(func() { _ = closer.Close() })

	localPort := freeTCPPort(t)
	spec := strconv.Itoa(localPort) + ":" + echoHost + ":" + strconv.Itoa(echoPort)
	localAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var buf bytes.Buffer
	runErrCh := make(chan error, 1)
	go func() {
		// 真实 cmd 核心：runForward + 隧道 Dialer + 真实 proxy.Run。
		runErrCh <- runForward(ctx, &buf, dialer, transport, []string{spec}, proxy.Run)
	}()

	// 待本地监听就绪后拨入，验证经「隧道 → Hub → echo」的双向数据流（R5.1）。
	conn := waitDialable(t, localAddr, 5*time.Second)
	defer conn.Close()

	payload := []byte("hello-through-hub-tunnel")
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("向 forward 本地端口写入失败: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("从 forward 本地端口读回失败（隧道全链路）: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("经隧道转发的回显不匹配：写入 %q，读回 %q", payload, got)
	}
	_ = conn.Close()

	// 取消 ctx → proxy.Run 优雅停止 → runForward 返回 nil（R6.x）。
	cancel()
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("ctx 取消后 runForward 应返回 nil（零退出，R6.x），得到: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 取消后 runForward 未在 5s 内返回（应优雅退出）")
	}
}

// TestForwardThroughHubRejectSurfacesReason 是 PART B 被拒场景（R5.4/5.5）：
// 目标不在 Hub 的 ForwardTargets allowlist 内时，Hub 拒绝该 forward 拨号。
//
// 断言两件事：
//  1. 隧道 Dialer 的 DialContext 如实透出含 Hub 拒绝原因的错误（R5.4）——
//     原因子串为 "rejected" 与 "target_not_allowed"（yamuxproxy forwarder.go 的
//     拒绝错误格式 "yamuxproxy: forward to %s rejected: %s"，reason=target_not_allowed）。
//  2. 经 cmd 层 runForward + proxy.Run 驱动被拒目标时，被拒连接被立即关闭（本地侧
//     读到零字节并出错），且 runForward 按 R4.6 单连接隔离继续运行（不因单次拨号
//     被拒而整体失败）——取消 ctx 后正常返回 nil。
//
// 即：Hub 拒绝原因由隧道 Dialer 层如实呈现（R5.4 的实质，bk 不绕过 Hub 策略 R5.5），
// 而非由 runForward 返回——后者依既有核心做单连接隔离（design 行 291，R4.6）。
func TestForwardThroughHubRejectSurfacesReason(t *testing.T) {
	echoHost, echoPort, _ := startEchoServer(t)
	echoAddr := net.JoinHostPort(echoHost, strconv.Itoa(echoPort))

	// allowlist 仅含 echoAddr；下面拨一个不在其中的目标 → Hub 拒绝。
	hub, _ := startForwardHub(t, []string{echoAddr})

	hub2 := hubConfig{
		Server:   hub.Addr().String(),
		Token:    "tok",
		App:      "dev",
		Insecure: true,
	}
	dialer, closer, transport, err := selectForwardDialer(hub2, nil, false, tunnel.New)
	if err != nil {
		t.Fatalf("selectForwardDialer 建立隧道失败: %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })

	// (1) 直接经隧道 Dialer 拨一个不在 allowlist 的目标，断言 Hub 拒绝原因如实透出（R5.4）。
	notAllowed := "127.0.0.1:1" // 任意不在 allowlist 内的目标
	dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dcancel()
	_, derr := dialer.DialContext(dctx, "tcp", notAllowed)
	if derr == nil {
		t.Fatal("拨非 allowlist 目标应被 Hub 拒绝，但 DialContext 成功了")
	}
	msg := derr.Error()
	// 断言含可识别的 Hub 拒绝原因子串（R5.4：呈现 Hub 拒绝原因）。
	if !strings.Contains(msg, "rejected") {
		t.Errorf("拒绝错误应含 'rejected'（Hub 拒绝原因）；得到: %v", derr)
	}
	if !strings.Contains(msg, "target_not_allowed") {
		t.Errorf("拒绝错误应含 Hub 给出的原因 'target_not_allowed'（allowlist 外目标）；得到: %v", derr)
	}

	// (2) 经 cmd 层真实 wiring 驱动被拒目标：runForward 调用包了
	//     proxy.RunWithDialErrorHandler 的闭包（handler 写入 errBuf），断言被拒连接
	//     的「呈现给用户」输出同时含 Hub 拒绝原因（R5.4）与 Hub 策略说明（R5.4/R5.5）；
	//     并按 R4.6 单连接隔离继续运行（不整体失败），取消后返回 nil。
	localPort := freeTCPPort(t)
	spec := strconv.Itoa(localPort) + ":127.0.0.1:1" // 远端 127.0.0.1:1 不在 allowlist
	localAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf bytes.Buffer
	var errBuf bytes.Buffer
	var errMu sync.Mutex
	errReady := make(chan struct{}, 1)

	// 真实 cmd wiring：RunE 注入的正是这样一个调用 RunWithDialErrorHandler 的闭包，
	// handler 把 forwardDialErrorMessage 写到 cmd.ErrOrStderr()（此处用 errBuf 捕获）。
	run := func(rctx context.Context, d proxy.Dialer, fs []proxy.Forward) error {
		return proxy.RunWithDialErrorHandler(rctx, d, fs, func(remoteAddr string, e error) {
			errMu.Lock()
			errBuf.WriteString(forwardDialErrorMessage(remoteAddr, e))
			errBuf.WriteByte('\n')
			errMu.Unlock()
			select {
			case errReady <- struct{}{}:
			default:
			}
		})
	}

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- runForward(ctx, &buf, dialer, transport, []string{spec}, run)
	}()

	conn := waitDialable(t, localAddr, 5*time.Second)
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = conn.Write([]byte("ping"))
	rbuf := make([]byte, 4)
	n, rerr := conn.Read(rbuf)
	// 被拒：远端从未建立 → 本地连接被关闭/重置，读到零字节并出错。
	if rerr == nil && n > 0 {
		t.Errorf("被拒目标不应有数据回显，但读到 %d 字节: %q", n, rbuf[:n])
	}
	_ = conn.Close()

	// 等待 handler 捕获到被拒原因（R5.4：呈现给用户）。
	select {
	case <-errReady:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("被拒后用户可见输出未在 5s 内产生（R5.4 未呈现拒绝原因）")
	}
	errMu.Lock()
	userMsg := errBuf.String()
	errMu.Unlock()
	// R5.4：呈现给用户的输出须含 Hub 拒绝原因。
	if !strings.Contains(userMsg, "rejected") {
		t.Errorf("用户可见输出应含 Hub 拒绝原因 'rejected'，得到: %q", userMsg)
	}
	if !strings.Contains(userMsg, "target_not_allowed") {
		t.Errorf("用户可见输出应含 Hub 给出的原因 'target_not_allowed'，得到: %q", userMsg)
	}
	// R5.4/R5.5：须说明该限制由 Hub 侧安全策略执行、bk 不绕过。
	if !strings.Contains(userMsg, "Hub") {
		t.Errorf("用户可见输出应说明该限制由 Hub 侧安全策略决定，得到: %q", userMsg)
	}

	// runForward 按 R4.6 单连接隔离继续运行（未因被拒整体失败）；取消后正常返回 nil。
	cancel()
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("单连接被拒不应使整体转发失败（R4.6 隔离）；取消后应返回 nil，得到: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后 runForward 未在 5s 内返回")
	}
}
