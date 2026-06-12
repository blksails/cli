package dokku

import (
	"context"
	"fmt"
	"strings"
)

// SSHKeysAdd 等价 `dokku ssh-keys:add <name>`，公钥经 stdin 传入。
//
// 命令拼接复用 Run 的 Sudo 前缀语义（Sudo 为 true 时前缀 `sudo dokku`），但因需向
// 远端会话喂 stdin，走 c.ssh.RunArgsStdin 而非 c.Run。返回远端标准输出；
// 出错时透传 dokku 的 stderr（如「名称已存在」），供上层区分处理。
func (c *Client) SSHKeysAdd(ctx context.Context, name, publicKey string) (string, error) {
	args := []string{"ssh-keys:add", name}
	remote := args
	if c.cfg.Sudo {
		remote = append([]string{"sudo", "dokku"}, args...)
	}
	// dokku 的 ssh-keys:add 经 stdin 读取公钥时要求该行以换行结尾；缺结尾换行会导致
	// 命令静默失败（exit 1、无任何 stdout/stderr）。故确保结尾恰有一个换行。
	key := strings.TrimRight(publicKey, "\n") + "\n"
	res, err := c.ssh.RunArgsStdin(ctx, strings.NewReader(key), remote...)
	if err != nil {
		return res.Stdout, fmt.Errorf("dokku %s: %w", strings.Join(args, " "), err)
	}
	return res.Stdout, nil
}

// SSHKeysRemove 等价 `dokku ssh-keys:remove <name>`。复用 Run（已处理 Sudo 前缀），
// 返回标准输出；出错时透传 dokku stderr（如「名称不存在」）供上层区分。
func (c *Client) SSHKeysRemove(ctx context.Context, name string) (string, error) {
	return c.Run(ctx, "ssh-keys:remove", name)
}
