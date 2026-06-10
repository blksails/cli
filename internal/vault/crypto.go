// Package vault 提供 secret 的本地加密与远端（Supabase）存储。
//
// 安全模型：密文存放在 Supabase 的 blacksail.secrets 表，便于多端共享与审计；
// 而对称主密钥仅保存在本机 ~/.local/bk/vault.key（0600）。因此即便数据库泄露，
// 没有本机主密钥也无法解密。加密算法为 AES-256-GCM（认证加密）。
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// keySize 为 AES-256 的密钥长度。
const keySize = 32

// LoadOrCreateKey 读取本机主密钥，不存在则生成一个新的随机密钥并持久化。
func LoadOrCreateKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		decoded, derr := base64.StdEncoding.DecodeString(string(data))
		if derr != nil {
			return nil, fmt.Errorf("vault: 主密钥文件格式无效: %w", derr)
		}
		if len(decoded) != keySize {
			return nil, fmt.Errorf("vault: 主密钥长度异常，期望 %d 字节", keySize)
		}
		return decoded, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	// 生成新密钥
	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("vault: 生成主密钥失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return nil, fmt.Errorf("vault: 持久化主密钥失败: %w", err)
	}
	return key, nil
}

// Encrypt 使用 AES-256-GCM 加密明文，返回 base64(nonce || ciphertext)。
func Encrypt(key []byte, plaintext string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 解密 Encrypt 产生的 base64 串。
func Decrypt(key []byte, encoded string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("vault: 密文格式无效: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("vault: 密文长度异常")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("vault: 解密失败（主密钥不匹配或数据被篡改）: %w", err)
	}
	return string(plaintext), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("vault: 主密钥长度必须为 %d 字节", keySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
