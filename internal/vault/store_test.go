package vault

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/supabase-community/supabase-go"
)

// captured 记录假 PostgREST 端点为某次请求观察到的关键字段，供断言出站请求。
type captured struct {
	method string
	path   string
	query  string
	prefer string
	body   string
}

// newTestStore 启动一个 httptest server 并返回一个由真实 *supabase.Client（schema
// 固定为 "blacksail"，与 cli-foundation 装配一致）支撑、指向该 server 的 Store，
// 使请求真正命中 <server>/rest/v1/secrets，走 postgrest-go 的真实传输。
func newTestStore(t *testing.T, handler http.HandlerFunc) *Store {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := supabase.NewClient(srv.URL, "anon-key", &supabase.ClientOptions{Schema: "blacksail"})
	if err != nil {
		t.Fatalf("supabase.NewClient: %v", err)
	}
	return NewStore(client)
}

// readBody 读取并返回请求体字符串。
func readBody(t *testing.T, r *http.Request) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// writeJSON 写一个带 JSON 原文的响应。
func writeJSON(w http.ResponseWriter, status int, raw string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, raw)
}

// writePGError 模拟 PostgREST 4xx 错误体（code+message），postgrest-go 会折叠为
// "(<code>) <message>" 形式的 error 字符串。
func writePGError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    code,
		"message": message,
	})
}

// TestNewStore 验证 NewStore 返回可用的非空 *Store 实例（即便 client 为 nil，
// 构造也不应 panic，仅持有注入的 client）。
func TestNewStore(t *testing.T) {
	s := NewStore(nil)
	if s == nil {
		t.Fatal("NewStore(nil) 返回 nil，期望非空 *Store")
	}
}

// TestErrNotFoundIsDistinct 验证 ErrNotFound 是一个可被 errors.Is 识别的、
// 非空 sentinel 错误（Get/Remove 在记录不存在时返回它，见 design Postconditions）。
func TestErrNotFoundIsDistinct(t *testing.T) {
	if ErrNotFound == nil {
		t.Fatal("ErrNotFound 为 nil，期望非空 sentinel 错误")
	}
	wrapped := errors.Join(errors.New("查询返回空集"), ErrNotFound)
	if !errors.Is(wrapped, ErrNotFound) {
		t.Error("包裹后的错误应能被 errors.Is 识别为 ErrNotFound")
	}
	if errors.Is(errors.New("其他错误"), ErrNotFound) {
		t.Error("无关错误不应被识别为 ErrNotFound")
	}
}

// TestSecretJSONTags 验证 Secret 的 JSON 序列化仅产出 app/key/value 三个键，
// 且绝不包含 owner（owner 由 DB 端 auth.uid() 决定，不在此层传入，R6.3/R7.3）。
func TestSecretJSONTags(t *testing.T) {
	sec := Secret{App: "myapp", Key: "DB_PASSWORD", Value: "Y2lwaGVydGV4dA=="}

	data, err := json.Marshal(sec)
	if err != nil {
		t.Fatalf("序列化 Secret 失败: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("反序列化为 map 失败: %v", err)
	}

	for _, want := range []string{"app", "key", "value"} {
		if _, ok := m[want]; !ok {
			t.Errorf("Secret JSON 缺少键 %q，实际 keys=%v", want, keysOf(m))
		}
	}
	if _, ok := m["owner"]; ok {
		t.Error("Secret JSON 不应包含 owner 键（owner 由 DB auth.uid() 决定）")
	}
	if len(m) != 3 {
		t.Errorf("Secret JSON 应恰有 3 个键(app/key/value)，实际 %d 个: %v", len(m), keysOf(m))
	}

	// 往返：序列化再反序列化应保留三字段原值。
	var back Secret
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("往返反序列化失败: %v", err)
	}
	if back != sec {
		t.Errorf("往返后值不一致: got %+v want %+v", back, sec)
	}
}

