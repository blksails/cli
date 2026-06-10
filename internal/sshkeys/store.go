package sshkeys

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/supabase-community/supabase-go"
)

// store.go 实现「SSH 密钥发放」的持久层 Store：基于注入的、已认证的 *supabase.Client
// 对 blacksail.ssh_keys 做登记/列举/状态回写（design「persistence：sshkeys.Store」）。
//
// 安全不变量（Requirement 8.5）：Store 只搬运公钥与元数据，绝不生成密钥、绝不触碰私钥、
// 绝不做加解密；owner 不由客户端发送（由 DB 端默认值 auth.uid() + RLS 约束写入，Req 2.2）。
//
// 错误映射（Requirement 7.2/7.3）：vendored 的 postgrest-go v0.0.11 在 4xx 时把响应折叠成
// 形如 "(<pgcode>) <message>" 的普通 error（不保留 HTTP 状态码，见 execute.go）。因此本层
// 通过识别 PostgREST/PG 的权限类错误码把拒绝归类为 ErrPermission；单记录读到空集归类为
// ErrNotFound。两者均以 %w 包裹底层 error，保留 errors.Is 可识别性。

const table = "ssh_keys"

// Store 封装对 blacksail.ssh_keys 的读写。零依赖于 cmd——*supabase.Client 在 cmd 层装配后注入。
type Store struct {
	client *supabase.Client
}

// NewStore 用一个已认证、schema 固定为 blacksail 的 *supabase.Client 构造 Store。
func NewStore(client *supabase.Client) *Store {
	return &Store{client: client}
}

