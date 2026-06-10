package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// proxy_hubconfig_test.go 覆盖 task 1.2：共享 hub 连接配置解析助手
// resolveHubConfigFrom 的行为契约（Requirement 2.1–2.6、7.2）。
//
// 用可测核心 resolveHubConfigFrom（注入显式标志值 + *viper.Viper）驱动断言，
// 镜像 cli-foundation 的 sshConfigFrom 模式：测试无需触达文件系统或全局 viper。

// newProxyViper 构造一个仅含 proxy.* 键的内存 viper 实例。
func newProxyViper(kv map[string]any) *viper.Viper {
	v := viper.New()
	for k, val := range kv {
		v.Set(k, val)
	}
	return v
}

// TestResolveHubConfig_FlagOverridesConfig 验证标志优先于 .bs.yaml（Requirement 2.3）：
// 同一项标志非空时用标志，标志为空时回退 viper proxy.* 值（Requirement 2.2）。
func TestResolveHubConfig_FlagOverridesConfig(t *testing.T) {
	v := newProxyViper(map[string]any{
		"proxy.server": "cfgserver:443",
		"proxy.token":  "cfgtoken",
		"proxy.app":    "cfgapp",
	})

	// 标志全部提供 → 标志优先。
	got, err := resolveHubConfigFrom("flagserver:8443", "flagtoken", "flagapp",
		false, "", "", false, v)
	if err != nil {
		t.Fatalf("resolveHubConfigFrom 返回错误 %v，期望成功", err)
	}
	if got.Server != "flagserver:8443" {
		t.Errorf("Server = %q，期望标志值 \"flagserver:8443\"（标志应优先于配置）", got.Server)
	}
	if got.Token != "flagtoken" {
		t.Errorf("Token = %q，期望标志值 \"flagtoken\"", got.Token)
	}
	if got.App != "flagapp" {
		t.Errorf("App = %q，期望标志值 \"flagapp\"", got.App)
	}

	// 标志为空 → 回退 viper proxy.* 值。
	got2, err := resolveHubConfigFrom("", "", "", false, "", "", false, v)
	if err != nil {
		t.Fatalf("resolveHubConfigFrom（空标志）返回错误 %v，期望回退配置成功", err)
	}
	if got2.Server != "cfgserver:443" {
		t.Errorf("Server = %q，期望回退配置值 \"cfgserver:443\"", got2.Server)
	}
	if got2.Token != "cfgtoken" {
		t.Errorf("Token = %q，期望回退配置值 \"cfgtoken\"", got2.Token)
	}
	if got2.App != "cfgapp" {
		t.Errorf("App = %q，期望回退配置值 \"cfgapp\"", got2.App)
	}
}

// TestResolveHubConfig_MissingRequiredNamesFields 验证缺失必填项时报错且错误指明
// 具体缺失项（Requirement 2.4），同时错误信息绝不含 token 明文（Requirement 7.2）。
func TestResolveHubConfig_MissingRequiredNamesFields(t *testing.T) {
	v := newProxyViper(nil)

	// 全缺 → 错误应同时点名 server/token/app。
	_, err := resolveHubConfigFrom("", "", "", false, "", "", false, v)
	if err == nil {
		t.Fatalf("全部必填缺失时应返回错误，得到 nil")
	}
	for _, name := range []string{"server", "token", "app"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("错误信息应点名缺失项 %q；得到：%v", name, err)
		}
	}

	// token 已提供但 app 缺失：错误须点名 app，且绝不含 token 明文（Requirement 7.2）。
	const sampleToken = "secret-tok-123"
	_, err = resolveHubConfigFrom("hub:443", sampleToken, "", false, "", "", false, v)
	if err == nil {
		t.Fatalf("app 缺失时应返回错误，得到 nil")
	}
	if !strings.Contains(err.Error(), "app") {
		t.Errorf("错误信息应点名缺失项 \"app\"；得到：%v", err)
	}
	if strings.Contains(err.Error(), "server") {
		t.Errorf("server 已提供，错误不应点名 \"server\"；得到：%v", err)
	}
	if strings.Contains(err.Error(), "token") {
		t.Errorf("token 已提供，错误不应点名 \"token\"；得到：%v", err)
	}
	if strings.Contains(err.Error(), sampleToken) {
		t.Errorf("错误信息绝不能含 token 明文 %q；得到：%v", sampleToken, err)
	}
}

// TestResolveHubConfig_TLSPassthroughViaFlags 验证 insecure/ca/server-name 经标志
// 正确透传到 hubConfig（Requirement 2.6 及 2.1 的 TLS 证书项）。
func TestResolveHubConfig_TLSPassthroughViaFlags(t *testing.T) {
	v := newProxyViper(nil)

	got, err := resolveHubConfigFrom("hub:443", "tok", "app",
		true, "/etc/ca.pem", "hub.example", true, v)
	if err != nil {
		t.Fatalf("resolveHubConfigFrom 返回错误 %v，期望成功", err)
	}
	if !got.Insecure {
		t.Errorf("Insecure = false，期望标志 --insecure 透传为 true")
	}
	if got.CAFile != "/etc/ca.pem" {
		t.Errorf("CAFile = %q，期望标志值 \"/etc/ca.pem\"", got.CAFile)
	}
	if got.ServerName != "hub.example" {
		t.Errorf("ServerName = %q，期望标志值 \"hub.example\"", got.ServerName)
	}
}

// TestResolveHubConfig_TLSPassthroughViaConfig 验证标志未提供时 TLS 项回退 viper
// proxy.* 值（Requirement 2.2），且 --insecure 用 Changed 状态决定是否回退配置。
func TestResolveHubConfig_TLSPassthroughViaConfig(t *testing.T) {
	v := newProxyViper(map[string]any{
		"proxy.server":      "hub:443",
		"proxy.token":       "tok",
		"proxy.app":         "app",
		"proxy.insecure":    true,
		"proxy.ca":          "/cfg/ca.pem",
		"proxy.server_name": "cfg.example",
	})

	// insecure 标志未 Changed（flagInsecureSet=false）→ 回退 proxy.insecure=true。
	got, err := resolveHubConfigFrom("", "", "", false, "", "", false, v)
	if err != nil {
		t.Fatalf("resolveHubConfigFrom 返回错误 %v，期望回退配置成功", err)
	}
	if !got.Insecure {
		t.Errorf("Insecure = false，期望回退 proxy.insecure=true（标志未设置时应回退配置）")
	}
	if got.CAFile != "/cfg/ca.pem" {
		t.Errorf("CAFile = %q，期望回退配置值 \"/cfg/ca.pem\"", got.CAFile)
	}
	if got.ServerName != "cfg.example" {
		t.Errorf("ServerName = %q，期望回退配置值 \"cfg.example\"", got.ServerName)
	}

	// insecure 标志已 Changed 为 false（flagInsecureSet=true）→ 标志优先，覆盖配置的 true。
	got2, err := resolveHubConfigFrom("", "", "", false, "", "", true, v)
	if err != nil {
		t.Fatalf("resolveHubConfigFrom 返回错误 %v，期望成功", err)
	}
	if got2.Insecure {
		t.Errorf("Insecure = true，期望标志 --insecure=false（已显式设置）覆盖配置的 true")
	}
}
