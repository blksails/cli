package hosts

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/supabase-community/supabase-go"
)

// store.go 实现主机目录的读取层：基于注入的、已认证（schema=cli）的 *supabase.Client
// 从 cli.hosts 读取主机目录。零依赖于 cmd——client 在 cmd 层装配后注入。
//
// 本层只读：登录后客户端拉取目录并缓存到本机。写入（维护目录）由管理员经其它途径
// （SQL / 后台）完成，CLI 不提供写接口（符合「仅管理员维护」的安全模型）。

const table = "hosts"

// Store 封装对 cli.hosts 的只读访问。
type Store struct {
	client *supabase.Client
}

// NewStore 用一个已认证、schema 固定为 cli 的 *supabase.Client 构造 Store。
func NewStore(client *supabase.Client) *Store {
	return &Store{client: client}
}

// List 返回主机目录全部记录。RLS 允许任意已登录用户读取（USING true）。
// 空集返回空切片而非错误；权限/schema 未暴露类错误映射为 ErrPermission。
func (s *Store) List() ([]Host, error) {
	data, _, err := s.client.From(table).
		Select("*", "", false).
		ExecuteString()
	if err != nil {
		return nil, mapErr(err)
	}
	return decodeHosts(data)
}

// decodeHosts 把 PostgREST 返回的 JSON 数组解析为 []Host。空字符串/空数组返回空切片。
func decodeHosts(data string) ([]Host, error) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "[]" {
		return []Host{}, nil
	}
	var list []Host
	if err := json.Unmarshal([]byte(trimmed), &list); err != nil {
		return nil, fmt.Errorf("解析 hosts 响应失败: %w", err)
	}
	return list, nil
}

// permissionCodes 与 sshkeys 包一致：PostgREST/PG 表示权限不足/认证失败的错误码。
// postgrest-go v0.0.11 把 4xx 折叠为 "(<code>) <message>"，故据 code 识别。
var permissionCodes = []string{"42501", "PGRST301", "PGRST302", "PGRST106"}

// mapErr 把底层 error 归类：权限/schema 未暴露类→ErrPermission，其余透传（%w 包裹）。
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, code := range permissionCodes {
		if strings.Contains(msg, "("+code+")") {
			return fmt.Errorf("%w: %v", ErrPermission, err)
		}
	}
	return err
}
