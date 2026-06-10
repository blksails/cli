package cmd

import (
	"bytes"
	"encoding/base64"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"

	"pkg.blksails.net/bk/internal/auth"
	"pkg.blksails.net/bk/internal/vault"
)

// vault_security_test.go 是 Secret Vault 的「安全与篡改路径」集成验证（Task 5.2）：
// 仅依赖既有生产缝（runVaultGet / runVaultExport / runVaultSet / runVaultList /
// newVaultStore）与真实加解密（internal/vault.Encrypt/Decrypt + 真实 32 字节主密钥），
// 在不触碰任何生产代码或其它 _test.go 的前提下，证明三类安全不变量成立：
//
//  1. 篡改（R2.3/R5.3）：库中密文被篡改后，get 解密失败、非零退出且不输出任何明文/密文；
//     export 单条失败即整体失败、不输出其余已解密明文（绝不部分泄露）。
//     —— 真实 AES-GCM：篡改的是真实密文的 GCM 认证标签，Decrypt 因认证失败而拒绝，
//        不是伪造的解密错误。
//  2. 密文恒为密文 / 输出不含明文（R6.3/R7.2）：真实 set 后 store 侧 value 字段恒为密文
//     （≠明文），set 确认与 list 输出均不含明文 VALUE（get 按设计输出明文，故排除在外）。
//  3. 未登录（R6.4/R7.2）：profile 无有效会话时，newVaultStore 透传引导用户运行
//     `bk auth login` 的错误并非零退出（错误链含 auth.ErrReloginRequired）。
//
// 命名：与 vault_integration_test.go 同属 package cmd，故刻意避开其已声明的
// e2eVaultStore / newE2EVaultStore / nonEmptyLines / equalStrings，本文件使用 sec 前缀。

// secVaultStore 是一个 backing 单一共享 map 的内存存储，单结构即满足
// vaultSetter + vaultGetter + vaultLister + vaultListerFull 四个注入接口
// （set / get / list / export 各自所需的缝），使密文状态可跨步骤连续流转。
// 镜像真实 *vault.Store：仅搬运密文（app→key→CIPHERTEXT），绝不持有主密钥、绝不加解密。
type secVaultStore struct {
	// data[app][key] = ciphertext（base64(nonce||ciphertext||tag)），刻意只存密文。
	data map[string]map[string]string
}

func newSecVaultStore() *secVaultStore {
	return &secVaultStore{data: map[string]map[string]string{}}
}

// Set 满足 vaultSetter：以 (app,key) upsert 密文。
func (s *secVaultStore) Set(app, key, ciphertext string) error {
	if s.data[app] == nil {
		s.data[app] = map[string]string{}
	}
	s.data[app][key] = ciphertext
	return nil
}

// Get 满足 vaultGetter：按 (app,key) 取回密文，命中 0 行返回 vault.ErrNotFound。只搬运密文。
func (s *secVaultStore) Get(app, key string) (string, error) {
	if m, ok := s.data[app]; ok {
		if ct, ok := m[key]; ok {
			return ct, nil
		}
	}
	return "", vault.ErrNotFound
}

