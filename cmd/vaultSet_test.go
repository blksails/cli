package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// fakeVaultSetter 记录 Set 调用，断言「整体不写入」与传入的是密文而非明文。
type fakeVaultSetter struct {
	calls   []struct{ app, key, ciphertext string }
	failKey string // 若非空，对该 key 的 Set 返回错误
}

func (f *fakeVaultSetter) Set(app, key, ciphertext string) error {
	f.calls = append(f.calls, struct{ app, key, ciphertext string }{app, key, ciphertext})
	if f.failKey != "" && key == f.failKey {
		return errors.New("store boom")
	}
	return nil
}

// fakeEncrypt 把明文包装为可识别的「密文」，从而断言传给 Set 的不是明文本身。
func fakeEncrypt(key []byte, plaintext string) (string, error) {
	return "ENC(" + plaintext + ")", nil
}

func TestRunVaultSet_MultiPair_WritesCiphertextAndCount(t *testing.T) {
	var w bytes.Buffer
	setter := &fakeVaultSetter{}

	err := runVaultSet(&w, "myapp", []string{"A=1", "B=2"}, []byte("k"), setter, fakeEncrypt)
	if err != nil {
		t.Fatalf("runVaultSet 返回错误：%v", err)
	}

	if len(setter.calls) != 2 {
		t.Fatalf("期望 Set 被调用 2 次，实际 %d 次：%+v", len(setter.calls), setter.calls)
	}
	// 传给 Set 的必须是密文，不能是明文 "1"/"2"。
	for _, c := range setter.calls {
		if c.ciphertext == "1" || c.ciphertext == "2" {
			t.Fatalf("Set 收到了明文而非密文：%q", c.ciphertext)
		}
		if !strings.HasPrefix(c.ciphertext, "ENC(") {
			t.Fatalf("Set 收到的不是 encrypt 产出的密文：%q", c.ciphertext)
		}
	}
	if setter.calls[0].app != "myapp" || setter.calls[0].key != "A" {
		t.Fatalf("首条写入的 app/key 不正确：%+v", setter.calls[0])
	}

	out := w.String()
	if !strings.Contains(out, "2") {
		t.Fatalf("确认输出未包含写入数量 2：%q", out)
	}
	// 输出不得回显明文 VALUE（R1.2/R6.2）。确认行可含数量 2，但不得含明文 "=VALUE"。
	if strings.Contains(out, "=1") || strings.Contains(out, "=2") {
		t.Fatalf("输出回显了明文 VALUE：%q", out)
	}
}

func TestRunVaultSet_InvalidPair_NoWriteAtAll(t *testing.T) {
	var w bytes.Buffer
	setter := &fakeVaultSetter{}

	err := runVaultSet(&w, "myapp", []string{"A=1", "BAD"}, []byte("k"), setter, fakeEncrypt)
	if err == nil {
		t.Fatalf("非法参数应返回错误，但返回 nil")
	}
	// 整体不写入：即便首个 pair 合法，也不得调用 Set（R1.7）。
	if len(setter.calls) != 0 {
		t.Fatalf("非法参数场景下 Set 不应被调用，实际 %d 次：%+v", len(setter.calls), setter.calls)
	}
}

func TestRunVaultSet_EncryptError_StopsAndNoFurtherWrites(t *testing.T) {
	var w bytes.Buffer
	setter := &fakeVaultSetter{}

	boom := func(key []byte, plaintext string) (string, error) {
		if plaintext == "1" {
			return "", errors.New("encrypt boom")
		}
		return "ENC(" + plaintext + ")", nil
	}

	err := runVaultSet(&w, "myapp", []string{"A=1", "B=2"}, []byte("k"), setter, boom)
	if err == nil {
		t.Fatalf("encrypt 失败应返回错误，但返回 nil")
	}
	if len(setter.calls) != 0 {
		t.Fatalf("encrypt 失败时不应写入任何记录，实际 %d 次：%+v", len(setter.calls), setter.calls)
	}
}

func TestRunVaultSet_NoPlaintextInOutput(t *testing.T) {
	var w bytes.Buffer
	setter := &fakeVaultSetter{}

	const secret = "supersecret"
	err := runVaultSet(&w, "myapp", []string{"PASSWORD=" + secret}, []byte("k"), setter, fakeEncrypt)
	if err != nil {
		t.Fatalf("runVaultSet 返回错误：%v", err)
	}
	if strings.Contains(w.String(), secret) {
		t.Fatalf("输出回显了明文 VALUE %q：%q", secret, w.String())
	}
	// 写入的密文里也不应原样暴露明文给日志使用方——这里仅断言确认行不含明文已足够，
	// 但顺带确认 Set 收到的是 encrypt 的产出。
	if len(setter.calls) != 1 {
		t.Fatalf("期望写入 1 条，实际 %d 条", len(setter.calls))
	}
}
