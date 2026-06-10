package cmd

import (
	"bytes"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/vault"
)

// vault_integration_test.go 是 Secret Vault 的 set→get→list→rm 往返集成回归（Task 5.1）：
// 以注入式把四个可测核心（runVaultSet / runVaultGet / runVaultList / runVaultRm）串联成
// 完整生命周期，验证主路径与覆盖语义在真实加解密下端到端正确：
//   - set 写入后 Supabase 侧仅持有密文（≠明文），get 用真实主密钥解密取回原明文（R1.1/R2.1/R2.4）。
//   - get 输出仅明文本身（无 key 名/标签），list 仅列 key 名（无任何 value），顺序稳定（R3.1）。
//   - rm 后该 key 从 list 消失而其余 key 保留（R4.1/R4.4）。
//   - 对同 (app,key) 二次 set 为覆盖（不新增重复条目），get 取回新值（R1.3/R2.4）。
//
// 本测试仅依赖既有生产缝（runVaultSet/runVaultGet/runVaultList/runVaultRm 及其注入接口）与
// 真实加解密（internal/vault.Encrypt/Decrypt + 真实主密钥），不触碰任何生产代码或其它 _test.go。
// fake 命名以 e2e 前缀避免与既有 fakeVaultSetter/fakeVaultGetter/fakeVaultLister/fakeVaultRemover 冲突。

// e2eVaultStore 是一个 backing 单一共享 map 的内存存储，单结构即满足
// vaultSetter + vaultGetter + vaultLister + vaultRemover 四个注入接口，
// 使密文状态可跨 set/get/list/rm 各步骤连续流转——镜像真实 *vault.Store 的语义：
// 仅搬运密文（app→key→CIPHERTEXT），绝不持有主密钥、绝不加解密。
type e2eVaultStore struct {
	// data[app][key] = ciphertext（base64(nonce||ciphertext)），刻意只存密文，不存明文。
	data map[string]map[string]string
}

func newE2EVaultStore() *e2eVaultStore {
	return &e2eVaultStore{data: map[string]map[string]string{}}
}

// Set 以 (app,key) 维度 upsert 密文（满足 vaultSetter）。同 (app,key) 二次写入覆盖既有值
// 而非新增重复条目——镜像 *vault.Store.Set 的 on_conflict(owner,app,key) upsert 语义（R1.3）。
func (s *e2eVaultStore) Set(app, key, ciphertext string) error {
	if s.data[app] == nil {
		s.data[app] = map[string]string{}
	}
	s.data[app][key] = ciphertext
	return nil
}

// Get 按 (app,key) 取回密文（满足 vaultGetter）。命中 0 行返回 vault.ErrNotFound，
// 镜像 *vault.Store.Get 把 PGRST116 归为 ErrNotFound 的语义（R2.2）。只搬运密文，绝不解密（R6.3）。
func (s *e2eVaultStore) Get(app, key string) (string, error) {
	if m, ok := s.data[app]; ok {
		if ct, ok := m[key]; ok {
			return ct, nil
		}
	}
	return "", vault.ErrNotFound
}

