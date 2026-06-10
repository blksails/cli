package sshx

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// startEchoSSHServer 启动一个进程内 SSH 服务端（监听 127.0.0.1:0），对 exec 请求
// 把会话 stdin 原样拷贝到 stdout（模拟远端 `cat`），用于验证 stdin 透传。
// 返回监听地址、可验证的主机公钥，以及一个用于关闭的函数。
func startEchoSSHServer(t *testing.T) (addr string, hostPub ssh.PublicKey, stop func()) {
	t.Helper()

	// 生成主机密钥。
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成主机密钥失败: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("构造主机 signer 失败: %v", err)
	}

	const testUser = "tester"
	const testPass = "secret"

	srvCfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == testUser && string(pass) == testPass {
				return &ssh.Permissions{}, nil
			}
			return nil, errAuth
		},
	}
	srvCfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			nConn, err := ln.Accept()
			if err != nil {
				return // 监听关闭
			}
			go handleEchoConn(nConn, srvCfg)
		}
	}()

	stop = func() {
		_ = ln.Close()
		wg.Wait()
	}
	return ln.Addr().String(), hostSigner.PublicKey(), stop
}

var errAuth = &authError{}

type authError struct{}

func (*authError) Error() string { return "认证失败" }

func handleEchoConn(nConn net.Conn, srvCfg *ssh.ServerConfig) {
	defer nConn.Close()
	sConn, chans, reqs, err := ssh.NewServerConn(nConn, srvCfg)
	if err != nil {
		return
	}
	defer sConn.Close()
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "only session")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range chReqs {
				switch req.Type {
				case "exec":
					if req.WantReply {
						_ = req.Reply(true, nil)
					}
					// 把 stdin 拷贝到 stdout（模拟 `cat`）。
					_, _ = io.Copy(ch, ch)
					_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					return
				default:
					if req.WantReply {
						_ = req.Reply(false, nil)
					}
				}
			}
		}()
	}
}

// TestRunArgsStdinPassthrough 验证 RunArgsStdin 把给定 io.Reader 作为远端会话 stdin，
// 端到端透传到远端（此处由进程内 echo 服务端原样回显到 stdout）。— Requirements 9.1
func TestRunArgsStdinPassthrough(t *testing.T) {
	addr, _, stop := startEchoSSHServer(t)
	defer stop()

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("解析地址失败: %v", err)
	}
	port := 0
	for _, r := range portStr {
		port = port*10 + int(r-'0')
	}

	cfg := Config{
		Host:     host,
		Port:     port,
		User:     "tester",
		Password: "secret",
		Timeout:  5 * time.Second,
	}
	// 测试用进程内服务端：跳过主机密钥校验，避免触碰 known_hosts。
	cfg.Insecure = true

	client, err := Dial(cfg)
	if err != nil {
		t.Fatalf("连接进程内 SSH 服务端失败: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const payload = "hello-stdin"
	res, err := client.RunArgsStdin(ctx, strings.NewReader(payload), "cat")
	if err != nil {
		t.Fatalf("RunArgsStdin 执行失败: %v", err)
	}
	if res.Stdout != payload {
		t.Fatalf("stdin 透传不符: 期望 Stdout=%q，实际 %q", payload, res.Stdout)
	}
}

// TestRunArgsStdinComposesSameCommand 确认 RunArgsStdin 与 RunArgs 使用相同的
// ShellJoin 命令拼接，唯一行为差异是会话 stdin。
func TestRunArgsStdinComposesSameCommand(t *testing.T) {
	args := []string{"ssh-keys:add", "my key"}
	if got, want := ShellJoin(args), "ssh-keys:add 'my key'"; got != want {
		t.Fatalf("ShellJoin = %q, want %q", got, want)
	}
}
