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
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"pkg.blksails.net/bk/internal/selfupdate"
)

// update.go 实现 `bk update`（别名 upgrade）：优先从公开 OSS 镜像拉取最新版，校验
// sha256 后原子替换当前可执行文件。镜像不可用时回退私有 GitHub Releases，token
// 解析顺序：--token > GH_TOKEN > GITHUB_TOKEN 环境变量 > `gh auth token`。
//
// 纯逻辑（版本比较 / 选资产 / 校验 / 解包 / 替换）在 internal/selfupdate，便于测试；
// 本文件只承载网络交互与命令编排。

const (
	updateRepoOwner        = "blksails"
	updateRepoName         = "cli"
	defaultUpdateMirrorURL = "https://blksails-pi-desktop.oss-cn-hangzhou.aliyuncs.com/bk"
	defaultGitHubAPIURL    = "https://api.github.com"
)

var (
	updateCheckOnly bool
	updateAssumeYes bool
	updateToken     string
	updateVersion   string
	updateForce     bool
	updateMirror    string
)

type releaseSource string

const (
	releaseSourceMirror releaseSource = "OSS"
	releaseSourceGitHub releaseSource = "GitHub"
)

// releaseInfo 是 OSS 清单和 GitHub Release API 共用的精简映射。
type releaseInfo struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"assets"`
	Source releaseSource `json:"-"`
}

var updateCmd = &cobra.Command{
	Use:     "update",
	Aliases: []string{"upgrade"},
	Short:   "自升级到最新版本（OSS 镜像优先，GitHub Releases 回退）",
	Long: `从公开 OSS 镜像检查最新 bk 版本，校验 sha256 后原子替换当前可执行文件。

默认镜像无需凭据；镜像不可用时回退私有 GitHub Releases，此时 token 解析顺序：
  --token 标志 > GH_TOKEN > GITHUB_TOKEN 环境变量 > 已登录的 gh CLI（gh auth token）

示例：
  bk update                 # 升级到最新版（交互确认）
  bk update --check         # 仅检查是否有新版本，不安装
  bk update -y              # 升级且跳过确认
  bk update --version v0.1.1 # 安装指定版本
  bk update --mirror off    # 禁用 OSS 镜像，仅使用 GitHub
  bk update --token <tok>   # 显式提供 GitHub 回退 token`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUpdate(cmd)
	},
}

func runUpdate(cmd *cobra.Command) error {
	w := cmd.OutOrStdout()
	goos, goarch := selfupdate.CurrentPlatform()

	mirrorURL, err := normalizeMirrorURL(updateMirror)
	if err != nil {
		return err
	}
	token := resolveGitHubToken()

	// 1) 取目标 release（OSS 优先；指定 --version 则取该 tag，否则取 latest）。
	rel, err := fetchReleaseFromSources(githubClient(), mirrorURL, defaultGitHubAPIURL, token, updateVersion)
	if err != nil {
		return err
	}

	latest := rel.TagName
	fmt.Fprintf(w, "当前版本：%s\n最新版本：%s（来源：%s）\n", versionString(), latest, rel.Source)

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
	archive, err := downloadReleaseAsset(githubClient(), rel.Source, token, assetURL)
	if err != nil {
		return fmt.Errorf("下载资产失败：%w", err)
	}

	// 6) 校验 sha256（checksums.txt 存在时；不存在则告警跳过）。
	if checksumsURL != "" {
		sums, err := downloadReleaseAsset(githubClient(), rel.Source, token, checksumsURL)
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

// normalizeMirrorURL 校验并规范化镜像基址。off 或空值表示禁用镜像。
func normalizeMirrorURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "off") {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("无效的 OSS 镜像地址 %q：仅支持 http/https URL 或 off", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// fetchReleaseFromSources 优先读取公开镜像；失败时回退私有 GitHub Releases。
func fetchReleaseFromSources(client *http.Client, mirrorBase, githubAPIBase, token, tag string) (*releaseInfo, error) {
	var mirrorErr error
	if mirrorBase != "" {
		mirrorURL := strings.TrimRight(mirrorBase, "/") + "/releases/latest.json"
		if tag != "" {
			mirrorURL = strings.TrimRight(mirrorBase, "/") + "/releases/" + url.PathEscape(tag) + "/release.json"
		}
		if rel, err := fetchReleaseJSON(client, mirrorURL, "", releaseSourceMirror); err == nil {
			return rel, nil
		} else {
			mirrorErr = err
		}
	}

	if token == "" {
		if mirrorErr != nil {
			return nil, fmt.Errorf("OSS 镜像不可用（%v），且未找到 GitHub token：请重试、设置 GH_TOKEN/GITHUB_TOKEN，或用 --token 指定", mirrorErr)
		}
		return nil, fmt.Errorf("OSS 镜像已禁用，且未找到 GitHub token：请设置 GH_TOKEN/GITHUB_TOKEN、用 --token 指定，或先 `gh auth login`")
	}

	githubURL := fmt.Sprintf("%s/repos/%s/%s/releases/latest", strings.TrimRight(githubAPIBase, "/"), updateRepoOwner, updateRepoName)
	if tag != "" {
		githubURL = fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", strings.TrimRight(githubAPIBase, "/"), updateRepoOwner, updateRepoName, url.PathEscape(tag))
	}
	rel, err := fetchReleaseJSON(client, githubURL, token, releaseSourceGitHub)
	if err != nil {
		if mirrorErr != nil {
			return nil, fmt.Errorf("OSS 镜像不可用（%v），GitHub 回退也失败：%w", mirrorErr, err)
		}
		return nil, err
	}
	return rel, nil
}

func fetchReleaseJSON(client *http.Client, releaseURL, token string, source releaseSource) (*releaseInfo, error) {
	req, _ := http.NewRequest(http.MethodGet, releaseURL, nil)
	if source == releaseSourceGitHub {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 %s release 失败：%w", source, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s release 返回 404", source)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%s release 返回 %d：%s", source, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rel releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析 %s release 响应失败：%w", source, err)
	}
	if strings.TrimSpace(rel.TagName) == "" {
		return nil, fmt.Errorf("%s release 缺少 tag_name", source)
	}
	rel.Source = source
	return &rel, nil
}

// downloadReleaseAsset 下载发布资产。只有 GitHub 来源会携带 token；镜像始终匿名。
func downloadReleaseAsset(client *http.Client, source releaseSource, token, apiURL string) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodGet, apiURL, nil)
	if source == releaseSourceGitHub {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/octet-stream")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}

	resp, err := client.Do(req)
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
	updateCmd.Flags().StringVar(&updateMirror, "mirror", defaultUpdateMirrorURL, "OSS 镜像基址；设为 off 禁用")
	rootCmd.AddCommand(updateCmd)
}