// ListKeys 返回 app 下全部 key 名，按稳定升序排序（满足 vaultLister）。刻意只取 key 名，
// 绝不返回 value——镜像 *vault.Store.ListKeys 仅 Select("key") 的结构性约束（R3.2/R3.4）。
func (s *e2eVaultStore) ListKeys(app string) ([]string, error) {
	m := s.data[app]
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// Remove 删除 (app,key)（满足 vaultRemover）。命中 0 行返回 vault.ErrNotFound；仅删目标 key，
// 同一 app 下其余 key 不受影响——镜像 *vault.Store.Remove 的 .Eq(app).Eq(key) 双过滤（R4.4）。
func (s *e2eVaultStore) Remove(app, key string) error {
	if m, ok := s.data[app]; ok {
		if _, ok := m[key]; ok {
			delete(m, key)
			return nil
		}
	}
	return vault.ErrNotFound
}

// TestVaultLifecycle_EndToEnd 串联 set→get→list→rm 主路径 + 覆盖语义，全程真实加解密。
func TestVaultLifecycle_EndToEnd(t *testing.T) {
	// 真实主密钥：经 LoadOrCreateKey 在临时目录生成并持久化一个真实 32 字节 AES-256 密钥，
	// 使 store 持有的是真实 AES-GCM 密文、get 是真实解密往返（不是恒等 stub）。
	key, err := vault.LoadOrCreateKey(filepath.Join(t.TempDir(), "vault.key"))
	if err != nil {
		t.Fatalf("LoadOrCreateKey 失败: %v", err)
	}

	store := newE2EVaultStore()
	const app = "myapp"

	// --- 步骤 1：set 写入 A、B 两个明文，store 侧只应持有密文（R1.1）。 ---
	var setBuf bytes.Buffer
	if err := runVaultSet(&setBuf, app, []string{"A=secretA", "B=secretB"}, key, store, vault.Encrypt); err != nil {
		t.Fatalf("runVaultSet 失败: %v", err)
	}
	if got := len(store.data[app]); got != 2 {
		t.Fatalf("set 后期望 store 有 2 个条目，实得 %d", got)
	}
	// R1.1 关键断言：Supabase 侧绝不持有明文——存储值必须是密文（≠原明文）。
	if ct := store.data[app]["A"]; ct == "secretA" || ct == "" {
		t.Errorf("A 的存储值应为密文，却得到明文/空：%q", ct)
	}
	if ct := store.data[app]["B"]; ct == "secretB" || ct == "" {
		t.Errorf("B 的存储值应为密文，却得到明文/空：%q", ct)
	}
	// 确认行不得回显明文 VALUE（R1.7/R6.2）。
	if out := setBuf.String(); strings.Contains(out, "secretA") || strings.Contains(out, "secretB") {
		t.Errorf("set 确认输出泄露了明文 VALUE：%q", out)
	}

	// --- 步骤 2：get A → 真实解密往返，输出恰为原明文且仅明文（无 key 名）（R2.1/R2.4）。 ---
	var getBuf bytes.Buffer
	if err := runVaultGet(&getBuf, app, "A", key, store, vault.Decrypt); err != nil {
		t.Fatalf("runVaultGet(A) 失败: %v", err)
	}
	if got := strings.TrimSpace(getBuf.String()); got != "secretA" {
		t.Errorf("get A 期望明文 %q，实得 %q", "secretA", got)
	}
	// 仅明文：输出不得含 key 名 "A"（除非明文本身含），也不得含密文。
	if strings.Contains(getBuf.String(), store.data[app]["A"]) {
		t.Errorf("get A 输出泄露了密文：%q", getBuf.String())
	}

	// --- 步骤 3：list → 仅列 key 名（A、B），稳定顺序，绝不含值（R3.1/R3.2）。 ---
	var listBuf bytes.Buffer
	if err := runVaultList(&listBuf, app, store); err != nil {
		t.Fatalf("runVaultList 失败: %v", err)
	}
	listLines := nonEmptyLines(listBuf.String())
	if want := []string{"A", "B"}; !equalStrings(listLines, want) {
		t.Errorf("list 期望按稳定顺序列出 %v，实得 %v", want, listLines)
	}
	// list 绝不暴露任何 value（明文或密文）。
	if out := listBuf.String(); strings.Contains(out, "secretA") || strings.Contains(out, "secretB") ||
		strings.Contains(out, store.data[app]["A"]) || strings.Contains(out, store.data[app]["B"]) {
		t.Errorf("list 输出泄露了 value：%q", out)
	}

	// --- 步骤 4：rm A → 确认；随后 list 仅剩 B（A 消失、B 保留）（R4.1/R4.4）。 ---
	var rmBuf bytes.Buffer
	if err := runVaultRm(&rmBuf, app, "A", store); err != nil {
		t.Fatalf("runVaultRm(A) 失败: %v", err)
	}
	if !strings.Contains(rmBuf.String(), "A") {
		t.Errorf("rm 确认输出应提及被删 key A，实得：%q", rmBuf.String())
	}
	if _, ok := store.data[app]["A"]; ok {
		t.Errorf("rm 后 A 仍存在于 store")
	}

	var listBuf2 bytes.Buffer
	if err := runVaultList(&listBuf2, app, store); err != nil {
		t.Fatalf("runVaultList(rm 后) 失败: %v", err)
	}
	if want := []string{"B"}; !equalStrings(nonEmptyLines(listBuf2.String()), want) {
		t.Errorf("rm A 后 list 期望仅剩 %v，实得 %v", want, nonEmptyLines(listBuf2.String()))
	}
	// B 的明文仍可解密取回——R4.4：删 A 未殃及 B。
	var getBBuf bytes.Buffer
	if err := runVaultGet(&getBBuf, app, "B", key, store, vault.Decrypt); err != nil {
		t.Fatalf("runVaultGet(B, rm 后) 失败: %v", err)
	}
	if got := strings.TrimSpace(getBBuf.String()); got != "secretB" {
		t.Errorf("rm A 后 get B 期望仍为 %q，实得 %q", "secretB", got)
	}

	// --- 步骤 5：覆盖语义（R1.3）：对同 (app,B) 二次 set → 仍只 1 个 B 条目（不新增重复）。 ---
	oldCipherB := store.data[app]["B"]
	var ovrBuf bytes.Buffer
	if err := runVaultSet(&ovrBuf, app, []string{"B=newB"}, key, store, vault.Encrypt); err != nil {
		t.Fatalf("runVaultSet(覆盖 B) 失败: %v", err)
	}
	if got := len(store.data[app]); got != 1 {
		t.Fatalf("覆盖 B 后期望 store 仍只 1 个条目（B），实得 %d", got)
	}
	if newCipher := store.data[app]["B"]; newCipher == oldCipherB {
		t.Errorf("覆盖后 B 的密文应更新（重新加密 newB），却与旧密文相同")
	}
	// get B → 取回的是覆盖后的新值（R1.3/R2.4）。
	var getNewBBuf bytes.Buffer
	if err := runVaultGet(&getNewBBuf, app, "B", key, store, vault.Decrypt); err != nil {
		t.Fatalf("runVaultGet(B, 覆盖后) 失败: %v", err)
	}
	if got := strings.TrimSpace(getNewBBuf.String()); got != "newB" {
		t.Errorf("覆盖后 get B 期望新值 %q，实得 %q", "newB", got)
	}
}

// nonEmptyLines 把多行文本切成去除首尾空白的非空行切片，便于断言 list 的逐行 key 输出。
func nonEmptyLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// equalStrings 按序比较两个字符串切片是否完全相等。
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