// TestSet_UpsertOnConflictOwnerAppKey_PayloadNoOwner 验证 Set 经 PostgREST 以
// upsert（Prefer: resolution=merge-duplicates）写入 secrets，on_conflict 为
// owner,app,key，且请求体仅含 app/key/value、绝不含 owner，密文按原样发送（R1.1, R6.3）。
func TestSet_UpsertOnConflictOwnerAppKey_PayloadNoOwner(t *testing.T) {
	const cipher = "Y2lwaGVydGV4dA==" // base64(nonce||ciphertext)，按原样发送
	var got captured
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		got = captured{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			prefer: r.Header.Get("Prefer"),
			body:   readBody(t, r),
		}
		// upsert minimal：返回空体即可。
		writeJSON(w, http.StatusCreated, ``)
	})

	if err := store.Set("myapp", "DB_PASSWORD", cipher); err != nil {
		t.Fatalf("Set 返回错误: %v", err)
	}

	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if !strings.HasSuffix(got.path, "/rest/v1/secrets") {
		t.Errorf("path = %q, want suffix /rest/v1/secrets", got.path)
	}
	if !strings.Contains(got.query, "on_conflict=owner%2Capp%2Ckey") && !strings.Contains(got.query, "on_conflict=owner,app,key") {
		t.Errorf("query = %q, want on_conflict=owner,app,key", got.query)
	}
	if !strings.Contains(got.prefer, "resolution=merge-duplicates") {
		t.Errorf("Prefer = %q, want resolution=merge-duplicates (upsert)", got.prefer)
	}

	// 请求体仅含 app/key/value，绝不泄露 owner，且密文原样发送。
	var payload map[string]any
	if err := json.Unmarshal([]byte(got.body), &payload); err != nil {
		t.Fatalf("payload 非 JSON 对象: %v (body=%s)", err, got.body)
	}
	if payload["app"] != "myapp" {
		t.Errorf("payload app = %v, want myapp", payload["app"])
	}
	if payload["key"] != "DB_PASSWORD" {
		t.Errorf("payload key = %v, want DB_PASSWORD", payload["key"])
	}
	if payload["value"] != cipher {
		t.Errorf("payload value = %v, want 原样密文 %q", payload["value"], cipher)
	}
	if v, ok := payload["owner"]; ok && v != "" {
		t.Errorf("payload 不应发送 owner（DB 端 auth.uid() 决定），got %v", v)
	}
}

// TestSet_RepeatedSetUpsertsNotDuplicate 验证对同一 (app,key) 二次 Set 仍走
// upsert（merge-duplicates + on_conflict=owner,app,key），由唯一约束折叠为覆盖
// 而非新增第二条记录（R1.3）。在 store 单测边界，断言两次写入都以覆盖语义发出。
func TestSet_RepeatedSetUpsertsNotDuplicate(t *testing.T) {
	var calls []captured
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, captured{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			prefer: r.Header.Get("Prefer"),
			body:   readBody(t, r),
		})
		writeJSON(w, http.StatusCreated, ``)
	})

	if err := store.Set("myapp", "DB_PASSWORD", "Y2lwaGVyMQ=="); err != nil {
		t.Fatalf("首次 Set 错误: %v", err)
	}
	if err := store.Set("myapp", "DB_PASSWORD", "Y2lwaGVyMg=="); err != nil {
		t.Fatalf("二次 Set 错误: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("期望两次写入请求，实际 %d", len(calls))
	}
	for i, c := range calls {
		// 每次都必须是带 merge-duplicates 与 on_conflict=owner,app,key 的 upsert，
		// 才能让 DB 端唯一约束把同 (app,key) 折叠为覆盖而非插入重复。
		if c.method != http.MethodPost {
			t.Errorf("call %d method = %q, want POST", i, c.method)
		}
		if !strings.Contains(c.prefer, "resolution=merge-duplicates") {
			t.Errorf("call %d Prefer = %q, want merge-duplicates", i, c.prefer)
		}
		if !strings.Contains(c.query, "on_conflict=owner%2Capp%2Ckey") && !strings.Contains(c.query, "on_conflict=owner,app,key") {
			t.Errorf("call %d query = %q, want on_conflict=owner,app,key", i, c.query)
		}
	}
}

// TestSet_RLSDenied_MapsErrPermission 验证 RLS/权限拒绝（PG 42501）被映射为
// 可被 errors.Is 识别的 ErrPermission（与 sshkeys 错误映射一致）。
func TestSet_RLSDenied_MapsErrPermission(t *testing.T) {
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		writePGError(w, http.StatusForbidden, "42501", "permission denied for table secrets")
	})
	err := store.Set("myapp", "DB_PASSWORD", "Y2lwaGVy")
	if err == nil {
		t.Fatal("RLS 拒绝时 Set 必须返回错误")
	}
	if !errors.Is(err, ErrPermission) {
		t.Errorf("err = %v, want errors.Is ErrPermission", err)
	}
}

