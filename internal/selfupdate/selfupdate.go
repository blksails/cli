// Package selfupdate 实现 bk 自升级的可测核心：版本归一化/比较、按平台选择
// release 资产、校验 sha256、从 tar.gz/zip 解出二进制、原子替换当前可执行文件。
//
// 这里只放纯逻辑与本地文件操作（无网络、无 cmd 依赖），便于单元测试；GitHub API
// 交互与命令编排在 cmd/update.go。
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// NormalizeVersion 去掉前导 'v' 与首尾空白，使 "v0.1.2" 与 "0.1.2" 可比。
func NormalizeVersion(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "v")
}

// SameVersion 报告两个版本（忽略前导 v / 空白）是否相同。
func SameVersion(a, b string) bool {
	return NormalizeVersion(a) == NormalizeVersion(b)
}

// IsDevVersion 报告 v 是否为非 release 构建（dev / snapshot / 空）——这类应总是允许升级。
func IsDevVersion(v string) bool {
	n := NormalizeVersion(v)
	return n == "" || n == "dev" || strings.Contains(n, "snapshot") || strings.Contains(n, "dirty")
}

// AssetExt 返回当前/指定平台的归档扩展名：windows 为 zip，其余 tar.gz。
func AssetExt(goos string) string {
	if goos == "windows" {
		return "zip"
	}
	return "tar.gz"
}

// BinaryName 返回归档内二进制名：windows 为 bk.exe，其余 bk。
func BinaryName(goos string) string {
	if goos == "windows" {
		return "bk.exe"
	}
	return "bk"
}

// MatchAsset 从一组资产名中选出匹配 goos/goarch 的归档名。
// 匹配后缀 `_<goos>_<goarch>.<ext>`（与 .goreleaser.yaml 的 name_template 对应），
// 不依赖版本号字符串，故跨版本稳健。无匹配返回错误。
func MatchAsset(names []string, goos, goarch string) (string, error) {
	suffix := fmt.Sprintf("_%s_%s.%s", goos, goarch, AssetExt(goos))
	for _, n := range names {
		if strings.HasSuffix(n, suffix) {
			return n, nil
		}
	}
	return "", fmt.Errorf("未找到匹配当前平台（%s/%s）的发布资产（期望后缀 %q）", goos, goarch, suffix)
}

// ParseChecksums 解析 goreleaser 的 checksums.txt（每行 "<sha256>  <filename>"）。
// 返回 filename→sha256(小写十六进制)。
func ParseChecksums(data []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 {
			out[fields[len(fields)-1]] = strings.ToLower(fields[0])
		}
	}
	return out
}

// VerifySHA256 校验 data 的 sha256 是否等于 wantHex（不区分大小写）。
func VerifySHA256(data []byte, wantHex string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, strings.TrimSpace(wantHex)) {
		return fmt.Errorf("校验和不匹配：期望 %s，实得 %s", wantHex, got)
	}
	return nil
}

// ExtractBinary 从归档（tar.gz 或 zip）中取出名为 binName 的文件内容。
func ExtractBinary(archive []byte, ext, binName string) ([]byte, error) {
	if ext == "zip" {
		return extractFromZip(archive, binName)
	}
	return extractFromTarGz(archive, binName)
}

func extractFromTarGz(archive []byte, binName string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("解压 gzip 失败: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取 tar 失败: %w", err)
		}
		if filepath.Base(hdr.Name) == binName {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("归档中未找到二进制 %q", binName)
}

func extractFromZip(archive []byte, binName string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("打开 zip 失败: %w", err)
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) == binName {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("打开 zip 内文件失败: %w", err)
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("归档中未找到二进制 %q", binName)
}

// ReplaceExecutable 用 newBin 原子替换 path 指向的可执行文件。
//
// 做法：在同目录写临时文件（保证 rename 原子、不跨卷）、chmod 0755，再 rename 覆盖。
// Windows 无法覆盖正在运行的 exe：先把旧文件改名为 <path>.old 再写入新文件。
// path 应为已 EvalSymlinks 解析后的真实路径（见 cmd 层 ResolveExecutable）。
func ReplaceExecutable(path string, newBin []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".bk-update-*")
	if err != nil {
		return fmt.Errorf("在 %s 创建临时文件失败（可能无写权限，尝试 sudo 或重新安装）: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 失败时清理；成功时已被 rename 走

	if _, err := tmp.Write(newBin); err != nil {
		tmp.Close()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("设置可执行权限失败: %w", err)
	}

	if runtime.GOOS == "windows" {
		// 正在运行的 exe 不能直接覆盖：先把旧的挪开（保留为 .old，下次启动前可清理）。
		_ = os.Remove(path + ".old")
		if err := os.Rename(path, path+".old"); err != nil {
			return fmt.Errorf("移动旧可执行文件失败: %w", err)
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换可执行文件失败（可能无写权限，尝试 sudo 或重新安装）: %w", err)
	}
	return nil
}

// ResolveExecutable 返回当前进程可执行文件的真实路径（解析 symlink）。
func ResolveExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位当前可执行文件失败: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

// CurrentPlatform 返回当前运行平台（便于 cmd 层与测试统一取用）。
func CurrentPlatform() (goos, goarch string) {
	return runtime.GOOS, runtime.GOARCH
}
