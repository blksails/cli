package proxyhub

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/supabase-community/supabase-go"
)

// store.go：基于注入的、已认证（schema=cli）的 *supabase.Client 从 cli.proxy_hub 读取
// proxy hub 目录（只读）。登录后客户端拉取并缓存到本机；目录的维护（写）仅管理员经 SQL/后台完成。

const table = "proxy_hub"

// Store 封装对 cli.proxy_hub 的只读访问。
type Store struct {
	client *supabase.Client
}

// NewStore 用一个已认证、schema 固定为 cli 的 *supabase.Client 构造 Store。
func NewStore(client *supabase.Client) *Store { return &Store{client: client} }

// List 返回 hub 目录全部记录。RLS 允许任意已登录用户读取；空集返回空切片；
// 权限/schema 未暴露类错误映射为 ErrPermission。
func (s *Store) List() ([]Hub, error) {
	data, _, err := s.client.From(table).
		Select("*", "", false).
		ExecuteString()
	if err != nil {
		return nil, mapErr(err)
	}
	return decodeHubs(data)
}

func decodeHubs(data string) ([]Hub, error) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "[]" {
		return []Hub{}, nil
	}
	var list []Hub
	if err := json.Unmarshal([]byte(trimmed), &list); err != nil {
		return nil, fmt.Errorf("解析 proxy_hub 响应失败: %w", err)
	}
	return list, nil
}

var permissionCodes = []string{"42501", "PGRST301", "PGRST302", "PGRST106"}

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
