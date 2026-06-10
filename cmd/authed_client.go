/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"time"

	"github.com/supabase-community/supabase-go"
	"pkg.blksails.net/bk/internal/auth"
)

// authed_client.go 提供 cli-foundation 的稳定共享入口 AuthedClient：按 profile 返回
// 已注入会话、schema 恒为 `blacksail` 的 Supabase client（Requirement 8.1–8.6）。
//
// 核心编排被抽出为可注入测试缝（authedClientWith），使会话有效/缺失/过期刷新成功/
// 过期刷新失败四条路径都能在不触达网络的前提下被集成测试覆盖。导出入口 AuthedClient
// 负责装配真实依赖：DefaultClient()（schema=blacksail）、GoTrueRefresher（来自该 client
// 的 gotrue/Auth）、time.Now()。

// authedClientSkew 是判定会话是否需要刷新的安全余量。design 建议约 60s，使临近过期的
// 会话提前进入刷新路径，避免拿到刚好越过有效期的 client（design：过期判定留 60s 余量）。
const authedClientSkew = 60 * time.Second

// AuthedClient 返回指定 profile 的带认证 Supabase client。
//
// 行为（Requirement 8.2/8.3/8.4）：
//   - 会话有效：返回携带该会话认证态的 client。
//   - 会话过期：先尝试刷新并回写（见 Requirement 10），成功后返回携带新会话的 client。
//   - 无会话或刷新失败：返回包裹 auth.ErrReloginRequired 的明确错误，引导用户先登录。
//
// schema 默认 `blacksail`（应用域）。签名冻结，供下游 spec（如 secret-vault）
// 安全依赖（Requirement 8.1/8.5/8.6）。
func AuthedClient(profile string) (*supabase.Client, error) {
	return AuthedClientSchema(profile, schema)
}

// AuthedClientSchema 同 AuthedClient，但把 client 绑定到指定 PostgREST schema。
// CLI 工具专属数据（如 ssh-key-provisioning 的 cli.ssh_keys）用独立 schema `cli`，
// 与应用域 `blacksail`（access_keys 等）隔离。会话刷新/回写语义与 AuthedClient 一致。
func AuthedClientSchema(profile, sch string) (*supabase.Client, error) {
	base, err := newSchemaClient(sch)
	if err != nil {
		return nil, fmt.Errorf("create supabase client: %w", err)
	}
	// 用同一底座 client 的 gotrue/Auth 构造真实 Refresher（tasks.md 2.2 note）。
	refresher := auth.GoTrueRefresher{Client: base.Auth}
	return authedClientWith(authConfig, profile, time.Now(), refresher, base)
}

// authedClientWith 是 AuthedClient 的可测核心：把外部依赖（auth.json 路径、当前时间、
// Refresher、底座 client）作为参数注入，便于以临时文件 + fake refresher 覆盖全部路径。
//
// 它委托 auth.EnsureFresh 处理「有效直接返回 / 过期刷新回写 / 无会话或刷新失败返回
// ErrReloginRequired」的判定与持久化，然后从回写后的 auth.json 重新读取该 profile 的
// 最新会话并注入到 client（auth.FromAuthConfig），保证返回的 client 携带的是最新会话。
func authedClientWith(loadPath, profile string, now time.Time, refresher auth.Refresher, base *supabase.Client) (*supabase.Client, error) {
	if _, err := auth.EnsureFresh(loadPath, profile, now, authedClientSkew, refresher); err != nil {
		// EnsureFresh 已用 %w 包裹 ErrReloginRequired（缺 profile / 无 refresh token /
		// 刷新失败）；这里再包一层面向用户的引导信息，并继续保留 errors.Is 可识别性。
		return nil, fmt.Errorf("profile %q 未登录或会话已失效，请运行 bk auth login: %w", profile, err)
	}

	// EnsureFresh 成功后，auth.json 中该 profile 已是最新（未过期或刚回写的）会话。
	// 重新读取以获得完整 AuthConfig（含 User 等），再注入到 client。
	configs, err := auth.LoadAuthConfig(loadPath)
	if err != nil {
		return nil, fmt.Errorf("reload auth config for profile %q: %w", profile, err)
	}
	var current *auth.AuthConfig
	for _, c := range configs {
		if c != nil && c.Profile == profile {
			current = c
			break
		}
	}
	if current == nil {
		// 理论上 EnsureFresh 成功后该 profile 必然存在；防御性兜底为引导登录错误。
		return nil, fmt.Errorf("profile %q 未登录，请运行 bk auth login: %w", profile, auth.ErrReloginRequired)
	}

	return auth.FromAuthConfig(current, base), nil
}
