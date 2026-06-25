package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeAndSame(t *testing.T) {
	if NormalizeVersion("v0.1.2") != "0.1.2" {
		t.Fatal("应去掉前导 v")
	}
	if !SameVersion("v0.1.2", "0.1.2") {
		t.Fatal("v0.1.2 应等于 0.1.2")
	}
	if SameVersion("0.1.2", "0.1.3") {
		t.Fatal("不同版本不应相等")
	}
}

func TestIsDevVersion(t *testing.T) {
	for _, v := range []string{"dev", "", "0.0.1-snapshot-none", "0.1.2-dirty"} {
		if !IsDevVersion(v) {
			t.Fatalf("%q 应判为 dev", v)
		}
	}
	if IsDevVersion("0.1.2") {
		t.Fatal("正式版不应判为 dev")
	}
}

func TestMatchAsset(t *testing.T) {
	names := []string{
		"bk_0.1.2_darwin_amd64.tar.gz",
		"bk_0.1.2_darwin_arm64.tar.gz",
		"bk_0.1.2_linux_amd64.tar.gz",
		"bk_0.1.2_windows_amd64.zip",
		"checksums.txt",
	}
	got, err := MatchAsset(names, "darwin", "arm64")
	if err != nil || got != "bk_0.1.2_darwin_arm64.tar.gz" {
		t.Fatalf("darwin/arm64 => %q, %v", got, err)
	}
	got, err = MatchAsset(names, "windows", "amd64")
	if err != nil || got != "bk_0.1.2_windows_amd64.zip" {
		t.Fatalf("windows/amd64 => %q, %v", got, err)
	}
	if _, err := MatchAsset(names, "linux", "arm64"); err == nil {
		t.Fatal("linux/arm64 无资产应报错")
	}
}

func TestParseChecksumsAndVerify(t *testing.T) {
	data := []byte("hello")
	sum := sha256.Sum256(data)
	hexsum := hex.EncodeToString(sum[:])
	checks := []byte(hexsum + "  bk_0.1.2_linux_amd64.tar.gz\n" + "deadbeef  other.zip\n")
	m := ParseChecksums(checks)
	if m["bk_0.1.2_linux_amd64.tar.gz"] != hexsum {
		t.Fatalf("解析校验和失败: %v", m)
	}
	if err := VerifySHA256(data, hexsum); err != nil {
		t.Fatalf("校验应通过: %v", err)
	}
	if err := VerifySHA256(data, "deadbeef"); err == nil {
		t.Fatal("错误校验和应失败")
	}
}

func TestExtractBinary_TarGz(t *testing.T) {
	// 构造一个含 bk 二进制的 tar.gz。
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	content := []byte("#!fake-bk-binary")
	for _, f := range []struct {
		name string
		body []byte
	}{
		{"README.md", []byte("readme")},
		{"bk", content},
	} {
		_ = tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body))})
		_, _ = tw.Write(f.body)
	}
	tw.Close()
	gw.Close()

	got, err := ExtractBinary(buf.Bytes(), "tar.gz", "bk")
	if err != nil {
		t.Fatalf("解包失败: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("解出的二进制内容不符: %q", got)
	}

	if _, err := ExtractBinary(buf.Bytes(), "tar.gz", "nonexist"); err == nil {
		t.Fatal("找不到的二进制应报错")
	}
}

func TestReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bk")
	if err := os.WriteFile(path, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("准备旧文件: %v", err)
	}
	newBin := []byte("new-binary-content")
	if err := ReplaceExecutable(path, newBin); err != nil {
		t.Fatalf("替换失败: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if !bytes.Equal(got, newBin) {
		t.Fatalf("内容未替换: %q", got)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatal("替换后应保持可执行权限")
	}
}
