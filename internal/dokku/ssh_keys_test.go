package dokku

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

	"pkg.blksails.net/bk/internal/sshx"
)

// capturedExec 记录一次 exec 请求所观察到的命令字符串与会话 stdin 内容。
type capturedExec struct {
	mu      sync.Mutex
	command string
	stdin   string
}

func (c *capturedExec) set(command, stdin string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.command = command
	c.stdin = stdin
}

func (c *capturedExec) get() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.command, c.stdin
}

// startCaptureSSHServer 启动一个进程内 SSH 服务端（监听 127.0.0.1:0），对 exec 请求
// 捕获命令字符串与会话 stdin，并以 fail 决定退出码：fail==true 时向 stderr 写
// stderrMsg 并以非零退出码结束（模拟 dokku 失败）。返回监听地址与关闭函数。
func startCaptureSSHServer(t *testing.T, cap *capturedExec, fail bool, stderrMsg string) (addr string, stop func()) {
	t.Helper()

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
			return nil, &authErr{}
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
				return
			}
			go handleCaptureConn(nConn, srvCfg, cap, fail, stderrMsg)
		}
	}()

	stop = func() {
		_ = ln.Close()
		wg.Wait()
	}
	return ln.Addr().String(), stop
}

type authErr struct{}

func (*authErr) Error() string { return "认证失败" }

// execPayload 是 SSH exec 请求的有效载荷结构（RFC 4254 §6.5）。
type execPayload struct {
	Command string
}

func handleCaptureConn(nConn net.Conn, srvCfg *ssh.ServerConfig, cap *capturedExec, fail bool, stderrMsg string) {
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
					var p execPayload
					_ = ssh.Unmarshal(req.Payload, &p)
					// 读取会话 stdin（客户端发送 publicKey 后会关闭写端）。
					stdinBytes, _ := io.ReadAll(ch)
					cap.set(p.Command, string(stdinBytes))
					var status uint32
					if fail {
						if stderrMsg != "" {
							_, _ = ch.Stderr().Write([]byte(stderrMsg))
						}
						status = 1
					}
					_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
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

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("解析地址失败: %v", err)
	}
	port := 0
	for _, r := range portStr {
		port = port*10 + int(r-'0')
	}
	return host, port
}

func newTestClient(t *testing.T, addr string, sudo bool) *Client {
	t.Helper()
	host, port := splitHostPort(t, addr)
	c, err := New(Config{
		SSH: sshx.Config{
			Host:     host,
			Port:     port,
			User:     "tester",
			Password: "secret",
			Insecure: true,
			Timeout:  5 * time.Second,
		},
		Sudo: sudo,
	})
	if err != nil {
		t.Fatalf("建立 dokku 客户端失败: %v", err)
	}
	return c
}

const testPubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterial0000000000000000 tester@host"

// TestSSHKeysAddCommandAndStdin 验证 SSHKeysAdd 拼接 `ssh-keys:add <name>` 且公钥经 stdin。— Requirements 5.2, 9.1
func TestSSHKeysAddCommandAndStdin(t *testing.T) {
	cap := &capturedExec{}
	addr, stop := startCaptureSSHServer(t, cap, false, "")
	defer stop()

	c := newTestClient(t, addr, false)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := c.SSHKeysAdd(ctx, "bk-test", testPubKey); err != nil {
		t.Fatalf("SSHKeysAdd 执行失败: %v", err)
	}

	cmd, stdin := cap.get()
	if strings.TrimSpace(cmd) != "ssh-keys:add bk-test" {
		t.Fatalf("非 Sudo 命令应为裸 'ssh-keys:add bk-test'（无前缀），实际: %q", cmd)
	}
	if stdin != testPubKey {
		t.Fatalf("stdin 公钥透传不符: 期望 %q，实际 %q", testPubKey, stdin)
	}
}