// registerPayload 是 Register 写入 DB 的精简载荷：只发送客户端应控制的字段。
// 刻意省略 owner（DB 默认 auth.uid()）、id、created_at 与审计字段，避免覆盖 DB 侧的默认/约束。
type registerPayload struct {
	Name        string `json:"name"`
	Host        string `json:"host"`
	DokkuUser   string `json:"dokku_user"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	Status      Status `json:"status"`
}

// Register 以 (owner,host) 为冲突目标 upsert 一条登记，状态恒置为 pending（Requirement 2.1/2.3）。
// owner 不随载荷发送：由 DB 端 auth.uid() 默认值与 RLS 决定归属（Requirement 2.2）。
// 返回 DB 回写的 representation（含 id/created_at），供命令层向用户确认指纹与状态（Requirement 2.5）。
func (s *Store) Register(rec KeyRecord) (KeyRecord, error) {
	payload := registerPayload{
		Name:        rec.Name,
		Host:        rec.Host,
		DokkuUser:   rec.DokkuUser,
		PublicKey:   rec.PublicKey,
		Fingerprint: rec.Fingerprint,
		Status:      StatusPending,
	}

	data, _, err := s.client.From(table).
		Upsert(payload, "owner,host", "representation", "").
		ExecuteString()
	if err != nil {
		return KeyRecord{}, mapErr(err)
	}

	recs, err := decodeRecords(data)
	if err != nil {
		return KeyRecord{}, err
	}
	if len(recs) == 0 {
		// upsert 正常应回写 representation；空集视为未登记成功。
		return KeyRecord{}, fmt.Errorf("登记未返回记录: %w", ErrNotFound)
	}
	return recs[0], nil
}

// ListMine 返回当前用户登记的全部记录。RLS 自动按 owner=auth.uid() 限定，故无需客户端过滤
// （Requirement 4.1/4.4）。空集返回空切片而非错误，由命令层给出友好空提示（Requirement 4.3）。
func (s *Store) ListMine() ([]KeyRecord, error) {
	data, _, err := s.client.From(table).
		Select("*", "", false).
		ExecuteString()
	if err != nil {
		return nil, mapErr(err)
	}
	return decodeRecords(data)
}

// ListPending 返回全部状态为 pending 的记录，供管理员代装（Requirement 5.1）。
// 仅管理员经 RLS 可成功；非管理员被拒时映射为 ErrPermission（Requirement 7.2/7.3）。
func (s *Store) ListPending() ([]KeyRecord, error) {
	data, _, err := s.client.From(table).
		Select("*", "", false).
		Eq("status", string(StatusPending)).
		ExecuteString()
	if err != nil {
		return nil, mapErr(err)
	}
	return decodeRecords(data)
}

// MarkInstalled 把记录 id 的状态回写为 installed，并记录安装者 by 与安装时间（Requirement 5.3, 8.1）。
// 仅管理员经 RLS 成功；被拒映射为 ErrPermission。
func (s *Store) MarkInstalled(id, by string) error {
	patch := map[string]any{
		"status":       StatusInstalled,
		"installed_by": by,
		"installed_at": time.Now().UTC().Format(time.RFC3339),
	}
	_, _, err := s.client.From(table).
		Update(patch, "representation", "").
		Eq("id", id).
		ExecuteString()
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// MarkRevoked 把记录 id 的状态回写为 revoked，并记录吊销者 by 与吊销时间（Requirement 6.2, 8.1）。
// 仅管理员经 RLS 成功；被拒映射为 ErrPermission。
func (s *Store) MarkRevoked(id, by string) error {
	patch := map[string]any{
		"status":     StatusRevoked,
		"revoked_by": by,
		"revoked_at": time.Now().UTC().Format(time.RFC3339),
	}
	_, _, err := s.client.From(table).
		Update(patch, "representation", "").
		Eq("id", id).
		ExecuteString()
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// Find 按指纹或名称定位单条记录（revoke 用，Requirement 6.1）。用 or 过滤同时匹配
// fingerprint 与 name 两列；空集映射为 ErrNotFound，被 RLS 拒绝映射为 ErrPermission。
func (s *Store) Find(ref string) (KeyRecord, error) {
	// PostgREST or 语法：or=(fingerprint.eq.<ref>,name.eq.<ref>)。
	orFilter := fmt.Sprintf("fingerprint.eq.%s,name.eq.%s", ref, ref)
	data, _, err := s.client.From(table).
		Select("*", "", false).
		Or(orFilter, "").
		ExecuteString()
	if err != nil {
		return KeyRecord{}, mapErr(err)
	}
	recs, err := decodeRecords(data)
	if err != nil {
		return KeyRecord{}, err
	}
	if len(recs) == 0 {
		return KeyRecord{}, fmt.Errorf("ref %q: %w", ref, ErrNotFound)
	}
	return recs[0], nil
}

// decodeRecords 把 PostgREST 返回的 JSON 数组解析为 []KeyRecord。空字符串/空数组返回空切片。
func decodeRecords(data string) ([]KeyRecord, error) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "[]" {
		return []KeyRecord{}, nil
	}
	var recs []KeyRecord
	if err := json.Unmarshal([]byte(trimmed), &recs); err != nil {
		return nil, fmt.Errorf("解析 ssh_keys 响应失败: %w", err)
	}
	return recs, nil
}

// permissionCodes 是 PostgREST/PostgreSQL 表示「权限不足 / 认证失败」的错误码。
// postgrest-go v0.0.11 把 4xx 折叠为 "(<code>) <message>"，故据 code 识别：
//   - 42501：insufficient_privilege（RLS / 表权限拒绝，HTTP 403）。
//   - PGRST301 / PGRST302：JWT 失效或缺失（HTTP 401）。
//   - PGRST116：单行期望下的 0 行（部分场景），此处不归为权限。
var permissionCodes = []string{"42501", "PGRST301", "PGRST302"}

// mapErr 把 postgrest-go 的底层 error 归类为可识别的 sentinel：权限类→ErrPermission，
// 其余透传（以 %w 包裹保留原因）。HTTP 状态码在 vendored 版本中不可见，故以错误码字符串识别。
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
