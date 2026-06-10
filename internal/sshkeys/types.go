// Package sshkeys 实现「SSH 密钥发放」的领域层：密钥生成/落盘（keygen）、
// 基于 Supabase 的 blacksail.ssh_keys 存储（Store）以及领域类型、状态机与可识别错误（types）。
//
// 本文件（types.go）只定义领域类型与 sentinel 错误，刻意保持零依赖（仅标准库 errors），
// 不 import cmd / internal/dokku / supabase——装配在 cmd 层完成（见 design「Allowed Dependencies」）。
package sshkeys

import "errors"

// Status 是密钥登记的状态枚举，底层为字符串，取值与 blacksail.ssh_keys.status 列的
// SQL CHECK 约束（'pending' / 'installed' / 'revoked'）一一对应（Requirement 8.4）。
type Status string

const (
	// StatusPending：公钥已登记，等待管理员代装到 Dokku。
	StatusPending Status = "pending"
	// StatusInstalled：公钥已被管理员安装到 Dokku 主机。
	StatusInstalled Status = "installed"
	// StatusRevoked：公钥已被管理员吊销，不再有效。
	StatusRevoked Status = "revoked"
)

// IsValid 报告 s 是否为合法的状态取值（仅 pending/installed/revoked 合法）。
// 供存储层、命令层与迁移对齐时校验状态字段，防止非法状态进入流转（Requirement 8.4）。
func (s Status) IsValid() bool {
	switch s {
	case StatusPending, StatusInstalled, StatusRevoked:
		return true
	default:
		return false
	}
}

// KeyRecord 是 blacksail.ssh_keys 一行登记记录的领域表示。json tag 与 DB 列名一一对应，
// 直接用于 PostgREST 读写。审计字段（installed_*/revoked_*）在未发生对应事件时为空，
// 以 omitempty 省略，避免向 DB 写入空字符串覆盖 NULL（Requirement 8.1）。
//
// 安全不变量：本结构不含任何私钥字段——私钥永不离开本机、永不入库（Requirement 8.5, 10.1）。
type KeyRecord struct {
	ID          string `json:"id"`
	Owner       string `json:"owner"` // auth.uid()，由 DB 默认值/RLS 约束写入
	Name        string `json:"name"`  // 派生：bk-<emaillocal>-<host>，作为 dokku ssh-keys 名
	Host        string `json:"host"`
	DokkuUser   string `json:"dokku_user"`  // 默认 dokku
	PublicKey   string `json:"public_key"`  // authorized_keys 行
	Fingerprint string `json:"fingerprint"` // SHA256:...
	Status      Status `json:"status"`
	CreatedAt   string `json:"created_at"`
	InstalledBy string `json:"installed_by,omitempty"`
	InstalledAt string `json:"installed_at,omitempty"`
	RevokedBy   string `json:"revoked_by,omitempty"`
	RevokedAt   string `json:"revoked_at,omitempty"`
}

// 以下为可被调用方用 errors.Is 区分的 sentinel 错误。store 与 cmd 层据此把 PostgREST/文件系统
// 的失败归类为权限不足、未找到、私钥已存在三类，从而给出精准的用户提示（Requirement 7.3, 8.4）。
// 包裹时务必使用 %w 以保留 errors.Is 的可识别性。
var (
	// ErrPermission 表示被 RLS 拒绝或权限不足（如非管理员读取全部/改状态）。
	ErrPermission = errors.New("权限不足：需要管理员权限")
	// ErrNotFound 表示目标记录不存在或查询返回空集。
	ErrNotFound = errors.New("未找到对应的密钥记录")
	// ErrKeyExists 表示目标私钥文件已存在（需显式 --force 覆盖）。
	ErrKeyExists = errors.New("私钥文件已存在")
)
