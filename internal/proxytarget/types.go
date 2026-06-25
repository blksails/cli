// Package proxytarget 实现「proxy 转发目标中心化」的持久层与同步逻辑：
// 把 yproxy hub 的 forward_targets allowlist 收敛到 Supabase cli.proxy_targets 表
// （RLS：任意已认证用户可读、仅管理员可写），再由 sync 渲染进 hub config.yaml 并重启 hub。
//
// 安全不变量：allowlist 是 hub 侧防 SSRF 的安全控制；本包只搬运 (app_id, target) 文本，
// 不放行任何 hub 配置以外的目标。Supabase 为唯一真源，sync 单向覆盖 hub config.yaml。
package proxytarget

import "errors"

// Target 是 cli.proxy_targets 的一行：某 app 允许转发到的一个目标模式。
type Target struct {
	ID        string `json:"id,omitempty"`
	AppID     string `json:"app_id"`
	Target    string `json:"target"` // host:port / host:* / *:port / *
	Note      string `json:"note,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

var (
	// ErrPermission：被 RLS/权限拒绝（非管理员写入等）。
	ErrPermission = errors.New("权限不足：需要管理员权限")
	// ErrNotFound：目标记录不存在（删除未命中等）。
	ErrNotFound = errors.New("未找到对应的转发目标")
)
