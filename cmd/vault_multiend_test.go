package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/vault"
)

// vault_multiend_test.go 是 Secret Vault 的「多端一致性」cmd 级回归（Task 5.3）：
// 以真实加解密在两个独立的「端」（A 与 B）上验证 R7.3/R7.4 的安全模型核心承诺——
// 「密文上云 + 主密钥留本机」：A 端写入的是 AES-256-GCM 密文（共享 Supabase 侧只持有密文），
// B 端以**相同身份 + 相同主密钥**取回时必能解出与 A 写入端一致的明文；而换一把不同的
// 主密钥（keyC）则解密必败——证明一致性来自「同一把 vault.key + 共享密文」，并非魔法。
//
// 建模：
//   - 共享云（Supabase）：单一 multiVaultStore，只搬运密文（app→key→CIPHERTEXT），
//     A 端 set 写入、B 端 get/export 读取，二者共享同一 store 实例 = 共享同一份云端密文。
//   - 本机主密钥（local-but-identical）：A、B 各自从**同一份字节内容**的 vault.key 加载
//     （把 tmpA/vault.key 拷到 tmpB/vault.key 再 LoadOrCreateKey），断言 keyA==keyB。
//
// 本测试仅依赖既有生产缝（runVaultSet/runVaultGet/runVaultExport 及其注入接口）与真实
// internal/vault.Encrypt/Decrypt + 真实主密钥，不触碰任何生产代码或其它 _test.go。
// store 命名以 multi 前缀，避免与 e2eVaultStore（vault_integration_test.go）/
// secVaultStore（vault_security_test.go）在同一 cmd 包内重复声明；复用既有
// nonEmptyLines / equalStrings 辅助（不重复声明）。

// multiVaultStore 是 backing 单一共享 map 的内存存储，单结构即满足
// vaultSetter（Set）+ vaultGetter（Get）+ vaultListerFull（List）三个注入接口，
// 使一份密文状态可在 A 端 set 与 B 端 get/export 之间连续流转——镜像真实 *vault.Store：
// 只搬运密文（app→key→CIPHERTEXT），绝不持有主密钥、绝不加解密（R6.3）。
type multiVaultStore struct {
	// data[app][key] = ciphertext（base64(nonce||ciphertext)），刻意只存密文，不存明文。
	data map[string]map[string]string
}

func newMultiVaultStore() *multiVaultStore {
	return &multiVaultStore{data: map[string]map[string]string{}}
}

// Set 以 (app,key) 维度 upsert 密文（满足 vaultSetter）。
func (s *multiVaultStore) Set(app, key, ciphertext string) error {
	if s.data[app] == nil {
		s.data[app] = map[string]string{}
	}
	s.data[app][key] = ciphertext
	return nil
}

// Get 按 (app,key) 取回密文（满足 vaultGetter）。命中 0 行返回 vault.ErrNotFound，
// 镜像 *vault.Store.Get 的 ErrNotFound 语义。只搬运密文，绝不解密（R6.3）。
func (s *multiVaultStore) Get(app, key string) (string, error) {
	if m, ok := s.data[app]; ok {
		if ct, ok := m[key]; ok {
			return ct, nil
		}
	}
	return "", vault.ErrNotFound
}

// List 返回 app 下全部完整记录（含密文 value），按 key 升序稳定排序（满足 vaultListerFull）——
// 镜像 *vault.Store.List 的稳定顺序，供 runVaultExport 逐条解密。
func (s *multiVaultStore) List(app string) ([]vault.Secret, error) {
	m := s.data[app]
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]vault.Secret, 0, len(keys))
	for _, k := range keys {
		out = append(out, vault.Secret{App: app, Key: k, Value: m[k]})
	}
	return out, nil
}

