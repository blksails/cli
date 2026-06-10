package vault

// store.go 定义 Secret Vault 的持久层骨架：基于注入的、已认证的 *supabase.Client
// 对 blacksail.secrets 做密文 CRUD（design「Service Interface」）。
//
// 安全不变量（Requirement 6.3 / 7.3）：Store 只搬运密文与 (app,key) 元数据，绝不持有主密钥、
// 绝不做加解密；owner 不由客户端发送（由 DB 端默认值 auth.uid() + RLS 约束写入），故
// Secret 载荷不含 owner 字段。
//
// 注：CRUD 方法（Set/Get/ListKeys/List/Remove）由后续任务（2.1/2.2/2.3）实现；
// 本文件仅定义类型、构造与可识别的 sentinel 错误。

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"sort"

	"github.com/supabase-community/postgrest-go"
	"github.com/supabase-community/supabase-go"
)

// table 是 blacksail schema 下的 secrets 表名（与 migrations/secrets.sql 对齐）。
const table = "secrets"

// Secret 表示一条密文记录（value 字段恒为密文，不含明文）。
// json tag 与 blacksail.secrets 列名一一对应，直接用于 PostgREST 读写。
// 刻意不含 owner 字段：owner 由 DB 端 auth.uid() 默认值与 RLS 决定，不在此层传入（R7.3）。
type Secret struct {
	App   string `json:"app"`
	Key   string `json:"key"`
	Value string `json:"value"` // 密文：base64(nonce||ciphertext)，绝不存明文（R6.3）
}

// Store 封装对 blacksail.secrets 的密文 CRUD。
// 仅持有注入的 *supabase.Client——不持有主密钥、不做加解密；装配在 cmd 层完成。
type Store struct {
	client *supabase.Client
}

// NewStore 用一个已认证、schema 固定为 blacksail 的 *supabase.Client 构造 Store。
func NewStore(client *supabase.Client) *Store {
	return &Store{client: client}
}

// Set 将 (app, key) 的密文 ciphertext 以 upsert 写入 blacksail.secrets，
// on_conflict 目标为 (owner,app,key)，使同一用户在同一 app 下对同一 key 的二次写入
// 覆盖既有记录而非新增重复条目（Requirement 1.1/1.3；design「Service Interface」）。
//
// 载荷为 Secret{App,Key,Value}——刻意不含 owner：owner 由 DB 端 auth.uid() 默认值与
// RLS 决定归属，客户端绝不传入（Requirement 6.3）。value 即密文 base64(nonce||ciphertext)，
// 按原样发送，本层绝不持有主密钥、绝不加解密（Requirement 6.3）。
//
// 返回偏好 minimal（不回写 representation）：Set 仅需确认写入成功，无需读回行
// （design「Integration」：Upsert(payload, "owner,app,key", "minimal", "")）。
// 被 RLS 拒绝或权限不足映射为 ErrPermission；其余错误透传（以 %w 包裹保留原因）。
func (s *Store) Set(app, key, ciphertext string) error {
	payload := Secret{
		App:   app,
		Key:   key,
		Value: ciphertext,
	}

	_, _, err := s.client.From(table).
		Upsert(payload, "owner,app,key", "minimal", "").
		ExecuteString()
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// Get 按 (app, key) 取回单条记录的密文 value（Requirement 2.1）。
// 经 client.From("secrets").Select("value",...).Eq("app",app).Eq("key",key).Single()
// 以「单对象」语义读回；RLS 自动收敛到 auth.uid()，故无需也不应传 owner（R7.3）。
//
// 空结果映射（design「空结果数组映射为 ErrNotFound」，Postconditions）：postgrest-go 的
// Single() 通过 Accept: application/vnd.pgrst.object+json 声明单对象返回；当 (app,key)
// 命中 0 行时，PostgREST 返回 HTTP 406 + 错误码 PGRST116（"JSON object requested,
// multiple (or no) rows returned"），vendored 版本将其折叠为 "(PGRST116) ..."。本层据此
// 把 PGRST116 归为 ErrNotFound（Requirement 2.2）。权限类错误经 mapErr → ErrPermission。
//
// 本层只搬运密文：value 即 base64(nonce||ciphertext)，按原样返回，绝不加解密（R6.3）。
func (s *Store) Get(app, key string) (string, error) {
	data, _, err := s.client.From(table).
		Select("value", "", false).
		Eq("app", app).
		Eq("key", key).
		Single().
		ExecuteString()
	if err != nil {
		// Single() 下 0 行 => PostgREST 返回 PGRST116，映射为 ErrNotFound。
		if strings.Contains(err.Error(), "(PGRST116)") {
			return "", fmt.Errorf("app=%q key=%q: %w", app, key, ErrNotFound)
		}
		return "", mapErr(err)
	}

	// Single() 成功返回单个 JSON 对象 {"value": "..."}。
	var rec struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &rec); err != nil {
		return "", fmt.Errorf("解析 secrets 响应失败: %w", err)
	}
	return rec.Value, nil
}

