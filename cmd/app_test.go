/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"errors"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/dokku"
	"pkg.blksails.net/bk/internal/sshx"
)

// app_test.go 验证 app 父命令的标志装配与按 profile 的连接装配核心
// （Requirement 11.1/11.2/11.3/11.4）。连接装配核心 appClientWith 经注入的
// loadSSHConfig/newClient 钩子做到无需真实 SSH 服务即可单测。

// TestAppCmd_PersistentFlags 断言 app 父命令注册了 --sudo（默认 false）与 --raw
// 持久标志（完成态：bk app --help 列出 --sudo/--raw）。
func TestAppCmd_PersistentFlags(t *testing.T) {
	sudo := appCmd.PersistentFlags().Lookup("sudo")
	if sudo == nil {
		t.Fatal("appCmd 应注册持久标志 --sudo")
	}
	if sudo.DefValue != "false" {
		t.Errorf("--sudo 默认值应为 false，实际为 %q", sudo.DefValue)
	}

	raw := appCmd.PersistentFlags().Lookup("raw")
	if raw == nil {
		t.Fatal("appCmd 应注册持久标志 --raw")
	}
}

// TestAppCmd_RegisteredOnRoot 断言 app 父命令已挂到既有 rootCmd（init 注册）。
func TestAppCmd_RegisteredOnRoot(t *testing.T) {
	var found bool
	for _, c := range rootCmd.Commands() {
		if c == appCmd {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("appCmd 应已注册到 rootCmd")
	}
	if appCmd.Use != "app" {
		t.Errorf("appCmd.Use 应为 \"app\"，实际为 %q", appCmd.Use)
	}
}

// TestAppClientWith_SSHConfigError 断言：SSHConfig 入口返回错误时，appClientWith
// 透传并以引导配置 ssh.host 的信息包装该错误，且绝不调用 newClient
// （Requirement 11.2：未配置主机时引导配置并非零退出）。
func TestAppClientWith_SSHConfigError(t *testing.T) {
	loadErr := errors.New("ssh.host 未配置")
	var newClientCalled bool

	_, err := appClientWith(
		"default",
		false,
		func(string) (sshx.Config, error) { return sshx.Config{}, loadErr },
		func(dokku.Config) (*dokku.Client, error) {
			newClientCalled = true
			return nil, nil
		},
	)
	if err == nil {
		t.Fatal("loadSSHConfig 失败时 appClientWith 应返回错误")
	}
	if newClientCalled {
		t.Fatal("loadSSHConfig 失败时不应调用 newClient")
	}
	if !errors.Is(err, loadErr) {
		t.Errorf("返回的错误应包裹底层 SSHConfig 错误，实际为 %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "ssh.host") || !strings.Contains(msg, "bk") {
		t.Errorf("错误信息应引导配置 ssh.host（如 'bk ...'），实际为 %q", msg)
	}
}

// TestAppClientWith_PassesSudoAndConfig 断言成功路径：newClient 收到的 dokku.Config
// 带有期望的 Sudo 值与透传的 SSH 配置（Requirement 11.1/11.3）。
func TestAppClientWith_PassesSudoAndConfig(t *testing.T) {
	cases := []struct {
		name string
		sudo bool
	}{
		{"sudo 关闭（默认）", false},
		{"sudo 开启", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := sshx.Config{Host: "dokku.example.com", User: "deploy", Port: 22}
			var got dokku.Config
			sentinel := &dokku.Client{}

			cli, err := appClientWith(
				"default",
				tc.sudo,
				func(string) (sshx.Config, error) { return want, nil },
				func(cfg dokku.Config) (*dokku.Client, error) {
					got = cfg
					return sentinel, nil
				},
			)
			if err != nil {
				t.Fatalf("成功路径不应报错：%v", err)
			}
			if cli != sentinel {
				t.Error("appClientWith 应返回 newClient 构造的 client")
			}
			if got.Sudo != tc.sudo {
				t.Errorf("dokku.Config.Sudo = %v，期望 %v", got.Sudo, tc.sudo)
			}
			if got.SSH != want {
				t.Errorf("dokku.Config.SSH = %+v，期望透传 %+v", got.SSH, want)
			}
		})
	}
}

// TestAppClientWith_PassesProfile 断言 profile 被透传给 loadSSHConfig（11.1：随
// --profile 切换目标主机）。
func TestAppClientWith_PassesProfile(t *testing.T) {
	var gotProfile string
	_, _ = appClientWith(
		"staging",
		false,
		func(p string) (sshx.Config, error) {
			gotProfile = p
			return sshx.Config{Host: "h"}, nil
		},
		func(dokku.Config) (*dokku.Client, error) { return &dokku.Client{}, nil },
	)
	if gotProfile != "staging" {
		t.Errorf("loadSSHConfig 应收到 profile \"staging\"，实际为 %q", gotProfile)
	}
}
