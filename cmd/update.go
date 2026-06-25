/*
Copyright © 2025 BlackSails
*/
package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"pkg.blksails.net/bk/internal/selfupdate"
)

// update.go 实现 `bk update`（别名 upgrade）：从 GitHub Releases 拉取最新版，校验 sha256
// 后原子替换当前可执行文件。仓库 blksails/cli 为私有，下载资产需 GitHub token——
// 解析顺序：--token > GH_TOKEN > GITHUB_TOKEN 环境变量 > `gh auth token`。
//
// 纯逻辑（版本比较 / 选资产 / 校验 / 解包 / 替换）在 internal/selfupdate，便于测试；
// 本文件只承载网络交互与命令编排。

const (
	updateRepoOwner = "blksails"
	updateRepoName  = "cli"
)

var (
	updateCheckOnly bool
	updateAssumeYes bool
	updateToken     string
	updateVersion   string
	updateForce     bool
)

// ghRelease 是 GitHub Release API 的精简映射。
type ghRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"url"` // API 资产地址（私有仓库经它 + Accept octet-stream 下载）
	} `json:"assets"`
}

var updateCmd = &cobra.Command{
	Use:     "update",
	Aliases: []string{"upgrade"},
	Short:   "自升级到最新版本（从 GitHub Releases 下载并替换当前二进制）",
	Long: `检查 GitHub Releases 上的最新 bk 版本，校验 sha256 后原子替换当前可执行文件。

仓库为私有，下载需 GitHub token，解析顺序：
  --token 标志 > GH_TOKEN > GITHUB_TOKEN 环境变量 > 已登录的 gh CLI（gh auth token）

示例：
  bk update                 # 升级到最新版（交互确认）
  bk update --check         # 仅检查是否有新版本，不安装
  bk update -y              # 升级且跳过确认
  bk update --version v0.1.1 # 安装指定版本
  bk update --token <tok>   # 显式提供 GitHub token`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUpdate(cmd)
	},
}

func runUpdate(cmd *cobra.Command) error {
	w := cmd.OutOrStdout()
	goos, goarch := selfupdate.CurrentPlatform()

	token := resolveGitHubToken()
	if token == "" {
		return fmt.Errorf("未找到 GitHub token：请设置 GH_TOKEN/GITHUB_TOKEN 环境变量、用 --token 指定，或先 `gh auth login`")
	}

	// 1) 取目标 release（指定 --version 则取该 tag，否则取 latest）。
	rel, err := fetchRelease(token, updateVersion)
	if err != nil {
		return err
	}

	latest := rel.TagName
	fmt.Fprintf(w, "当前版本：%s\n最新版本：%s\n", versionString(), latest)

	// 2) 版本比较：相同且非 dev、非 --force/--version → 已是最新。
	if !updateForce && updateVersion == "" &&
		!selfupdate.IsDevVersion(versionString()) &&
		selfupdate.SameVersion(versionString(), latest) {
		fmt.Fprintln(w, "已是最新版本，无需升级。")
		return nil
	}

	if updateCheckOnly {
		if selfupdate.SameVersion(versionString(), latest) {
			fmt.Fprintln(w, "（--check）已是最新版本。")
		} else {
			fmt.Fprintf(w, "（--check）有可用更新：%s → %s，运行 `bk update` 进行升级。\n", versionString(), latest)
		}
		return nil
	}

	// 3) 选当前平台的资产 + checksums。
	names := make([]string, 0, len(rel.Assets))
	for _, a := range rel.Assets {
		names = append(names, a.Name)
	}
	assetName, err := selfupdate.MatchAsset(names, goos, goarch)
	if err != nil {
		return err
	}
	assetURL, checksumsURL := "", ""
	for _, a := range rel.Assets {
		switch a.Name {
		case assetName:
			assetURL = a.URL
		case "checksums.txt":
			checksumsURL = a.URL
		}
	}
	if assetURL == "" {
		return fmt.Errorf("release %s 缺少资产 %s", latest, assetName)
	}

	// 4) 确认（除非 -y）。
	if !updateAssumeYes {
		fmt.Fprintf(w, "将把 bk 从 %s 升级到 %s（%s）。继续？[y/N] ", versionString(), latest, assetName)
		if !confirmYes(cmd) {
			fmt.Fprintln(w, "已取消。")
			return nil
		}
	}

	// 5) 下载资产。
	fmt.Fprintf(w, "下载 %s ...\n", assetName)
	archive, err := downloadAsset(token, assetURL)
	if err != nil {
		return fmt.Errorf("下载资产失败：%w", err)
	}

	// 6) 校验 sha256（checksums.txt 存在时；不存在则告警跳过）。
	if checksumsURL != "" {
		sums, err := downloadAsset(token, checksumsURL)
		if err != nil {
			return fmt.Errorf("下载 checksums.txt 失败：%w", err)
		}
		want := selfupdate.ParseChecksums(sums)[assetName]
		if want == "" {
			return fmt.Errorf("checksums.txt 中缺少 %s 的校验和", assetName)
		}
		if err := selfupdate.VerifySHA256(archive, want); err != nil {
			return fmt.Errorf("资产校验失败：%w", err)
		}
		fmt.Fprintln(w, "校验和通过。")
	} else {
		fmt.Fprintln(w, "⚠ release 无 checksums.txt，跳过校验。")
	}

	// 7) 解出二进制并替换当前可执行文件。
	bin, err := selfupdate.ExtractBinary(archive, selfupdate.AssetExt(goos), selfupdate.BinaryName(goos))
	if err != nil {
		return err
	}
	exePath, err := selfupdate.ResolveExecutable()
	if err != nil {
		return err
	}
	if err := selfupdate.ReplaceExecutable(exePath, bin); err != nil {
		return err
	}

	fmt.Fprintf(w, "✓ 已升级到 %s（%s）。运行 `bk version` 确认。\n", latest, exePath)
	return nil
}

