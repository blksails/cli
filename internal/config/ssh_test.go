package config

import (
	"testing"
)

// Task 1.2 — Requirements 2.2/2.3/2.4/2.5/2.6/9.2/9.4
// 纯函数：已读取的 ssh 块值 → sshx.Config，含默认值与校验。

func TestSSHSettingsToConfig_HostEmptyReturnsError(t *testing.T) {
	// Requirement 9.3 / 2.2: host 缺失返回明确错误
	_, err := SSHSettings{Host: ""}.ToSSHConfig()
	if err == nil {
		t.Fatal("期望 host 为空时返回错误，实际返回 nil")
	}

	// 仅空白也应视为缺失
	if _, err := (SSHSettings{Host: "   "}).ToSSHConfig(); err == nil {
		t.Fatal("期望 host 仅含空白时返回错误，实际返回 nil")
	}
}

func TestSSHSettingsToConfig_PortDefaultsTo22(t *testing.T) {
	// Requirement 2.4 / 9.4: port 未配置（0）→ 22
	cfg, err := SSHSettings{Host: "example.com"}.ToSSHConfig()
	if err != nil {
		t.Fatalf("未预期的错误: %v", err)
	}
	if cfg.Port != 22 {
		t.Fatalf("期望 port 默认 22，实际 %d", cfg.Port)
	}

	// 显式端口应透传，不被覆盖
	cfg2, err := SSHSettings{Host: "example.com", Port: 2222}.ToSSHConfig()
	if err != nil {
		t.Fatalf("未预期的错误: %v", err)
	}
	if cfg2.Port != 2222 {
		t.Fatalf("期望 port 透传 2222，实际 %d", cfg2.Port)
	}
}

func TestSSHSettingsToConfig_UserUnsetStaysEmpty(t *testing.T) {
	// Requirement 2.3 / 9.4: user 未配置时保持为空（不硬编码 root）
	cfg, err := SSHSettings{Host: "example.com"}.ToSSHConfig()
	if err != nil {
		t.Fatalf("未预期的错误: %v", err)
	}
	if cfg.User != "" {
		t.Fatalf("期望 user 未配置时保持为空，实际 %q", cfg.User)
	}
	if cfg.User == "root" {
		t.Fatal("user 不得被硬编码为 root（应保持为空，由下游 dokku.New 默认为 dokku）")
	}

	// 显式 user 应透传
	cfg2, err := SSHSettings{Host: "example.com", User: "dokku"}.ToSSHConfig()
	if err != nil {
		t.Fatalf("未预期的错误: %v", err)
	}
	if cfg2.User != "dokku" {
		t.Fatalf("期望 user 透传 dokku，实际 %q", cfg2.User)
	}
}

func TestSSHSettingsToConfig_InsecurePassthrough(t *testing.T) {
	// Requirement 2.5: insecure=true → 跳过主机校验
	cfg, err := SSHSettings{Host: "example.com", Insecure: true}.ToSSHConfig()
	if err != nil {
		t.Fatalf("未预期的错误: %v", err)
	}
	if !cfg.Insecure {
		t.Fatal("期望 insecure=true 透传为 Insecure=true")
	}

	// Requirement 2.6: insecure 未设置/false → 保留 known_hosts 校验（Insecure=false）
	cfgFalse, err := SSHSettings{Host: "example.com"}.ToSSHConfig()
	if err != nil {
		t.Fatalf("未预期的错误: %v", err)
	}
	if cfgFalse.Insecure {
		t.Fatal("期望 insecure 未设置时 Insecure=false（保留 known_hosts 校验）")
	}
}

func TestSSHSettingsToConfig_FieldMapping(t *testing.T) {
	// Requirement 9.2: host→Host, user→User, port→Port, identity→IdentityFile, insecure→Insecure
	s := SSHSettings{
		Host:     "dokku.example.com",
		User:     "deploy",
		Port:     2200,
		Identity: "~/.ssh/id_ed25519",
		Insecure: true,
	}
	cfg, err := s.ToSSHConfig()
	if err != nil {
		t.Fatalf("未预期的错误: %v", err)
	}
	if cfg.Host != "dokku.example.com" {
		t.Errorf("Host 映射错误: %q", cfg.Host)
	}
	if cfg.User != "deploy" {
		t.Errorf("User 映射错误: %q", cfg.User)
	}
	if cfg.Port != 2200 {
		t.Errorf("Port 映射错误: %d", cfg.Port)
	}
	if cfg.IdentityFile != "~/.ssh/id_ed25519" {
		t.Errorf("IdentityFile 映射错误: %q", cfg.IdentityFile)
	}
	if !cfg.Insecure {
		t.Errorf("Insecure 映射错误: %v", cfg.Insecure)
	}
}