// TestSSHKeysAddSudoPrefix 验证 Sudo=true 时命令前缀为 `sudo dokku ssh-keys:add ...`，
// 与 Run/SSHKeysRemove 的提权语义一致（Requirement 11.3）。— Requirements 5.2, 9.1
func TestSSHKeysAddSudoPrefix(t *testing.T) {
	cap := &capturedExec{}
	addr, stop := startCaptureSSHServer(t, cap, false, "")
	defer stop()

	c := newTestClient(t, addr, true)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := c.SSHKeysAdd(ctx, "bk-test", testPubKey); err != nil {
		t.Fatalf("SSHKeysAdd 执行失败: %v", err)
	}

	cmd, stdin := cap.get()
	if strings.TrimSpace(cmd) != "sudo dokku ssh-keys:add bk-test" {
		t.Fatalf("Sudo 命令应为 'sudo dokku ssh-keys:add bk-test'，实际: %q", cmd)
	}
	if stdin != testPubKey {
		t.Fatalf("Sudo 路径下 stdin 公钥透传不符: 期望 %q，实际 %q", testPubKey, stdin)
	}
}

// TestSSHKeysRemoveCommand 验证 SSHKeysRemove 拼接 `ssh-keys:remove <name>`。— Requirements 6.1, 9.2
func TestSSHKeysRemoveCommand(t *testing.T) {
	cap := &capturedExec{}
	addr, stop := startCaptureSSHServer(t, cap, false, "")
	defer stop()

	c := newTestClient(t, addr, false)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := c.SSHKeysRemove(ctx, "bk-test"); err != nil {
		t.Fatalf("SSHKeysRemove 执行失败: %v", err)
	}

	cmd, _ := cap.get()
	if !strings.Contains(cmd, "ssh-keys:remove bk-test") {
		t.Fatalf("命令未包含 'ssh-keys:remove bk-test'，实际: %q", cmd)
	}
}

// TestSSHKeysRemoveSudoPrefix 验证 Sudo=true 时移除命令前缀为 `sudo dokku ssh-keys:remove ...`。— Requirements 6.1, 9.2
func TestSSHKeysRemoveSudoPrefix(t *testing.T) {
	cap := &capturedExec{}
	addr, stop := startCaptureSSHServer(t, cap, false, "")
	defer stop()

	c := newTestClient(t, addr, true)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := c.SSHKeysRemove(ctx, "bk-test"); err != nil {
		t.Fatalf("SSHKeysRemove 执行失败: %v", err)
	}

	cmd, _ := cap.get()
	if strings.TrimSpace(cmd) != "sudo dokku ssh-keys:remove bk-test" {
		t.Fatalf("Sudo 命令应为 'sudo dokku ssh-keys:remove bk-test'，实际: %q", cmd)
	}
}

// TestSSHKeysAddErrorPassthrough 验证 dokku 失败（名称已存在）时 stderr 透传为可识别错误。— Requirements 9.3
func TestSSHKeysAddErrorPassthrough(t *testing.T) {
	const stderrMsg = "Name already exists: bk-test"
	cap := &capturedExec{}
	addr, stop := startCaptureSSHServer(t, cap, true, stderrMsg)
	defer stop()

	c := newTestClient(t, addr, false)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.SSHKeysAdd(ctx, "bk-test", testPubKey)
	if err == nil {
		t.Fatalf("期望 SSHKeysAdd 返回错误，实际为 nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("错误未透传 dokku stderr，实际: %v", err)
	}
}

// TestSSHKeysRemoveErrorPassthrough 验证移除不存在名称时 stderr 透传为可识别错误。— Requirements 9.3
func TestSSHKeysRemoveErrorPassthrough(t *testing.T) {
	const stderrMsg = "SSH key does not exist: bk-test"
	cap := &capturedExec{}
	addr, stop := startCaptureSSHServer(t, cap, true, stderrMsg)
	defer stop()

	c := newTestClient(t, addr, false)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.SSHKeysRemove(ctx, "bk-test")
	if err == nil {
		t.Fatalf("期望 SSHKeysRemove 返回错误，实际为 nil")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("错误未透传 dokku stderr，实际: %v", err)
	}
}