// resolveGitHubToken 按 --token > GH_TOKEN > GITHUB_TOKEN > `gh auth token` 解析 token。
func resolveGitHubToken() string {
	if updateToken != "" {
		return updateToken
	}
	for _, env := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v
		}
	}
	// 回退到已登录的 gh CLI。
	if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
		if t := strings.TrimSpace(string(out)); t != "" {
			return t
		}
	}
	return ""
}

// githubClient 返回一个在跨主机重定向时剥离 Authorization 头的 HTTP client——
// GitHub 资产下载会 302 到 S3 签名 URL，转发 Authorization 反而会被拒。
func githubClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				req.Header.Del("Authorization")
			}
			if len(via) >= 10 {
				return fmt.Errorf("重定向次数过多")
			}
			return nil
		},
	}
}

// fetchRelease 取 latest（tag 为空）或指定 tag 的 release 元数据。
func fetchRelease(token, tag string) (*ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", updateRepoOwner, updateRepoName)
	if tag != "" {
		url = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", updateRepoOwner, updateRepoName, tag)
	}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := githubClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub Release 失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		if tag != "" {
			return nil, fmt.Errorf("未找到版本 %s（404）", tag)
		}
		return nil, fmt.Errorf("未找到任何 release（404）；确认 token 有 %s/%s 读权限", updateRepoOwner, updateRepoName)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GitHub Release 返回 %d：%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析 release 响应失败：%w", err)
	}
	return &rel, nil
}

// downloadAsset 经 GitHub 资产 API（Accept octet-stream）下载私有仓库的二进制资产。
func downloadAsset(token, apiURL string) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodGet, apiURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := githubClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("下载返回 %d：%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(resp.Body)
}

// confirmYes 从 stdin 读一行，y/yes（不区分大小写）视为确认。
func confirmYes(cmd *cobra.Command) bool {
	in := cmd.InOrStdin()
	r := bufio.NewReader(in)
	line, _ := r.ReadString('\n')
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes"
}

func init() {
	updateCmd.Flags().BoolVar(&updateCheckOnly, "check", false, "仅检查是否有新版本，不安装")
	updateCmd.Flags().BoolVarP(&updateAssumeYes, "yes", "y", false, "跳过确认直接升级")
	updateCmd.Flags().StringVar(&updateToken, "token", "", "GitHub token（默认取 GH_TOKEN/GITHUB_TOKEN 或 gh auth token）")
	updateCmd.Flags().StringVar(&updateVersion, "version", "", "安装指定版本（如 v0.1.1），默认最新")
	updateCmd.Flags().BoolVar(&updateForce, "force", false, "即使已是最新也重新安装")
	rootCmd.AddCommand(updateCmd)
}
