package tunnel

import (
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
	"testing"
	"time"

	"pkg.blksails.net/bk/internal/proxy"
	"pkg.blksails.net/yamuxproxy"
)

func selfSignedTLS(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
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
		t.Fatalf("create cert: %v", err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
}

func startEcho(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

// TestForwardThroughHub 验证完整链路：proxy.Run + tunnel.Dialer → yamuxproxy.Hub → echo。
func TestForwardThroughHub(t *testing.T) {
	echoAddr, closeEcho := startEcho(t)
	defer closeEcho()

	hub, err := yamuxproxy.NewHub(yamuxproxy.HubOptions{
		ListenAddress: "127.0.0.1:0",
		TLSConfig:     selfSignedTLS(t),
		SharedToken:   "tok",
		Apps: []yamuxproxy.AppConfig{{
			ID:                     "dev",
			MaxProducers:           1,
			MaxConsumers:           1,
			MaxRulesPerConsumer:    1,
			MaxInflightPerConsumer: 1,
			MaxForwarders:          2,
			ForwardTargets:         []string{echoAddr},
		}},
	})
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	if err := hub.Start(context.Background()); err != nil {
		t.Fatalf("hub.Start: %v", err)
	}
	defer hub.Stop(context.Background())

	d, closer, err := New(Config{
		ServerAddress: hub.Addr().String(),
		Token:         "tok",
		AppID:         "dev",
		Insecure:      true,
	})
	if err != nil {
		t.Fatalf("tunnel.New: %v", err)
	}
	defer closer.Close()

	// 选一个空闲本地端口
	lp, _ := net.Listen("tcp", "127.0.0.1:0")
	localPort := lp.Addr().(*net.TCPAddr).Port
	lp.Close()

	echoHost, echoPortStr, _ := net.SplitHostPort(echoAddr)
	echoPort, _ := strconv.Atoi(echoPortStr)
	fwd := proxy.Forward{LocalAddr: "127.0.0.1", LocalPort: localPort, RemoteHost: echoHost, RemotePort: echoPort}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = proxy.Run(ctx, d, []proxy.Forward{fwd}) }()

	localAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort))
	var conn net.Conn
	for i := 0; i < 50; i++ {
		conn, err = net.Dial("tcp", localAddr)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial local: %v", err)
	}
	defer conn.Close()

	msg := []byte("hello-through-hub")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(msg))
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("echo mismatch: got %q want %q", buf, msg)
	}
}
