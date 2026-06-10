// Package sshx 是 bk CLI 自建的进程内 SSH 客户端库，基于 golang.org/x/crypto/ssh。
//
// 设计目标：不依赖系统 `ssh` 可执行文件，纯 Go 实现，跨操作系统（Linux/macOS/Windows）
// 一致工作。dokku 应用管理与端口代理两个模块共享同一套连接、认证与主机校验逻辑。
//
// 认证顺序：显式私钥文件 → ssh-agent（若 SSH_AUTH_SOCK 可用）→ 密码 → 默认私钥
// (~/.ssh/id_ed25519、~/.ssh/id_rsa)。主机校验默认走 ~/.ssh/known_hosts，可用
// Insecure 关闭（开发环境）。
package sshx

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Config 描述一次 SSH 连接所需的全部参数。
type Config struct {
	Host string // 主机地址，必填
	User string // 登录用户，默认 root
	Port int    // 端口，默认 22

	IdentityFile   string // 私钥文件路径，可选
	Passphrase     string // 私钥口令，可选
	Password       string // 密码认证，可选
	UseAgent       bool   // 是否尝试 ssh-agent（默认尝试）
	Insecure       bool   // true 时跳过主机密钥校验（开发用）
	KnownHostsPath string // known_hosts 路径，默认 ~/.ssh/known_hosts

	Timeout time.Duration // 连接超时，默认 15s
}

// Client 是一个已建立的 SSH 连接。使用完毕需调用 Close。
type Client struct {
	cfg  Config
	conn *ssh.Client
}

// Dial 按 Config 建立 SSH 连接。
func Dial(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, fmt.Errorf("sshx: 未配置主机地址")
	}
	if cfg.User == "" {
		cfg.User = "root"
	}
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}

	auths, err := authMethods(cfg)
	if err != nil {
		return nil, err
	}
	if len(auths) == 0 {
		return nil, fmt.Errorf("sshx: 没有可用的认证方式（请配置私钥、ssh-agent 或密码）")
	}

	hostKeyCallback, err := hostKeyCallback(cfg)
	if err != nil {
		return nil, err
	}

	clientCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auths,
		HostKeyCallback: hostKeyCallback,
		Timeout:         cfg.Timeout,
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	conn, err := ssh.Dial("tcp", addr, clientCfg)
	if err != nil {
		return nil, fmt.Errorf("sshx: 连接 %s@%s 失败: %w", cfg.User, addr, err)
	}
	return &Client{cfg: cfg, conn: conn}, nil
}

// Close 关闭底层连接。
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Conn 暴露底层 *ssh.Client，供端口转发等高级用法直接使用。
func (c *Client) Conn() *ssh.Client { return c.conn }

// authMethods 按优先级组装认证方式。
func authMethods(cfg Config) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// 1. 显式私钥
	keyFiles := []string{}
	if cfg.IdentityFile != "" {
		keyFiles = append(keyFiles, cfg.IdentityFile)
	} else {
		// 默认私钥
		if home, err := os.UserHomeDir(); err == nil {
			for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
				p := filepath.Join(home, ".ssh", name)
				if _, err := os.Stat(p); err == nil {
					keyFiles = append(keyFiles, p)
				}
			}
		}
	}
	for _, kf := range keyFiles {
		signer, err := loadPrivateKey(kf, cfg.Passphrase)
		if err != nil {
			// 显式指定的私钥加载失败应报错；默认私钥则跳过。
			if cfg.IdentityFile != "" {
				return nil, err
			}
			continue
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	// 2. ssh-agent（默认尝试，除非显式关闭且未提供其它方式）
	if cfg.UseAgent || cfg.IdentityFile == "" {
		if am := agentAuth(); am != nil {
			methods = append(methods, am)
		}
	}

	// 3. 密码
	if cfg.Password != "" {
		methods = append(methods, ssh.Password(cfg.Password))
	}

	return methods, nil
}

func loadPrivateKey(path, passphrase string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("sshx: 读取私钥 %s 失败: %w", path, err)
	}
	if passphrase != "" {
		signer, err := ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
		if err != nil {
			return nil, fmt.Errorf("sshx: 解析带口令私钥 %s 失败: %w", path, err)
		}
		return signer, nil
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("sshx: 解析私钥 %s 失败（若有口令请配置 passphrase）: %w", path, err)
	}
	return signer, nil
}

// agentAuth 通过 SSH_AUTH_SOCK 连接 ssh-agent；不可用时返回 nil。
// 在 Unix 上走 unix socket；Windows 用户建议直接使用私钥文件。
func agentAuth() ssh.AuthMethod {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil
	}
	ag := agent.NewClient(conn)
	return ssh.PublicKeysCallback(ag.Signers)
}

// hostKeyCallback 构造主机密钥校验回调。
func hostKeyCallback(cfg Config) (ssh.HostKeyCallback, error) {
	if cfg.Insecure {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	khPath := cfg.KnownHostsPath
	if khPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("sshx: 无法定位 known_hosts: %w", err)
		}
		khPath = filepath.Join(home, ".ssh", "known_hosts")
	}
	if _, err := os.Stat(khPath); err != nil {
		return nil, fmt.Errorf("sshx: known_hosts 文件不存在 (%s)，请先建立信任或设置 ssh.insecure=true: %w", khPath, err)
	}
	cb, err := knownhosts.New(khPath)
	if err != nil {
		return nil, fmt.Errorf("sshx: 加载 known_hosts 失败: %w", err)
	}
	return cb, nil
}

// 让 context 取消能够中断长连接的辅助：调用方可在 ctx Done 时 Close。
var _ = context.Background
