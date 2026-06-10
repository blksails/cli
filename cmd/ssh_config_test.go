package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// viperFromYAML 构造一个仅从内存 yaml 字符串读取的 *viper.Viper，
// 供下面的测试在不触达文件系统/全局 viper 的前提下驱动 sshConfigFrom。
func viperFromYAML(t *testing.T, yaml string) *viper.Viper {
	t.Helper()
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(yaml)); err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	return v
}

// TestSSHConfigFrom_FullBlock 验证 Requirement 9.1/9.2：
// 完整 ssh 块按 9.2 的键名映射为 sshx.Config 全部字段。
func TestSSHConfigFrom_FullBlock(t *testing.T) {
	v := viperFromYAML(t, ""+
		"ssh:\n"+
		"  host: foo\n"+
		"  user: bar\n"+
		"  port: 2222\n"+
		"  identity: ~/.ssh/id\n"+
		"  insecure: true\n")

	cfg, err := sshConfigFrom(v, "default")
	if err != nil {
		t.Fatalf("sshConfigFrom returned error: %v", err)
	}
	if cfg.Host != "foo" {
		t.Errorf("Host = %q, want %q", cfg.Host, "foo")
	}
	if cfg.User != "bar" {
		t.Errorf("User = %q, want %q", cfg.User, "bar")
	}
	if cfg.Port != 2222 {
		t.Errorf("Port = %d, want %d", cfg.Port, 2222)
	}
	if cfg.IdentityFile != "~/.ssh/id" {
		t.Errorf("IdentityFile = %q, want %q", cfg.IdentityFile, "~/.ssh/id")
	}
	if !cfg.Insecure {
		t.Errorf("Insecure = %v, want true", cfg.Insecure)
	}
}

// TestSSHConfigFrom_MissingHost 验证 Requirement 9.3：
// ssh 块缺失 host 时返回明确错误（不构造无效连接配置）。
func TestSSHConfigFrom_MissingHost(t *testing.T) {
	v := viperFromYAML(t, ""+
		"ssh:\n"+
		"  user: bar\n"+
		"  port: 2222\n")

	cfg, err := sshConfigFrom(v, "default")
	if err == nil {
		t.Fatalf("expected error for missing ssh.host, got nil (cfg=%+v)", cfg)
	}
	if !strings.Contains(err.Error(), "host") && !strings.Contains(err.Error(), "主机") {
		t.Errorf("error %q does not mention ssh host", err.Error())
	}
}

// TestSSHConfigFrom_Defaults 验证 Requirement 9.4：
// 仅配置 host 时，user 保持为空（不硬编码 root），port 缺省填 22。
func TestSSHConfigFrom_Defaults(t *testing.T) {
	v := viperFromYAML(t, ""+
		"ssh:\n"+
		"  host: only-host\n")

	cfg, err := sshConfigFrom(v, "default")
	if err != nil {
		t.Fatalf("sshConfigFrom returned error: %v", err)
	}
	if cfg.Host != "only-host" {
		t.Errorf("Host = %q, want %q", cfg.Host, "only-host")
	}
	if cfg.User != "" {
		t.Errorf("User = %q, want empty (must not hardcode root)", cfg.User)
	}
	if cfg.Port != 22 {
		t.Errorf("Port = %d, want default 22", cfg.Port)
	}
	if cfg.Insecure {
		t.Errorf("Insecure = %v, want false by default", cfg.Insecure)
	}
}

// TestSSHConfigFrom_ProfileIsolation 验证 Requirement 7.1/7.3/9.x：
// ssh 块为全局配置；SSHConfig 是纯读函数——
//  1. 对不同 profile 调用返回的是同一全局 ssh 块（不因 profile 名报错/串改）；
//  2. 多次调用之间不互相污染（同一输入稳定可重入）；
//  3. 隔离体现在 auth/session 层：SSHConfig 不读取/不修改 auth.json，
//     因此对一个 profile 的 SSH 读取不会触碰其它 profile 的会话数据。
func TestSSHConfigFrom_ProfileIsolation(t *testing.T) {
	v := viperFromYAML(t, ""+
		"ssh:\n"+
		"  host: shared-host\n"+
		"  user: shared-user\n"+
		"  port: 2200\n")

	// profile A 读取
	a, err := sshConfigFrom(v, "alpha")
	if err != nil {
		t.Fatalf("sshConfigFrom(alpha): %v", err)
	}
	// profile B 读取（不同 profile 名）
	b, err := sshConfigFrom(v, "beta")
	if err != nil {
		t.Fatalf("sshConfigFrom(beta): %v", err)
	}

	// 全局 ssh 块：两 profile 读到同样的值，且 viper 未被读取动作污染。
	if a != b {
		t.Fatalf("ssh block is global; profile alpha=%+v differs from beta=%+v", a, b)
	}
	if a.Host != "shared-host" || a.User != "shared-user" || a.Port != 2200 {
		t.Fatalf("unexpected mapped config: %+v", a)
	}

	// 纯读可重入：再次读取 alpha 仍得到相同结果（无副作用、未串改 viper）。
	a2, err := sshConfigFrom(v, "alpha")
	if err != nil {
		t.Fatalf("sshConfigFrom(alpha) second call: %v", err)
	}
	if a2 != a {
		t.Fatalf("sshConfigFrom not pure-read: %+v != %+v", a2, a)
	}

	// 隔离佐证：底层的 viper 配置未被任何一次调用修改
	// （ssh 块仍是原值，没有被某个 profile 的读取动作改写）。
	if got := v.GetString("ssh.host"); got != "shared-host" {
		t.Fatalf("viper ssh.host mutated to %q; SSHConfig must be pure-read", got)
	}
}
