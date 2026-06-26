/*
Copyright © 2025 BlackSails
*/
package cmd

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

// app_remote.go 实现 `bk app remote <app>`：为当前 git 仓库添加（或更新）Dokku 的
// git 部署 remote，免去手敲 `git remote add dokku dokku@<主机>:<app>`。
//
// 主机地址经 SSHConfig(profile) 解析（会回退到登录后缓存的在线主机目录），
// 故新用户即便没在 .bs.yaml 配 ssh.host 也能拿到坐标。git 推送用户固定为 dokku
// （Dokku 的 git 部署用户），与 ssh.user（管理员可能是 root）无关。

var (
	appRemoteName    string // --name：remote 名（默认 dokku，刻意不叫 origin 以免覆盖 GitHub）
	appRemoteGitUser string // --git-user：git 部署用户（默认 dokku）
	appRemoteHost    string // --host：覆盖主机地址（默认取 SSHConfig）
	appRemotePort    int    // --port：覆盖端口（默认取 SSHConfig）
	appRemotePrint   bool   // --print：只打印 URL，不改动仓库
	appRemoteForce   bool   // --force：remote 已存在且 URL 不同也直接覆盖
)

// deployRemoteURL 依据 git 用户/主机/端口/app 构造 Dokku git 部署 URL。
// 端口为 22（或 0）时用简洁的 scp 风格 dokku@host:app；非标准端口需 ssh:// 形式带端口。
func deployRemoteURL(gitUser, host string, port int, app string) (string, error) {
	if strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("未能确定部署主机：请用 --host 指定，或先 `bk auth login && bk init` 同步主机目录，或在 .bs.yaml 配置 ssh.host")
	}
	if strings.TrimSpace(app) == "" {
		return "", fmt.Errorf("缺少应用名")
	}
	if gitUser == "" {
		gitUser = "dokku"
	}
	if port == 0 || port == 22 {
		return fmt.Sprintf("%s@%s:%s", gitUser, host, app), nil
	}
	return fmt.Sprintf("ssh://%s@%s:%d/%s", gitUser, host, port, app), nil
}

// gitRemoter 抽象本命令所需的 git remote 操作，便于注入 fake 测试编排逻辑。
type gitRemoter interface {
	GetURL(name string) (string, error) // 不存在返回 ("", nil)
	Add(name, url string) error
	SetURL(name, url string) error
}

// runAppRemote 是可测核心：根据已解析的 host/port 与选项，为 git 仓库装配部署 remote。
//
//   - --print：只把 URL 写出，不改仓库。
//   - remote 不存在：git remote add。
//   - 已存在且 URL 相同：幂等，提示无需改动。
//   - 已存在且 URL 不同：需 --force 才 set-url，否则报错避免误改既有 remote。
func runAppRemote(w io.Writer, g gitRemoter, app, name, gitUser, host string, port int, doPrint, force bool) error {
	url, err := deployRemoteURL(gitUser, host, port, app)
	if err != nil {
		return err
	}
	if doPrint {
		fmt.Fprintln(w, url)
		return nil
	}

	existing, err := g.GetURL(name)
	if err != nil {
		return err
	}
	switch {
	case existing == "":
		if err := g.Add(name, url); err != nil {
			return fmt.Errorf("添加 git remote 失败：%w", err)
		}
		fmt.Fprintf(w, "✔ 已添加 remote %q → %s\n", name, url)
	case existing == url:
		fmt.Fprintf(w, "✔ remote %q 已指向 %s，无需改动\n", name, url)
	case !force:
		return fmt.Errorf("remote %q 已存在且不同（当前 %s）；如需覆盖请加 --force", name, existing)
	default:
		if err := g.SetURL(name, url); err != nil {
			return fmt.Errorf("更新 git remote 失败：%w", err)
		}
		fmt.Fprintf(w, "✔ 已更新 remote %q → %s（原 %s）\n", name, url, existing)
	}
	fmt.Fprintf(w, "\n部署：git push %s <部署分支，通常 main>\n", name)
	return nil
}

// execGitRemoter 是 gitRemoter 的生产实现，通过 git CLI 在当前工作目录操作。
type execGitRemoter struct{}

func (execGitRemoter) GetURL(name string) (string, error) {
	out, err := exec.Command("git", "remote", "get-url", name).Output()
	if err != nil {
		// remote 不存在时 git 以非零退出；视为「不存在」而非错误。
		if _, ok := err.(*exec.ExitError); ok {
			return "", nil
		}
		return "", fmt.Errorf("执行 git 失败（当前目录是 git 仓库吗？）：%w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (execGitRemoter) Add(name, url string) error {
	return exec.Command("git", "remote", "add", name, url).Run()
}

func (execGitRemoter) SetURL(name, url string) error {
	return exec.Command("git", "remote", "set-url", name, url).Run()
}

var appRemoteCmd = &cobra.Command{
	Use:   "remote <app>",
	Short: "为当前 git 仓库添加 Dokku 部署 remote（dokku@主机:应用）",
	Long: `在当前 git 仓库添加（或更新）指向 Dokku 主机的 git 部署 remote，
之后即可 git push <remote> <分支> 部署。

主机地址自动解析：优先 .bs.yaml 的 ssh.host，否则用登录后缓存的在线主机目录
（bk host ls）。git 推送用户固定为 dokku（Dokku 的部署用户），与 ssh.user 无关。

remote 默认名为 dokku（刻意不叫 origin，避免覆盖你的 GitHub origin）。

示例：
  bk app remote web                 # 添加 remote dokku → dokku@<主机>:web
  bk app remote web --print         # 只打印 URL，不改仓库
  bk app remote web --name prod     # 自定义 remote 名
  bk app remote web --force         # remote 已存在且不同也覆盖
  git push dokku main               # 随后部署`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		host, port := appRemoteHost, appRemotePort
		// 未显式覆盖主机时，从 SSHConfig（含在线目录回退）解析。
		if host == "" {
			cfg, err := SSHConfig(profile)
			if err != nil {
				return fmt.Errorf("解析部署主机失败：%w；可用 --host 指定", err)
			}
			host = cfg.Host
			if port == 0 {
				port = cfg.Port
			}
		}
		return runAppRemote(cmd.OutOrStdout(), execGitRemoter{}, args[0],
			appRemoteName, appRemoteGitUser, host, port, appRemotePrint, appRemoteForce)
	},
}

func init() {
	appRemoteCmd.Flags().StringVar(&appRemoteName, "name", "dokku", "git remote 名")
	appRemoteCmd.Flags().StringVar(&appRemoteGitUser, "git-user", "dokku", "git 部署用户（Dokku 默认 dokku）")
	appRemoteCmd.Flags().StringVar(&appRemoteHost, "host", "", "覆盖部署主机地址（默认取 ssh.host / 在线主机目录）")
	appRemoteCmd.Flags().IntVar(&appRemotePort, "port", 0, "覆盖 SSH 端口（默认取 ssh.port / 22）")
	appRemoteCmd.Flags().BoolVar(&appRemotePrint, "print", false, "只打印部署 URL，不修改仓库")
	appRemoteCmd.Flags().BoolVar(&appRemoteForce, "force", false, "remote 已存在且 URL 不同也覆盖")
	appCmd.AddCommand(appRemoteCmd)
}