// ListKeys 返回 app 下当前用户全部 secret 的 key 名，按稳定升序排序（Requirement 3.1/3.4）。
// 经 client.From("secrets").Select("key",...).Eq("app",app).Order("key", asc) 读回——
// 刻意仅 Select("key")，绝不取 value 列：list 只暴露 key 名，绝不取回任何密文或明文
// （Requirement 3.2/3.4）。归属由 RLS 收敛到 auth.uid()，故无需也不应传 owner。
//
// 稳定顺序双保险：①在 DB 端用 Order("key", Ascending) 声明 order=key.asc；②在 Go 端再对
// 结果 sort.Strings 一次，使返回顺序与 DB 实现/分页无关地确定（Requirement 3.1 稳定顺序，
// 便于脚本消费）。
//
// 空结果（app 下无任何 secret）返回空切片与 nil 错误——list 的空集不是错误，由命令层给出
// 友好空提示（Requirement 3.3）；不映射为 ErrNotFound（仅 Get/Remove 用 ErrNotFound）。
// 权限类错误经 mapErr → ErrPermission。
func (s *Store) ListKeys(app string) ([]string, error) {
	data, _, err := s.client.From(table).
		Select("key", "", false).
		Eq("app", app).
		Order("key", &postgrest.OrderOpts{Ascending: true}).
		ExecuteString()
	if err != nil {
		return nil, mapErr(err)
	}

	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "[]" {
		return []string{}, nil
	}

	// 仅解析 key 列：响应为 [{"key":"..."}, ...]，结构上不含 value（R3.2/R3.4）。
	var rows []struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
		return nil, fmt.Errorf("解析 secrets key 列表失败: %w", err)
	}

	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, r.Key)
	}
	// Go 端稳定升序，确保与 DB 返回顺序无关地确定（R3.1）。
	sort.Strings(keys)
	return keys, nil
}

// List 返回 app 下当前用户的全部 secret 完整记录（含密文 value），供 export 逐条解密
// （Requirement 5.1）。经 client.From("secrets").Select("app,key,value",...).Eq("app",app)
// .Order("key", asc) 读回——与 ListKeys 不同，List 取回含密文 value 的全记录，因为 export
// 需要密文才能逐条解密为 KEY=VALUE。本层只搬运密文，按原样返回，绝不加解密（R6.3）。
//
// 空结果（app 下无任何 secret）返回空切片与 nil 错误——export 空 app 输出空内容并正常退出
// （Requirement 5.4）；不映射为 ErrNotFound。权限类错误经 mapErr → ErrPermission。
func (s *Store) List(app string) ([]Secret, error) {
	data, _, err := s.client.From(table).
		Select("app,key,value", "", false).
		Eq("app", app).
		Order("key", &postgrest.OrderOpts{Ascending: true}).
		ExecuteString()
	if err != nil {
		return nil, mapErr(err)
	}

	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "[]" {
		return []Secret{}, nil
	}

	var recs []Secret
	if err := json.Unmarshal([]byte(trimmed), &recs); err != nil {
		return nil, fmt.Errorf("解析 secrets 记录列表失败: %w", err)
	}
	return recs, nil
}

// Remove 删除当前用户在 (app, key) 下的记录（Requirement 4.1）。
// 经 client.From("secrets").Delete("", "").Eq("app",app).Eq("key",key) 删除；Eq 双过滤
// 保证仅命中目标 key，同一 app 下其余 key 不受影响（Requirement 4.4），归属由 RLS 收敛。
//
// 记录不存在映射（Requirement 4.3）：Delete 默认 returning=representation，响应体为被删行
// 的 JSON 数组；据此可区分「确有删除」与「命中 0 行」——返回空数组（[]）即目标不存在，
// 映射为 ErrNotFound。权限类错误经 mapErr → ErrPermission。
func (s *Store) Remove(app, key string) error {
	data, _, err := s.client.From(table).
		Delete("", "").
		Eq("app", app).
		Eq("key", key).
		ExecuteString()
	if err != nil {
		return mapErr(err)
	}

	// returning=representation：空数组表示未命中任何行 => 记录不存在。
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "[]" {
		return fmt.Errorf("app=%q key=%q: %w", app, key, ErrNotFound)
	}
	var deleted []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &deleted); err != nil {
		return fmt.Errorf("解析 secrets 删除响应失败: %w", err)
	}
	if len(deleted) == 0 {
		return fmt.Errorf("app=%q key=%q: %w", app, key, ErrNotFound)
	}
	return nil
}

// 以下为可被调用方用 errors.Is 区分的 sentinel 错误。包裹时务必使用 %w 以保留可识别性。
var (
	// ErrPermission 表示被 RLS 拒绝或权限不足（PostgREST/PG 在 4xx 返回的权限类错误码）。
	// 与 sshkeys 层的同名 sentinel 语义一致，便于命令层统一给出「请先 bk auth login」类提示
	// （Requirement 7.3；design Postconditions）。
	ErrPermission = errors.New("权限不足：无权访问该 secret")

	// ErrNotFound 表示目标记录不存在或查询返回空集（Get/Remove 据此给出友好提示，
	// 见 design Postconditions）。包裹时使用 %w 以保留 errors.Is 可识别性。
	ErrNotFound = errors.New("未找到对应的 secret 记录")
)

// permissionCodes 是 PostgREST/PostgreSQL 表示「权限不足 / 认证失败」的错误码。
// postgrest-go v0.0.11 把 4xx 折叠为 "(<code>) <message>"，故据 code 识别：
//   - 42501：insufficient_privilege（RLS / 表权限拒绝，HTTP 403）。
//   - PGRST301 / PGRST302：JWT 失效或缺失（HTTP 401）。
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
