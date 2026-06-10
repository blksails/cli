/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh"
)

// app_integration_test.go 是 dokku-management 的端到端集成回归（Task 5.1）：以进程内
// 假 SSH 后端 + 真实 cobra 命令树（rootCmd）跑通 profile 切换、--sudo 命令构造、
// --raw 原文输出与退出码语义（Requirement 11.1/11.3/12.2/12.3/12.4；design 行 14、
// 239-240 sudo 语义；通用执行流；Error Handling）。
//
// 设计要点：
//   - 假 SSH 后端复用 internal/dokku.ssh_keys_test.go 的 capture 模式（记录 exec 命令字符串、
//     可返回成功 stdout 或非零退出 + stderr）与 internal/sshx.exec_stdin_test.go 的 host key +
//     ssh.ServerConfig 模式。两者在别的包的 _test.go 内不可导入，故把最小 helper 复制到本文件。
//   - 经 SSHConfig→appClient→dokku.New→sshx.Dial 的真实路径连接，认证用公钥（SSHConfig 只
//     映射 host/user/port/identity/insecure，无 password 字段，故必须走 identity 文件 + 公钥认证）。
//   - ssh.user 留空，使 dokku.New 默认 user 为 "dokku"（design 行 240）；服务端捕获连接用户名
//     以断言该默认确实生效。

// itCapture 记录一次 exec 请求观察到的命令字符串与连接所用的 SSH 用户名。
type itCapture struct {
	mu      sync.Mutex
	command string
	user    string
	hits    int
}

func (c *itCapture) record(command, user string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.command = command
	c.user = user
	c.hits++
}

func (c *itCapture) get() (command, user string, hits int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.command, c.user, c.hits
}

// itExecPayload 是 SSH exec 请求的有效载荷结构（RFC 4254 §6.5）。
type itExecPayload struct {
	Command string
}

// startITCaptureServer 启动一个进程内 SSH 服务端（监听 127.0.0.1:0），用公钥认证（接受
// authorizedPub 对应的客户端），对 exec 请求记录命令字符串与连接用户名。fail==true 时向
// stderr 写 stderrMsg 并以非零退出结束（模拟 dokku 失败）；否则向 stdout 写 stdoutMsg 并零退出。
// 返回监听地址与关闭函数。
func startITCaptureServer(t *testing.T, cap *itCapture, authorizedPub ssh.PublicKey, fail bool, stdoutMsg, stderrMsg string) (addr string, stop func()) {
	t.Helper()

	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成主机密钥失败: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("构造主机 signer 失败: %v", err)
	}

	authorizedMarshaled := authorizedPub.Marshal()
	srvCfg := &ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), authorizedMarshaled) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("公钥未授权")
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
			go handleITConn(nConn, srvCfg, cap, fail, stdoutMsg, stderrMsg)
		}
	}()

	stop = func() {
		_ = ln.Close()
		wg.Wait()
	}
	return ln.Addr().String(), stop
}