// TestGet_FiltersAppKeyAndReturnsValue 验证 Get 经 PostgREST 以
// select=value、过滤 app=eq.<app> 且 key=eq.<key>、单行（Accept:
// application/vnd.pgrst.object+json）读回密文，并解析出 value 原样返回（R2.1）。
func TestGet_FiltersAppKeyAndReturnsValue(t *testing.T) {
	const cipher = "cipher-xyz"
	var got captured
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		got = captured{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			prefer: r.Header.Get("Accept"),
			body:   readBody(t, r),
		}
		// Single() 期望单个 JSON 对象（非数组）。
		writeJSON(w, http.StatusOK, `{"value":"`+cipher+`"}`)
	})

	v, err := store.Get("myapp", "DB_PASSWORD")
	if err != nil {
		t.Fatalf("Get 返回错误: %v", err)
	}
	if v != cipher {
		t.Errorf("Get value = %q, want %q", v, cipher)
	}

	if got.method != http.MethodGet {
		t.Errorf("method = %q, want GET", got.method)
	}
	if !strings.HasSuffix(got.path, "/rest/v1/secrets") {
		t.Errorf("path = %q, want suffix /rest/v1/secrets", got.path)
	}
	if !strings.Contains(got.query, "app=eq.myapp") {
		t.Errorf("query = %q, want app=eq.myapp", got.query)
	}
	if !strings.Contains(got.query, "key=eq.DB_PASSWORD") {
		t.Errorf("query = %q, want key=eq.DB_PASSWORD", got.query)
	}
	if !strings.Contains(got.query, "select=value") {
		t.Errorf("query = %q, want select=value", got.query)
	}
	// Single() 通过 Accept 头声明单对象返回。
	if !strings.Contains(got.prefer, "application/vnd.pgrst.object+json") {
		t.Errorf("Accept = %q, want application/vnd.pgrst.object+json (Single)", got.prefer)
	}
}

// TestGet_NotFound_MapsErrNotFound 验证当 (app,key) 不存在时，PostgREST 在
// Single() 下返回 HTTP 406 + 错误码 PGRST116（0 行），Get 将其映射为可被
// errors.Is 识别的 ErrNotFound（R2.2；design「空结果数组映射为 ErrNotFound」）。
func TestGet_NotFound_MapsErrNotFound(t *testing.T) {
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		// PostgREST 在 Single() 下，结果集为 0 行时返回 406 + PGRST116。
		writePGError(w, http.StatusNotAcceptable, "PGRST116",
			"JSON object requested, multiple (or no) rows returned")
	})

	_, err := store.Get("myapp", "MISSING")
	if err == nil {
		t.Fatal("Get 对不存在记录必须返回错误")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want errors.Is ErrNotFound", err)
	}
}

// TestGet_RLSDenied_MapsErrPermission 验证 RLS/权限拒绝（PG 42501）在 Get 上
// 也被映射为 ErrPermission（与 Set 一致）。
func TestGet_RLSDenied_MapsErrPermission(t *testing.T) {
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		writePGError(w, http.StatusForbidden, "42501", "permission denied for table secrets")
	})
	_, err := store.Get("myapp", "DB_PASSWORD")
	if err == nil {
		t.Fatal("RLS 拒绝时 Get 必须返回错误")
	}
	if !errors.Is(err, ErrPermission) {
		t.Errorf("err = %v, want errors.Is ErrPermission", err)
	}
}

