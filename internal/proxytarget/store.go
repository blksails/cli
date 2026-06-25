package proxytarget

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/supabase-community/supabase-go"
)

// store.go：基于注入的、已认证且 schema 固定为 cli 的 *supabase.Client，对 cli.proxy_targets
// 做读/增/删（与 internal/sshkeys.Store 同构）。owner 不由客户端发送（DB 默认 auth.uid()），
// 写入权限由 RLS（cli.is_admin()）裁决；错误码映射为可识别 sentinel。
const table = "proxy_targets"

// Store 封装对 cli.proxy_targets 的读写。*supabase.Client 在 cmd 层装配后注入。
type Store struct {
	client *supabase.Client
}

// NewStore 用一个已认证、schema 固定为 cli 的 *supabase.Client 构造 Store。
func NewStore(client *supabase.Client) *Store { return &Store{client: client} }

// addPayload 是 Add 写入的精简载荷：只发送客户端应控制的字段（省略 id/created_by/created_at）。
type addPayload struct {
	AppID  string `json:"app_id"`
	Target string `json:"target"`
	Note   string `json:"note,omitempty"`
}

// List 返回转发目标；appID 非空时按 app_id 过滤。结果按 (app_id, target) 稳定升序，
// 便于脚本/同步消费。RLS 自动放行任意已认证用户读取；空集返回空切片（非错误）。
func (s *Store) List(appID string) ([]Target, error) {
	fb := s.client.From(table).Select("*", "", false)
	if appID != "" {
		fb = fb.Eq("app_id", appID)
	}
	data, _, err := fb.ExecuteString()
	if err != nil {
		return nil, mapErr(err)
	}
	targets, err := decodeTargets(data)
	if err != nil {
		return nil, err
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].AppID != targets[j].AppID {
			return targets[i].AppID < targets[j].AppID
		}
		return targets[i].Target < targets[j].Target
	})
	return targets, nil
}

// Add 以 (app_id, target) 为冲突目标 upsert 一条目标（幂等：重复添加只更新 note）。
// 非管理员被 RLS 的 WITH CHECK 拒绝 → 42501 → ErrPermission。返回 DB 回写的记录。
func (s *Store) Add(appID, target, note string) (Target, error) {
	data, _, err := s.client.From(table).
		Upsert(addPayload{AppID: appID, Target: target, Note: note}, "app_id,target", "representation", "").
		ExecuteString()
	if err != nil {
		return Target{}, mapErr(err)
	}
	recs, err := decodeTargets(data)
	if err != nil {
		return Target{}, err
	}
	if len(recs) == 0 {
		return Target{}, fmt.Errorf("新增未返回记录: %w", ErrNotFound)
	}
	return recs[0], nil
}

// Remove 按引用删除：ref 像 UUID 则按 id 删，否则按 target 删（可叠加 appID 限定）。
// 删除默认 returning=representation，空数组表示未命中 → ErrNotFound。
// 注意：非管理员因 RLS USING(is_admin()) 看不到行，删除命中 0 行，同样表现为 ErrNotFound。
func (s *Store) Remove(appID, ref string) error {
	col := "target"
	if looksLikeUUID(ref) {
		col = "id"
	}
	q := s.client.From(table).Delete("", "").Eq(col, ref)
	if appID != "" && col == "target" {
		q = q.Eq("app_id", appID)
	}
	data, _, err := q.ExecuteString()
	if err != nil {
		return mapErr(err)
	}
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "[]" {
		return fmt.Errorf("ref %q: %w", ref, ErrNotFound)
	}
	return nil
}

// decodeTargets 把 PostgREST 返回的 JSON 数组解析为 []Target。空串/空数组返回空切片。
func decodeTargets(data string) ([]Target, error) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "[]" {
		return []Target{}, nil
	}
	var recs []Target
	if err := json.Unmarshal([]byte(trimmed), &recs); err != nil {
		return nil, fmt.Errorf("解析 proxy_targets 响应失败: %w", err)
	}
	return recs, nil
}

// permissionCodes 是 PostgREST/PostgreSQL 表示「权限不足 / 认证失败」的错误码。
var permissionCodes = []string{"42501", "PGRST301", "PGRST302"}

// mapErr 把 postgrest-go 的底层 error 归类：权限类 → ErrPermission，其余透传（%w 包裹）。
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

// looksLikeUUID 粗判 ref 是否为 UUID（36 长、5 段、4 个连字符），用于选 id/target 删除列。
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	return strings.Count(s, "-") == 4
}