func handleITConn(nConn net.Conn, srvCfg *ssh.ServerConfig, cap *itCapture, fail bool, stdoutMsg, stderrMsg string) {
	defer nConn.Close()
	sConn, chans, reqs, err := ssh.NewServerConn(nConn, srvCfg)
	if err != nil {
		return
	}
	defer sConn.Close()
	user := sConn.User()
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
					var p itExecPayload
					_ = ssh.Unmarshal(req.Payload, &p)
					cap.record(p.Command, user)
					var status uint32
					if fail {
						if stderrMsg != "" {
							_, _ = ch.Stderr().Write([]byte(stderrMsg))
						}
						status = 1
					} else if stdoutMsg != "" {
						_, _ = ch.Write([]byte(stdoutMsg))
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

// itHostPort 把 "127.0.0.1:54321" 拆为 host 与 int port。
func itHostPort(t *testing.T, addr string) (string, int) {
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

// itWriteClientKey 生成一个 ed25519 客户端密钥，把私钥（OpenSSH PEM）落盘到 dir 下并返回
// 私钥路径与对应的 ssh.PublicKey（供服务端授权）。
func itWriteClientKey(t *testing.T, dir string) (identityPath string, pub ssh.PublicKey) {
	t.Helper()
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成客户端密钥失败: %v", err)
	}
	pemBlock, err := ssh.MarshalPrivateKey(privKey, "")
	if err != nil {
		t.Fatalf("序列化客户端私钥失败: %v", err)
	}
	identityPath = filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(identityPath, pem.EncodeToMemory(pemBlock), 0o600); err != nil {
		t.Fatalf("写入客户端私钥失败: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		t.Fatalf("构造 ssh.PublicKey 失败: %v", err)
	}
	return identityPath, sshPub
}

// itConfigYAML 渲染一个最小 .bs.yaml：仅含全局 ssh 块（host/port/identity/insecure），
// user 留空使 dokku.New 默认为 "dokku"。
func itConfigYAML(host string, port int, identity string) string {
	return strings.Join([]string{
		"ssh:",
		"  host: " + host,
		fmt.Sprintf("  port: %d", port),
		"  identity: " + identity,
		"  insecure: true",
	}, "\n") + "\n"
}

// runApp 经真实 cobra 树（rootCmd）执行一条命令：写入 cfgPath 指向的临时配置，
// 重置受影响的持久标志与全局 viper，设置 args 并 Execute，返回 stdout、stderr 与 Execute 错误。
//
// 必须重置的状态：cfgFile/profile（root 持久标志）、appSudo/appRaw（app 持久标志）、
// 全局 viper（initConfig 会按 --config 重新 ReadInConfig）。
func runApp(t *testing.T, cfgPath string, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	// 重置全局 viper，使本次 initConfig 从指定配置文件干净读取。
	viper.Reset()

	// 重置受影响的持久标志包级变量与 cobra「changed」状态。
	cfgFile = ""
	profile = "default"
	appSudo = false
	appRaw = false
	resetFlag(rootCmd, "config")
	resetFlag(rootCmd, "profile")
	resetFlag(appCmd, "sudo")
	resetFlag(appCmd, "raw")

	var outBuf, errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)

	full := append([]string{"--config", cfgPath}, args...)
	rootCmd.SetArgs(full)

	err = rootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// resetFlag 清掉某个命令上指定标志的 Changed 标记，避免跨子运行的状态泄漏。
// 同时查 Flags 与 PersistentFlags（root 的 --config/--profile 与 app 的 --sudo/--raw 均为持久标志）。
func resetFlag(cmd *cobra.Command, name string) {
	if f := cmd.PersistentFlags().Lookup(name); f != nil {
		f.Changed = false
	}
	if f := cmd.Flags().Lookup(name); f != nil {
		f.Changed = false
	}
}

// TestAppIntegration_ProfileSwitchSudoRawAndExitCodes 是 Task 5.1 的集成回归主测：
//
//	(a) profile 切换（11.1）：--profile p1 命中 server1、--profile p2 命中 server2。
//	(b) sudo 构造（11.3 / design 行 239）：--sudo → 远端命令为 `sudo dokku apps:list`；
//	    无 --sudo → 远端命令为 `apps:list`（dokku 用户强制命令形式，无前缀）。
//	(c) raw（12.2）：--raw 输出服务端原始文本逐字；无 --raw 输出表格（与原文不同）。
//	(d) 退出码 + stderr（12.3/12.4）：服务端非零退出 + stderr → 命令返回非 nil 错误且透传 stderr；
//	    成功路径 → nil 错误。
func TestAppIntegration_ProfileSwitchSudoRawAndExitCodes(t *testing.T) {
	dir := t.TempDir()
	identity, clientPub := itWriteClientKey(t, dir)

	// 两台 server 用于 profile 切换断言：各自独立 capture。
	cap1 := &itCapture{}
	cap2 := &itCapture{}
	const rawAppsList = "=====> My Apps\nalpha\nbeta\n"
	addr1, stop1 := startITCaptureServer(t, cap1, clientPub, false, rawAppsList, "")
	defer stop1()
	addr2, stop2 := startITCaptureServer(t, cap2, clientPub, false, rawAppsList, "")
	defer stop2()

	host1, port1 := itHostPort(t, addr1)
	host2, port2 := itHostPort(t, addr2)

	cfg1 := filepath.Join(dir, "p1.bs.yaml")
	cfg2 := filepath.Join(dir, "p2.bs.yaml")
	if err := os.WriteFile(cfg1, []byte(itConfigYAML(host1, port1, identity)), 0o600); err != nil {
		t.Fatalf("写 cfg1 失败: %v", err)
	}
	if err := os.WriteFile(cfg2, []byte(itConfigYAML(host2, port2, identity)), 0o600); err != nil {
		t.Fatalf("写 cfg2 失败: %v", err)
	}

	// (a) profile 切换：p1 命中 server1。
	t.Run("profile_p1_hits_server1", func(t *testing.T) {
		*cap1 = itCapture{}
		*cap2 = itCapture{}
		_, _, err := runApp(t, cfg1, "app", "ls", "--profile", "p1", "--raw")
		if err != nil {
			t.Fatalf("app ls --profile p1 应成功，实际错误：%v", err)
		}
		_, _, h1 := cap1.get()
		_, _, h2 := cap2.get()
		if h1 == 0 {
			t.Errorf("--profile p1 应命中 server1，但 server1 未收到 exec")
		}
		if h2 != 0 {
			t.Errorf("--profile p1 不应命中 server2，但 server2 收到 %d 次 exec", h2)
		}
	})

	// (a) profile 切换：p2 命中 server2。
	t.Run("profile_p2_hits_server2", func(t *testing.T) {
		*cap1 = itCapture{}
		*cap2 = itCapture{}
		_, _, err := runApp(t, cfg2, "app", "ls", "--profile", "p2", "--raw")
		if err != nil {
			t.Fatalf("app ls --profile p2 应成功，实际错误：%v", err)
		}
		_, _, h1 := cap1.get()
		_, _, h2 := cap2.get()
		if h2 == 0 {
			t.Errorf("--profile p2 应命中 server2，但 server2 未收到 exec")
		}
		if h1 != 0 {
			t.Errorf("--profile p2 不应命中 server1，但 server1 收到 %d 次 exec", h1)
		}
	})

	// (b) sudo 构造：无 --sudo → 远端命令为裸 `apps:list`（dokku 用户强制命令形式）。
	t.Run("no_sudo_bare_command", func(t *testing.T) {
		*cap1 = itCapture{}
		_, _, err := runApp(t, cfg1, "app", "ls", "--profile", "p1", "--raw")
		if err != nil {
			t.Fatalf("app ls 应成功，实际错误：%v", err)
		}
		cmd, user, _ := cap1.get()
		if cmd != "apps:list" {
			t.Errorf("无 --sudo 时远端命令应为 %q（无前缀），实际 %q", "apps:list", cmd)
		}
		// design 行 240：ssh.user 留空时 dokku.New 默认为 "dokku"。
		if user != "dokku" {
			t.Errorf("ssh.user 留空时连接用户应默认为 \"dokku\"，实际 %q", user)
		}
	})

	// (b) sudo 构造：--sudo → 远端命令为 `sudo dokku apps:list`。
	t.Run("sudo_prefixes_sudo_dokku", func(t *testing.T) {
		*cap1 = itCapture{}
		_, _, err := runApp(t, cfg1, "app", "ls", "--profile", "p1", "--raw", "--sudo")
		if err != nil {
			t.Fatalf("app ls --sudo 应成功，实际错误：%v", err)
		}
		cmd, _, _ := cap1.get()
		if cmd != "sudo dokku apps:list" {
			t.Errorf("--sudo 时远端命令应为 %q，实际 %q", "sudo dokku apps:list", cmd)
		}
	})

	// (c) raw vs 表格：--raw 输出服务端原文逐字；无 --raw 输出表格（与原文不同）。
	t.Run("raw_vs_table", func(t *testing.T) {
		*cap1 = itCapture{}
		rawOut, _, err := runApp(t, cfg1, "app", "ls", "--profile", "p1", "--raw")
		if err != nil {
			t.Fatalf("app ls --raw 应成功，实际错误：%v", err)
		}
		if rawOut != rawAppsList {
			t.Errorf("--raw 应逐字输出服务端原文 %q，实际 %q", rawAppsList, rawOut)
		}

		*cap1 = itCapture{}
		tableOut, _, err := runApp(t, cfg1, "app", "ls", "--profile", "p1")
		if err != nil {
			t.Fatalf("app ls（表格）应成功，实际错误：%v", err)
		}
		if tableOut == rawAppsList {
			t.Errorf("默认（无 --raw）不应等于服务端原文；应表格化")
		}
		// 表格视图应过滤掉 dokku 标题行 "=====> My Apps"，仅含应用名 + 表头。
		if strings.Contains(tableOut, "=====>") {
			t.Errorf("表格视图应过滤 dokku 标题装饰行，实际：%q", tableOut)
		}
		if !strings.Contains(tableOut, "App") || !strings.Contains(tableOut, "alpha") || !strings.Contains(tableOut, "beta") {
			t.Errorf("表格视图应含表头 App 与应用名 alpha/beta，实际：%q", tableOut)
		}
	})

	// (c) raw 也适用于 config（结构化命令）：--raw 输出 config:show 原文。
	t.Run("raw_config", func(t *testing.T) {
		const rawConfig = "=====> myapp env vars\nFOO:  bar\nBAZ:  qux\n"
		capCfg := &itCapture{}
		addrC, stopC := startITCaptureServer(t, capCfg, clientPub, false, rawConfig, "")
		defer stopC()
		hostC, portC := itHostPort(t, addrC)
		cfgC := filepath.Join(dir, "pc.bs.yaml")
		if err := os.WriteFile(cfgC, []byte(itConfigYAML(hostC, portC, identity)), 0o600); err != nil {
			t.Fatalf("写 cfgC 失败: %v", err)
		}

		rawOut, _, err := runApp(t, cfgC, "app", "config", "myapp", "--profile", "pc", "--raw")
		if err != nil {
			t.Fatalf("app config --raw 应成功，实际错误：%v", err)
		}
		if rawOut != rawConfig {
			t.Errorf("config --raw 应逐字输出服务端原文 %q，实际 %q", rawConfig, rawOut)
		}
		cmd, _, _ := capCfg.get()
		if cmd != "config:show myapp" {
			t.Errorf("config --raw 远端命令应为 %q，实际 %q", "config:show myapp", cmd)
		}
	})

	// (d) 退出码 + stderr：服务端非零退出 + stderr → 非 nil 错误且透传 dokku stderr。
	t.Run("failure_nonzero_exit_passthrough_stderr", func(t *testing.T) {
		const dokkuStderr = "Pool myapp does not exist"
		capF := &itCapture{}
		addrF, stopF := startITCaptureServer(t, capF, clientPub, true, "", dokkuStderr)
		defer stopF()
		hostF, portF := itHostPort(t, addrF)
		cfgF := filepath.Join(dir, "pf.bs.yaml")
		if err := os.WriteFile(cfgF, []byte(itConfigYAML(hostF, portF, identity)), 0o600); err != nil {
			t.Fatalf("写 cfgF 失败: %v", err)
		}

		_, errOut, err := runApp(t, cfgF, "app", "config", "myapp", "--profile", "pf", "--raw")
		if err == nil {
			t.Fatalf("服务端非零退出时命令应返回非 nil 错误（非零退出码）")
		}
		// dokku stderr 内容应被透传（经 cobra SilenceErrors? 默认 cobra 会把 RunE 错误写到 stderr）。
		combined := errOut + err.Error()
		if !strings.Contains(combined, dokkuStderr) {
			t.Errorf("失败路径应透传 dokku stderr %q，实际 stderr=%q err=%v", dokkuStderr, errOut, err)
		}
	})

	// (d) 成功路径 → nil 错误（零退出）。
	t.Run("success_nil_error", func(t *testing.T) {
		*cap1 = itCapture{}
		_, _, err := runApp(t, cfg1, "app", "ls", "--profile", "p1", "--raw")
		if err != nil {
			t.Errorf("成功路径应返回 nil 错误（零退出），实际 %v", err)
		}
	})
}