// TestRemove_FiltersAppKeyAndDeletesTarget 验证 Remove 以 DELETE 方法、过滤
// app=eq.<app> 且 key=eq.<key>，仅删目标 key；返回被删行（representation）非空时
// 返回 nil（R4.1, R4.4）。过滤条件保证仅命中目标 key（其余 key 不受影响）。
func TestRemove_FiltersAppKeyAndDeletesTarget(t *testing.T) {
	var got captured
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		got = captured{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			prefer: r.Header.Get("Prefer"),
			body:   readBody(t, r),
		}
		// Delete 默认 returning=representation：返回被删行数组（非空 => 确有删除）。
		writeJSON(w, http.StatusOK, `[{"app":"myapp","key":"DB_PASSWORD","value":"cipher"}]`)
	})

	if err := store.Remove("myapp", "DB_PASSWORD"); err != nil {
		t.Fatalf("Remove 返回错误: %v", err)
	}

	if got.method != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", got.method)
	}
	if !strings.HasSuffix(got.path, "/rest/v1/secrets") {
		t.Errorf("path = %q, want suffix /rest/v1/secrets", got.path)
	}
	if !strings.Contains(got.query, "app=eq.myapp") {
		t.Errorf("query = %q, want app=eq.myapp", got.query)
	}
	if !strings.Contains(got.query, "key=eq.DB_PASSWORD") {
		t.Errorf("query = %q, want key=eq.DB_PASSWORD（仅删目标 key）", got.query)
	}
	// representation 返回头，使我们能据返回行数判定是否真有删除。
	if !strings.Contains(got.prefer, "return=representation") {
		t.Errorf("Prefer = %q, want return=representation", got.prefer)
	}
}

// TestRemove_NotFound_MapsErrNotFound 验证当 (app,key) 不存在时，DELETE 命中 0 行、
// representation 返回空数组，Remove 映射为 ErrNotFound（R4.3）。
func TestRemove_NotFound_MapsErrNotFound(t *testing.T) {
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		// 0 行被删：返回空数组。
		writeJSON(w, http.StatusOK, `[]`)
	})

	err := store.Remove("myapp", "MISSING")
	if err == nil {
		t.Fatal("Remove 对不存在记录必须返回错误")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want errors.Is ErrNotFound", err)
	}
}

// TestListKeys_SelectsKeyOnly_NoValue_StableOrder 验证 ListKeys 经 PostgREST 仅
// select=key（绝不取 value，R3.2/R3.4）、过滤 app=eq.<app>，并对返回的乱序 key 做稳定
// 升序排序（R3.1/R3.4：每行一个 key 的稳定顺序）。返回 []string 结构上不含任何 value。
func TestListKeys_SelectsKeyOnly_NoValue_StableOrder(t *testing.T) {
	var got captured
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		got = captured{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			prefer: r.Header.Get("Accept"),
			body:   readBody(t, r),
		}
		// 服务端故意返回乱序（B 在前），用于验证客户端稳定排序。
		writeJSON(w, http.StatusOK, `[{"key":"B_KEY"},{"key":"A_KEY"}]`)
	})

	keys, err := store.ListKeys("myapp")
	if err != nil {
		t.Fatalf("ListKeys 返回错误: %v", err)
	}

	// 稳定升序：无论 DB 返回顺序如何，结果都确定为 [A_KEY, B_KEY]。
	want := []string{"A_KEY", "B_KEY"}
	if len(keys) != len(want) {
		t.Fatalf("ListKeys 返回 %d 个 key，期望 %d: %v", len(keys), len(want), keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("ListKeys[%d] = %q, want %q（稳定升序）", i, keys[i], want[i])
		}
	}

	if got.method != http.MethodGet {
		t.Errorf("method = %q, want GET", got.method)
	}
	if !strings.HasSuffix(got.path, "/rest/v1/secrets") {
		t.Errorf("path = %q, want suffix /rest/v1/secrets", got.path)
	}
	if !strings.Contains(got.query, "app=eq.myapp") {
		t.Errorf("query = %q, want app=eq.myapp", got.query)
	}
	// 关键不变量：仅取 key 列，绝不取 value（R3.2/R3.4）。
	if !strings.Contains(got.query, "select=key") {
		t.Errorf("query = %q, want select=key（仅取 key 列）", got.query)
	}
	if strings.Contains(got.query, "value") {
		t.Errorf("query = %q 不应包含 value（ListKeys 绝不取密文）", got.query)
	}
	// 同时声明 DB 端 order=key.asc，使 DB 与 Go 排序双重保证确定性。
	if !strings.Contains(got.query, "order=key") {
		t.Errorf("query = %q, want order=key（DB 端稳定排序）", got.query)
	}
}

