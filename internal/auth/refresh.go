package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/supabase-community/gotrue-go"
)

// refresh.go 实现 auth.session 的会话刷新与回写编排（Requirement 10.1–10.4）。
// 编排逻辑与具体的远端刷新实现解耦：通过 Refresher 接口注入，使核心路径（过期判定、
// 回写、sentinel 错误）可在不触达网络的情况下被完整单测覆盖；生产路径由
// GoTrueRefresher 适配真实的 gotrue/supabase 客户端。

// ErrReloginRequired 是可被调用方识别的 sentinel 错误：当会话已过期且无法续期
//（缺少 refresh token、刷新失败、目标 profile 不存在）时返回，调用方据此引导用户
// 重新登录（Requirement 10.4）。包裹时请使用 %w 以保留 errors.Is 可识别性。
var ErrReloginRequired = errors.New("session expired, please re-login")

// Refresher 抽象「用 refresh token 向认证服务换取新会话」的能力。注入该接口而非
// 直接在编排逻辑中硬编码 supabase 调用，便于以 fake 实现做单元测试。
type Refresher interface {
	// RefreshToken 使用给定的 refresh token 换取新的会话；失败返回非 nil error。
	RefreshToken(refreshToken string) (Session, error)
}

// EnsureFresh 保证返回指定 profile 的「有效（未过期）」会话：
//   - 找不到该 profile：返回 ErrReloginRequired（不调用 refresher）。
//   - 会话未过期：原样返回，不调用 refresher（Requirement 10.1）。
//   - 会话已过期且存在 refresh token：调用 refresher 刷新；成功则把新会话用
//     AddAuthConfig 覆盖回写到 path 对应文件并返回新会话（Requirement 10.2/10.3）。
//   - 会话已过期但无 refresh token，或刷新失败：返回包裹 ErrReloginRequired 的错误
//     （Requirement 10.4）。
//
// now/skew 用于过期判定（基于 Session.ExpiresAt，不依赖远端，Requirement 10.5）。
func EnsureFresh(path, profile string, now time.Time, skew time.Duration, refresher Refresher) (Session, error) {
	configs, err := LoadAuthConfig(path)
	if err != nil {
		return Session{}, fmt.Errorf("load auth config: %w: %w", err, ErrReloginRequired)
	}

	var current *AuthConfig
	for _, c := range configs {
		if c != nil && c.Profile == profile {
			current = c
			break
		}
	}
	if current == nil {
		return Session{}, fmt.Errorf("profile %q not found: %w", profile, ErrReloginRequired)
	}

	// 未过期：直接复用现有会话。
	if !IsExpiredAt(current.Session, now, skew) {
		return current.Session, nil
	}

	// 过期但无 refresh token：无法续期，需重新登录。
	if current.Session.RefreshToken == "" {
		return Session{}, fmt.Errorf("profile %q has no refresh token: %w", profile, ErrReloginRequired)
	}

	if refresher == nil {
		return Session{}, fmt.Errorf("no refresher available: %w", ErrReloginRequired)
	}

	// 尝试刷新；失败回退到重新登录提示。
	refreshed, err := refresher.RefreshToken(current.Session.RefreshToken)
	if err != nil {
		return Session{}, fmt.Errorf("refresh session for profile %q: %w: %w", profile, err, ErrReloginRequired)
	}

	// 刷新成功：覆盖回写新会话到该 profile（Requirement 10.3）。
	updated := &AuthConfig{Profile: profile, Session: refreshed}
	if err := AddAuthConfig(path, updated); err != nil {
		return Session{}, fmt.Errorf("persist refreshed session for profile %q: %w", profile, err)
	}

	return refreshed, nil
}

// GoTrueRefresher 是基于 supabase-go/gotrue 客户端的真实 Refresher 实现，供生产路径
// 使用。它把 gotrue 的 TokenResponse 映射回本包的 Session 类型。
//
// 注意：gotrue-go v1.2.0 暴露的刷新方法为 client.RefreshToken(refreshToken) →
// (*types.TokenResponse, error)，其中 TokenResponse 内嵌 types.Session（含新的
// access/refresh token 与 ExpiresAt）。该映射仅复制持久化所需的标量字段。
type GoTrueRefresher struct {
	Client gotrue.Client
}

// RefreshToken 调用底层 gotrue 客户端刷新会话，并把结果映射为本包 Session。
func (g GoTrueRefresher) RefreshToken(refreshToken string) (Session, error) {
	if g.Client == nil {
		return Session{}, errors.New("gotrue client is nil")
	}
	resp, err := g.Client.RefreshToken(refreshToken)
	if err != nil {
		return Session{}, err
	}
	s := resp.Session
	return Session{
		AccessToken:  s.AccessToken,
		RefreshToken: s.RefreshToken,
		TokenType:    s.TokenType,
		ExpiresIn:    int64(s.ExpiresIn),
		ExpiresAt:    s.ExpiresAt,
		User: User{
			ID:    s.User.ID.String(),
			Email: s.User.Email,
		},
	}, nil
}