// TestVaultMultiEnd_SameKeySameCloud_ConsistentPlaintext 验证「相同身份 + 相同主密钥 +
// 共享云端密文」下，A 端 set 的明文在 B 端 get/export 取回一致（R7.3/R7.4），并以不同主密钥
// 的负向控制证明一致性确由同一把密钥保证（R7.4）。全程真实 AES-256-GCM 加解密。
func TestVaultMultiEnd_SameKeySameCloud_ConsistentPlaintext(t *testing.T) {
	const app = "myapp"

	// 共享云：A 端写、B 端读，同一 store 实例 = 共享同一份云端密文。
	store := newMultiVaultStore()

	// --- End-A：在 tmpA 生成真实主密钥并 set 写入两个明文 ---
	tmpA := t.TempDir()
	keyAPath := filepath.Join(tmpA, "vault.key")
	keyA, err := vault.LoadOrCreateKey(keyAPath)
	if err != nil {
		t.Fatalf("End-A LoadOrCreateKey 失败: %v", err)
	}

	var bufA bytes.Buffer
	if err := runVaultSet(&bufA, app, []string{"TOKEN=multiend-secret", "DB=pg://x"}, keyA, store, vault.Encrypt); err != nil {
		t.Fatalf("End-A runVaultSet 失败: %v", err)
	}
	// A 端写入后，共享云侧只应持有密文（≠明文）——「密文上云」前提。
	if ct := store.data[app]["TOKEN"]; ct == "multiend-secret" || ct == "" {
		t.Fatalf("End-A 写入后 TOKEN 应为密文，却得到明文/空：%q", ct)
	}
	if ct := store.data[app]["DB"]; ct == "pg://x" || ct == "" {
		t.Fatalf("End-A 写入后 DB 应为密文，却得到明文/空：%q", ct)
	}

	// --- End-B：模拟另一台机器，持有内容相同的 vault.key（把 A 的 key 文件原样拷到 tmpB）---
	// 注意：vault.key 在磁盘上是 base64 编码的密钥串，故按**原始文件字节**逐字拷贝（而非 decode 后
	// 的密钥字节），让 B 端 LoadOrCreateKey 读取到与 A 端完全相同的持久化文件。
	tmpB := t.TempDir()
	keyBPath := filepath.Join(tmpB, "vault.key")
	keyAFileBytes, err := os.ReadFile(keyAPath)
	if err != nil {
		t.Fatalf("读取 End-A vault.key 文件失败: %v", err)
	}
	if err := writeKeyFile(t, keyBPath, keyAFileBytes); err != nil {
		t.Fatalf("拷贝主密钥文件到 End-B 失败: %v", err)
	}
	keyB, err := vault.LoadOrCreateKey(keyBPath)
	if err != nil {
		t.Fatalf("End-B LoadOrCreateKey 失败: %v", err)
	}
	// 关键前提（R7.4）：B 端持有的就是与 A 端相同的主密钥材料。
	if !bytes.Equal(keyA, keyB) {
		t.Fatalf("End-A 与 End-B 主密钥应相同（多端共享同一 vault.key），却不同")
	}

	// --- End-B get：用相同主密钥解密 A 端写入的共享密文，输出恰为原明文（R7.3/R7.4）---
	var bufB bytes.Buffer
	if err := runVaultGet(&bufB, app, "TOKEN", keyB, store, vault.Decrypt); err != nil {
		t.Fatalf("End-B runVaultGet(TOKEN) 失败: %v", err)
	}
	if got := strings.TrimSpace(bufB.String()); got != "multiend-secret" {
		t.Errorf("End-B get TOKEN 期望取回 A 端写入的明文 %q，实得 %q", "multiend-secret", got)
	}

	// --- End-B export：app 下两条 secret 均在 B 端解密成功并往返一致（R7.3/R7.4）---
	var bufE bytes.Buffer
	if err := runVaultExport(&bufE, app, keyB, store, vault.Decrypt); err != nil {
		t.Fatalf("End-B runVaultExport 失败: %v", err)
	}
	exportOut := bufE.String()
	if !strings.Contains(exportOut, "TOKEN=multiend-secret") {
		t.Errorf("End-B export 应含 %q，实得：%q", "TOKEN=multiend-secret", exportOut)
	}
	if !strings.Contains(exportOut, "DB=pg://x") {
		t.Errorf("End-B export 应含 %q，实得：%q", "DB=pg://x", exportOut)
	}
	// export 行恰为 app 下两条记录（按 key 升序稳定）：DB、TOKEN 各一行。
	if lines := nonEmptyLines(exportOut); !equalStrings(lines, []string{"DB=pg://x", "TOKEN=multiend-secret"}) {
		t.Errorf("End-B export 期望恰好两行 [DB=pg://x TOKEN=multiend-secret]，实得 %v", lines)
	}

	// --- 负向控制（R7.4）：换一把不同主密钥 keyC（来自不同 vault.key）→ 解密必败 ---
	// 证明 B 端取回一致并非魔法，而严格依赖「同一把主密钥」。
	tmpC := t.TempDir()
	keyC, err := vault.LoadOrCreateKey(filepath.Join(tmpC, "vault.key"))
	if err != nil {
		t.Fatalf("End-C（不同密钥）LoadOrCreateKey 失败: %v", err)
	}
	if bytes.Equal(keyA, keyC) {
		t.Fatalf("负向控制前提失效：keyC 不应与 keyA 相同（两把独立随机密钥）")
	}
	var bufC bytes.Buffer
	errC := runVaultGet(&bufC, app, "TOKEN", keyC, store, vault.Decrypt)
	if errC == nil {
		t.Errorf("负向控制：用不同主密钥 keyC 解密 A 端密文应失败，却成功了")
	}
	// 解密失败时绝不向 w 写出任何明文/密文（R2.3）——更不得泄露 A 端明文。
	if strings.Contains(bufC.String(), "multiend-secret") {
		t.Errorf("负向控制：解密失败路径泄露了 A 端明文：%q", bufC.String())
	}
}

// writeKeyFile 把主密钥字节写到目标路径（0600），模拟把 A 端的 vault.key 拷贝到 B 端机器。
func writeKeyFile(t *testing.T, path string, key []byte) error {
	t.Helper()
	return os.WriteFile(path, key, 0o600)
}