// ListKeys 满足 vaultLister：仅返回 app 下 key 名（稳定升序），绝不返回 value。
func (s *secVaultStore) ListKeys(app string) ([]string, error) {
	m := s.data[app]
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// List 满足 vaultListerFull（export 所需）：返回 app 下全部完整记录（含密文 value），
// 按 key 升序排序——镜像 *vault.Store.List 的稳定顺序。
func (s *secVaultStore) List(app string) ([]vault.Secret, error) {
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

// secNewMasterKey 经 LoadOrCreateKey 在临时目录生成并持久化一个真实 32 字节 AES-256 主密钥，
// 使 store 持有的是真实 AES-GCM 密文、解密是真实往返（篡改必被 GCM 认证拒绝，非伪造错误）。
func secNewMasterKey(t *testing.T) []byte {
	t.Helper()
	key, err := vault.LoadOrCreateKey(filepath.Join(t.TempDir(), "vault.key"))
	if err != nil {
		t.Fatalf("LoadOrCreateKey 失败: %v", err)
	}
	return key
}

// secTamper 把一个真实密文（base64(nonce||ciphertext||tag)）篡改成「仍是合法 base64、
// 但 GCM 认证标签被破坏」的串：解码 → 翻转最后一个字节（位于 GCM tag 区）→ 重新 base64 编码。
// 这样 Decrypt 必因认证失败（gcm.Open）而报错，而不是 base64 解码失败——精确命中 R2.3/R5.3
// 「密文被篡改」语义。
func secTamper(t *testing.T, ciphertext string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		t.Fatalf("解码原密文失败（测试前置条件不成立）: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("原密文解码后为空，无法篡改")
	}
	// 翻转最后一个字节（GCM 认证标签的一部分），破坏认证而保持长度不变。
	raw[len(raw)-1] ^= 0xFF
	tampered := base64.StdEncoding.EncodeToString(raw)
	if tampered == ciphertext {
		t.Fatal("篡改后密文与原密文相同，篡改未生效")
	}
	// 防御性自检：篡改后仍是合法 base64（否则失败会发生在解码而非认证阶段）。
	if _, derr := base64.StdEncoding.DecodeString(tampered); derr != nil {
		t.Fatalf("篡改后不是合法 base64: %v", derr)
	}
	return tampered
}

// TestVaultSecurity_TamperedCiphertext_GetExportFailWithoutPlaintextLeak 验证场景 1：
// 篡改库中密文后，get 与 export（含单条失败的部分失败）均解密失败、整体非零退出，
// 且绝不向输出写出任何明文/密文（R2.3/R5.3 核心安全断言）。
func TestVaultSecurity_TamperedCiphertext_GetExportFailWithoutPlaintextLeak(t *testing.T) {
	key := secNewMasterKey(t)
	const app = "secapp"

	// --- get：单条密文被篡改 → 解密失败、非零退出、输出 0 字节、无明文 "secretA"。 ---
	goodA, err := vault.Encrypt(key, "secretA")
	if err != nil {
		t.Fatalf("Encrypt secretA 失败: %v", err)
	}
	tamperedA := secTamper(t, goodA)

	storeGet := newSecVaultStore()
	if err := storeGet.Set(app, "A", tamperedA); err != nil {
		t.Fatalf("Set 篡改密文失败: %v", err)
	}

	var getBuf bytes.Buffer
	getErr := runVaultGet(&getBuf, app, "A", key, storeGet, vault.Decrypt)
	if getErr == nil {
		t.Fatal("get 篡改密文期望返回非 nil 错误（解密失败、非零退出），实得 nil")
	}
	// 关键安全断言：失败路径绝不向 w 写出任何字节（既无明文也无密文）。
	if getBuf.Len() != 0 {
		t.Errorf("get 解密失败后输出应为 0 字节，实得 %d 字节：%q", getBuf.Len(), getBuf.String())
	}
	if strings.Contains(getBuf.String(), "secretA") {
		t.Errorf("get 解密失败后输出泄露了明文 secretA：%q", getBuf.String())
	}
	if strings.Contains(getBuf.String(), tamperedA) || strings.Contains(getBuf.String(), goodA) {
		t.Errorf("get 解密失败后输出泄露了密文：%q", getBuf.String())
	}

	// --- export：部分失败（R5.3）——一条有效密文 goodB + 一条被篡改密文 → 整体失败、 ---
	// --- 输出 0 字节，已成功解密的 "goodB" 绝不出现（关键：无部分明文泄露）。 ---
	goodB, err := vault.Encrypt(key, "goodB")
	if err != nil {
		t.Fatalf("Encrypt goodB 失败: %v", err)
	}
	goodC, err := vault.Encrypt(key, "goodC")
	if err != nil {
		t.Fatalf("Encrypt goodC 失败: %v", err)
	}
	tamperedC := secTamper(t, goodC)

	storeExport := newSecVaultStore()
	// B 有效、C 被篡改。List 按 key 升序返回（B 先于 C），故若实现是「增量写出」，
	// B 的明文会在 C 失败前被写出——本断言正是要证明实现不会这样泄露。
	if err := storeExport.Set(app, "B", goodB); err != nil {
		t.Fatalf("Set goodB 失败: %v", err)
	}
	if err := storeExport.Set(app, "C", tamperedC); err != nil {
		t.Fatalf("Set tamperedC 失败: %v", err)
	}

	var exportBuf bytes.Buffer
	exportErr := runVaultExport(&exportBuf, app, key, storeExport, vault.Decrypt)
	if exportErr == nil {
		t.Fatal("export 含被篡改密文期望返回非 nil 错误（整体非零退出），实得 nil")
	}
	// 关键安全断言（R5.3）：单条失败即整体失败，绝不输出已部分解密的明文——w 必须为 0 字节。
	if exportBuf.Len() != 0 {
		t.Errorf("export 单条解密失败后输出应为 0 字节（不部分泄露），实得 %d 字节：%q", exportBuf.Len(), exportBuf.String())
	}
	if strings.Contains(exportBuf.String(), "goodB") {
		t.Errorf("export 部分失败泄露了已解密明文 goodB：%q", exportBuf.String())
	}
}

// TestVaultSecurity_CiphertextOnly_NoPlaintextInStoreOrOutput 验证场景 2：
// 真实 set 后 store 侧 value 恒为密文（≠明文），且 set 确认与 list 输出均不含明文 VALUE。
// get 按设计输出明文（脚本消费），故被排除在「输出不含明文」检查之外（其职责即输出明文）。
func TestVaultSecurity_CiphertextOnly_NoPlaintextInStoreOrOutput(t *testing.T) {
	key := secNewMasterKey(t)
	const app = "secapp"
	const plaintext = "supersecret-XYZ" // 可识别的明文标记，便于断言其绝不出现在存储/输出。

	store := newSecVaultStore()

	// 真实 set：经 vault.Encrypt 加密后写入 store。
	var setBuf bytes.Buffer
	if err := runVaultSet(&setBuf, app, []string{"TOKEN=" + plaintext}, key, store, vault.Encrypt); err != nil {
		t.Fatalf("runVaultSet 失败: %v", err)
	}

	// R6.3：store 侧 value 字段恒为密文（≠明文、非空）。
	stored := store.data[app]["TOKEN"]
	if stored == "" {
		t.Fatal("set 后 store 中 TOKEN 的值为空")
	}
	if stored == plaintext {
		t.Errorf("R6.3 违反：store 中存的是明文而非密文：%q", stored)
	}
	// 进一步证明它是真实密文：可被同一主密钥解密回原明文。
	if got, err := vault.Decrypt(key, stored); err != nil || got != plaintext {
		t.Errorf("store 中的值应为可解密回 %q 的真实密文，实得明文=%q err=%v", plaintext, got, err)
	}

	// R6.2/R7.2：set 确认输出不得回显明文 VALUE。
	if strings.Contains(setBuf.String(), plaintext) {
		t.Errorf("set 确认输出泄露了明文 VALUE %q：%q", plaintext, setBuf.String())
	}

	// R3.2/R7.2：list 输出仅 key 名，绝不含明文（也不含密文 value）。
	var listBuf bytes.Buffer
	if err := runVaultList(&listBuf, app, store); err != nil {
		t.Fatalf("runVaultList 失败: %v", err)
	}
	if strings.Contains(listBuf.String(), plaintext) {
		t.Errorf("list 输出泄露了明文 VALUE %q：%q", plaintext, listBuf.String())
	}
	if strings.Contains(listBuf.String(), stored) {
		t.Errorf("list 输出泄露了密文 value：%q", listBuf.String())
	}
	// list 仍应正常列出 key 名 TOKEN（证明它确实有内容、并非空输出导致断言空过）。
	if !strings.Contains(listBuf.String(), "TOKEN") {
		t.Errorf("list 应列出 key 名 TOKEN，实得：%q", listBuf.String())
	}
}

// TestVaultSecurity_Unauthenticated_GuidesAuthLogin 验证场景 3（R6.4/R7.2）：
// 当目标 profile 无有效会话时，newVaultStore 透传 AuthedClient 引导用户运行
// `bk auth login` 的错误并非零退出（错误链含 auth.ErrReloginRequired）。
//
// 测试方式（真实路径、确定性、零网络）：
//   - 把全局 authConfig 指向一个临时 auth.json，其中只含「别的 profile」，目标 profile 缺失，
//     使 EnsureFresh 在「找不到 profile」分支返回 ErrReloginRequired（在调用 refresher 之前，
//     不触网）。
//   - 把 viper 的 api_endpoint/api_key 置为非空 dummy 值，使 newSchemaClient 的
//     supabase.NewClient（纯内存装配，不发请求）成功，从而真正走到 authedClientWith 的
//     认证判定（否则会先因 "url and key are required" 在装配阶段失败，错过被测路径）。
//   - 所有被改动的全局状态（authConfig、两个 viper key）均 defer 还原。
func TestVaultSecurity_Unauthenticated_GuidesAuthLogin(t *testing.T) {
	const targetProfile = "sec-missing"

	// 1) 临时 auth.json：只 seed 一个无关 profile，目标 profile 缺失。
	authPath := filepath.Join(t.TempDir(), "auth.json")
	now := time.Now()
	if err := auth.AddAuthConfig(authPath, &auth.AuthConfig{
		Profile: "someone-else",
		Session: auth.Session{
			AccessToken:  "x",
			RefreshToken: "y",
			TokenType:    "bearer",
			ExpiresIn:    3600,
			ExpiresAt:    now.Add(time.Hour).Unix(),
			User:         auth.User{ID: "00000000-0000-0000-0000-000000000001", Email: "e@x.com"},
		},
	}); err != nil {
		t.Fatalf("seed auth.json 失败: %v", err)
	}

	// 2) 还原全局 authConfig（AuthedClient 经此包级变量定位 auth.json）。
	origAuthConfig := authConfig
	authConfig = authPath
	defer func() { authConfig = origAuthConfig }()

	// 3) 给 viper 注入非空 dummy 端点/密钥，使 newSchemaClient 装配成功（纯内存、不触网），
	//    从而真正抵达 authedClientWith 的认证判定。结束后还原。
	origEndpoint := viper.Get("api_endpoint")
	origKey := viper.Get("api_key")
	viper.Set("api_endpoint", "http://localhost:0")
	viper.Set("api_key", "dummy-key")
	defer func() {
		viper.Set("api_endpoint", origEndpoint)
		viper.Set("api_key", origKey)
	}()

	// 4) 调用真实 newVaultStore：目标 profile 无会话 → 应返回非 nil 错误、store 为 nil。
	store, err := newVaultStore(targetProfile)
	if err == nil {
		t.Fatal("未登录 profile 期望 newVaultStore 返回非 nil 错误，实得 nil")
	}
	if store != nil {
		t.Errorf("未登录 profile 期望 store 为 nil，实得非 nil")
	}
	// 错误链应可识别为 ErrReloginRequired（sentinel），且面向用户的文案引导 `bk auth login`。
	if !errors.Is(err, auth.ErrReloginRequired) {
		t.Errorf("期望错误链含 auth.ErrReloginRequired，实得：%v", err)
	}
	if !strings.Contains(err.Error(), "bk auth login") {
		t.Errorf("期望错误信息引导用户运行 `bk auth login`，实得：%v", err)
	}
}
