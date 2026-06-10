package auth

import (
	"strings"
	"time"
)

// session.go 提供 auth.session 的纯逻辑：会话过期判定、按 profile 移除会话条目、
// token 掩码。这里的函数均无文件 IO、无远端探测，便于单元测试与复用；持久化
// 回写由调用方（cmd 层）负责。

// IsExpiredAt 是纯函数版本的过期判定：基于会话记录的过期时间 ExpiresAt（unix 秒）
// 与传入的本地时间 now，并预留安全余量 skew。当 now+skew 已达到或越过 ExpiresAt
// 时判定为过期。判定不依赖任何远端调用（Requirement 10.5）。
func IsExpiredAt(s Session, now time.Time, skew time.Duration) bool {
	deadline := now.Add(skew).Unix()
	return deadline >= s.ExpiresAt
}

// IsExpired 是 design 指定的方法形态，基于 ExpiresAt 与安全余量判定会话是否过期。
// 它以本机当前时间为基准，委托给纯函数 IsExpiredAt，无其它副作用。
func (s Session) IsExpired(skew time.Duration) bool {
	return IsExpiredAt(s, time.Now(), skew)
}

// RemoveProfile 返回移除了目标 profile 会话条目后的新切片：仅删除 profile 匹配的
// 条目，其余 profile 原样保留且不被修改。若目标 profile 不存在则返回内容等价的
// 新切片（幂等，不报错）。该函数仅操作内存切片，不做持久化（Requirement 4.2/4.3）。
func RemoveProfile(configs []*AuthConfig, profile string) []*AuthConfig {
	out := make([]*AuthConfig, 0, len(configs))
	for _, c := range configs {
		if c == nil || c.Profile == profile {
			continue
		}
		out = append(out, c)
	}
	return out
}

// maskKeepEdges 是 MaskToken 在长 token 情况下保留的首尾字符数。
const maskKeepEdges = 2

// MaskToken 对 access/refresh 等 token 类敏感字段做掩码，仅在 token 足够长时
// 保留首尾少量字符，中间统一用 *** 替换；对极短或空 token 不泄露任何原始字符，
// 任何非空输入都不会原样返回（Requirement 11.1/11.4）。
func MaskToken(token string) string {
	if token == "" {
		return ""
	}
	// 短 token（首尾会重叠或几乎暴露全部）一律完全掩码，不保留任何原文。
	if len(token) <= maskKeepEdges*2 {
		return "***"
	}
	var b strings.Builder
	b.WriteString(token[:maskKeepEdges])
	b.WriteString("***")
	b.WriteString(token[len(token)-maskKeepEdges:])
	return b.String()
}
