package sshkeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// KeyPair 持有一次密钥生成的结果。
//
// 安全不变量：PrivatePEM 仅用于落盘（见 WritePrivateKey），不得日志化、不得通过网络传输、
// 不得返回到落盘以外的用途（Requirement 10.1, 10.2）。PublicAuthLine 与 FingerprintSHA
// 仅由公钥派生，可自由展示与登记。
type KeyPair struct {
	// PrivatePEM 是 OpenSSH 格式（PEM）的私钥，仅供 WritePrivateKey 落盘。
	PrivatePEM []byte
	// PublicAuthLine 是 authorized_keys 行："ssh-ed25519 AAAA... <comment>"，可直接作为 dokku 公钥。
	PublicAuthLine string
	// FingerprintSHA 是公钥的 SHA256 指纹："SHA256:..."，供登记与展示。
	FingerprintSHA string
}

// GenerateKeyPair 在本机生成一对 ed25519 密钥，comment 一般为归属邮箱+host（Requirement 1.1, 1.3）。
//
// 产出 OpenSSH 私钥（PEM）、公钥 authorized line 与 SHA256 指纹。私钥仅置于返回值的
// PrivatePEM 字段，绝不出现在 PublicAuthLine / FingerprintSHA 中（Requirement 10.1）。
func GenerateKeyPair(comment string) (KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("生成 ed25519 密钥失败：%w", err)
	}

	pemBlock, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return KeyPair{}, fmt.Errorf("序列化私钥失败：%w", err)
	}
	privatePEM := pem.EncodeToMemory(pemBlock)

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return KeyPair{}, fmt.Errorf("构造公钥失败：%w", err)
	}

	// MarshalAuthorizedKey 返回 "ssh-ed25519 AAAA...\n"（自带换行、不含 comment）；
	// 去掉末尾换行后追加 comment，得到单行 authorized line。
	authLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	if comment != "" {
		authLine += " " + comment
	}

	return KeyPair{
		PrivatePEM:     privatePEM,
		PublicAuthLine: authLine,
		FingerprintSHA: ssh.FingerprintSHA256(sshPub),
	}, nil
}

// WritePrivateKey 以 0600 权限把私钥 privatePEM 写入 path，父目录不存在时以 0700 自动创建
//（Requirement 1.2, 10.1）。
//
// 当 overwrite=false 且目标文件已存在时，返回包裹 ErrKeyExists 的错误（errors.Is 可识别），
// 不静默覆盖正在使用的私钥（Requirement 1.4）。
func WritePrivateKey(path string, privatePEM []byte, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%w：%s", ErrKeyExists, path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("检查私钥文件失败：%w", err)
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建私钥目录失败：%w", err)
	}

	if err := os.WriteFile(path, privatePEM, 0o600); err != nil {
		return fmt.Errorf("写入私钥文件失败：%w", err)
	}

	// WriteFile 受 umask 影响，显式 Chmod 确保最终权限严格为 0600（即便文件先前已存在）。
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("设置私钥文件权限失败：%w", err)
	}

	return nil
}
