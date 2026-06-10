// Package dokku 通过进程内 SSH（internal/sshx）驱动远端 Dokku 主机，
// 封装常用的应用管理操作。
//
// 不依赖系统 `ssh` 可执行文件，纯 Go 实现，跨操作系统一致工作。
package dokku

import (
	"context"
	"fmt"
	"strings"

	"pkg.blksails.net/bk/internal/sshx"
)

// Config 描述如何连接 Dokku 主机。SSH 为底层连接参数，
// Sudo 为 true 时以 `sudo dokku <args>` 形式执行（普通管理员账号），
// 否则把 args 作为 dokku 用户的强制命令（标准 dokku 部署）。
type Config struct {
	SSH  sshx.Config
	Sudo bool
}

// Client 是 Dokku 主机的 SSH 客户端。
type Client struct {
	cfg Config
	ssh *sshx.Client
}

// New 建立到 Dokku 主机的连接并返回客户端。使用完毕需调用 Close。
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.SSH.Host) == "" {
		return nil, fmt.Errorf("dokku: 未配置主机地址 (dokku.host)")
	}
	if cfg.SSH.User == "" {
		cfg.SSH.User = "dokku"
	}
	conn, err := sshx.Dial(cfg.SSH)
	if err != nil {
		return nil, err
	}
	return &Client{cfg: cfg, ssh: conn}, nil
}

// Close 关闭底层 SSH 连接。
func (c *Client) Close() error { return c.ssh.Close() }

// Run 执行一条 dokku 命令并返回标准输出。args 为 dokku 子命令及参数，
// 例如 Run(ctx, "apps:create", "myapp")。
func (c *Client) Run(ctx context.Context, args ...string) (string, error) {
	remote := args
	if c.cfg.Sudo {
		// 普通管理员账号经 `sudo dokku <子命令>` 执行（Requirement 11.3 / design 行 239）。
		// 非 sudo 路径发送裸 args，对应 dokku 用户的强制命令模型。
		remote = append([]string{"sudo", "dokku"}, args...)
	}
	res, err := c.ssh.RunArgs(ctx, remote...)
	if err != nil {
		return res.Stdout, fmt.Errorf("dokku %s: %w", strings.Join(args, " "), err)
	}
	return res.Stdout, nil
}

// AppsList 返回应用名列表。
func (c *Client) AppsList(ctx context.Context) ([]string, error) {
	out, err := c.Run(ctx, "apps:list")
	if err != nil {
		return nil, err
	}
	var apps []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// dokku apps:list 首行是 "=====> My Apps" 这样的标题，跳过。
		if line == "" || strings.HasPrefix(line, "=====>") || strings.HasPrefix(line, "My Apps") {
			continue
		}
		apps = append(apps, line)
	}
	return apps, nil
}

// AppsCreate 创建一个新应用。
func (c *Client) AppsCreate(ctx context.Context, name string) (string, error) {
	return c.Run(ctx, "apps:create", name)
}

// AppsDestroy 销毁一个应用（dokku 需要确认，这里通过 --force 跳过交互）。
func (c *Client) AppsDestroy(ctx context.Context, name string) (string, error) {
	return c.Run(ctx, "apps:destroy", name, "--force")
}

// ConfigGet 返回应用的全部环境变量（key=value 形式）。
func (c *Client) ConfigGet(ctx context.Context, app string) (map[string]string, error) {
	out, err := c.Run(ctx, "config:show", app)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "=====>") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		result[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return result, nil
}

// ConfigSet 批量设置应用环境变量。
func (c *Client) ConfigSet(ctx context.Context, app string, kv map[string]string, noRestart bool) (string, error) {
	args := []string{"config:set"}
	if noRestart {
		args = append(args, "--no-restart")
	}
	args = append(args, app)
	for k, v := range kv {
		args = append(args, fmt.Sprintf("%s=%s", k, v))
	}
	return c.Run(ctx, args...)
}

// ConfigUnset 删除应用的指定环境变量。
func (c *Client) ConfigUnset(ctx context.Context, app string, keys ...string) (string, error) {
	args := append([]string{"config:unset", app}, keys...)
	return c.Run(ctx, args...)
}

// Ps 返回应用的进程状态原文。
func (c *Client) Ps(ctx context.Context, app string) (string, error) {
	return c.Run(ctx, "ps:report", app)
}

// PsScale 调整进程的副本数，例如 PsScale(ctx, "myapp", "web", 2)。
func (c *Client) PsScale(ctx context.Context, app, process string, count int) (string, error) {
	return c.Run(ctx, "ps:scale", app, fmt.Sprintf("%s=%d", process, count))
}

// PsRestart 重启应用。
func (c *Client) PsRestart(ctx context.Context, app string) (string, error) {
	return c.Run(ctx, "ps:restart", app)
}

// Logs 返回应用日志快照；num>0 时限制返回行数。
func (c *Client) Logs(ctx context.Context, app string, num int) (string, error) {
	args := []string{"logs", app}
	if num > 0 {
		args = append(args, "--num", fmt.Sprintf("%d", num))
	}
	return c.Run(ctx, args...)
}