// TestListKeys_Empty_ReturnsEmptySlice 验证 app 下无任何 secret 时，ListKeys 返回
// 空切片与 nil 错误（不视为 ErrNotFound——list 的空集是友好的空，由 cmd 层处理，R3.3）。
func TestListKeys_Empty_ReturnsEmptySlice(t *testing.T) {
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, `[]`)
	})

	keys, err := store.ListKeys("emptyapp")
	if err != nil {
		t.Fatalf("ListKeys 空集应返回 nil 错误，got %v", err)
	}
	if keys == nil {
		t.Error("ListKeys 空集应返回非 nil 的空切片，got nil")
	}
	if len(keys) != 0 {
		t.Errorf("ListKeys 空集应返回 0 个 key，got %d: %v", len(keys), keys)
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("ListKeys 空集不应映射为 ErrNotFound（list 的空集非错误）")
	}
}

// TestListKeys_RLSDenied_MapsErrPermission 验证 RLS/权限拒绝（PG 42501）在 ListKeys
// 上被映射为可被 errors.Is 识别的 ErrPermission（与 Set/Get 一致，R7.x）。
func TestListKeys_RLSDenied_MapsErrPermission(t *testing.T) {
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		writePGError(w, http.StatusForbidden, "42501", "permission denied for table secrets")
	})
	_, err := store.ListKeys("myapp")
	if err == nil {
		t.Fatal("RLS 拒绝时 ListKeys 必须返回错误")
	}
	if !errors.Is(err, ErrPermission) {
		t.Errorf("err = %v, want errors.Is ErrPermission", err)
	}
}

// TestList_ReturnsFullRecordsWithCiphertext 验证 List 经 PostgREST 取回 app 下全部
// 完整记录（app/key/value 三列，含密文 value，供 export 解密用，R5.1），过滤
// app=eq.<app>，并解析为含密文的 []Secret。
func TestList_ReturnsFullRecordsWithCiphertext(t *testing.T) {
	var got captured
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		got = captured{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			prefer: r.Header.Get("Accept"),
			body:   readBody(t, r),
		}
		writeJSON(w, http.StatusOK,
			`[{"app":"myapp","key":"A_KEY","value":"cipher1"},`+
				`{"app":"myapp","key":"B_KEY","value":"cipher2"}]`)
	})

	recs, err := store.List("myapp")
	if err != nil {
		t.Fatalf("List 返回错误: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("List 返回 %d 条记录，期望 2: %+v", len(recs), recs)
	}

	// 全记录含密文 value，供 export 逐条解密（R5.1）。
	byKey := map[string]Secret{}
	for _, r := range recs {
		byKey[r.Key] = r
	}
	if a := byKey["A_KEY"]; a.App != "myapp" || a.Value != "cipher1" {
		t.Errorf("A_KEY 记录 = %+v, want app=myapp value=cipher1（含密文）", a)
	}
	if b := byKey["B_KEY"]; b.App != "myapp" || b.Value != "cipher2" {
		t.Errorf("B_KEY 记录 = %+v, want app=myapp value=cipher2（含密文）", b)
	}

	if got.method != http.MethodGet {
		t.Errorf("method = %q, want GET", got.method)
	}
	if !strings.HasSuffix(got.path, "/rest/v1/secrets") {
		t.Errorf("path = %q, want suffix /rest/v1/secrets", got.path)
	}
	if !strings.Contains(got.query, "app=eq.myapp") {
		t.Errorf("query = %q, want app=eq.myapp", got.query)
	}
	// 与 ListKeys 不同：List 必须取 value 列（含密文，供 export）。
	if !strings.Contains(got.query, "value") {
		t.Errorf("query = %q, want select 含 value（List 取全记录含密文）", got.query)
	}
}

// TestList_Empty_ReturnsEmptySlice 验证 app 下无任何 secret 时，List 返回空切片与
// nil 错误（不视为 ErrNotFound；export 空 app 输出空内容并正常退出，R5.4）。
func TestList_Empty_ReturnsEmptySlice(t *testing.T) {
	store := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, `[]`)
	})

	recs, err := store.List("emptyapp")
	if err != nil {
		t.Fatalf("List 空集应返回 nil 错误，got %v", err)
	}
	if recs == nil {
		t.Error("List 空集应返回非 nil 的空切片，got nil")
	}
	if len(recs) != 0 {
		t.Errorf("List 空集应返回 0 条记录，got %d: %+v", len(recs), recs)
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("List 空集不应映射为 ErrNotFound（list 的空集非错误）")
	}
}

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
