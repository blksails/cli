/*
Copyright © 2025 BlackSails
*/
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

// app_deploy.go 实现 `bk app deploy <app> [本地分支]`：把"加 remote → 查部署分支 →
// git push 正确映射"收敛成一条命令，免去手动 git remote add 与 main:master 分支坑。
//
// 复用 app_remote.go 的 deployRemoteURL 与 execGitRemoter（GetURL/Add/SetURL），
// 并扩展 CurrentBranch/Push。部署分支经 dokku `git:report <app>` 解析。

var (
	appDeployRemote string // --remote：remote 名（默认 dokku）
	appDeployBranch string // --branch：覆盖部署分支（默认从 dokku git:report 解析）
	appDeployHost   string // --host：覆盖主机
	appDeployPort   int    // --port：覆盖端口
	appDeployDryRun bool   // --dry-run：只打印将执行的 git push，不真正推送
)

// resolveDeployBranch 从 dokku `git:report` 输出解析有效部署分支：
// 优先 per-app "Git deploy branch"，否则 "Git global deploy branch"，再否则回退 master。
func resolveDeployBranch(report string) string {
	perApp, global := "", ""
	for _, line := range strings.Split(report, "\n") {
		l := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(l, "Git global deploy branch:"):
			global = strings.TrimSpace(strings.TrimPrefix(l, "Git global deploy branch:"))
		case strings.HasPrefix(l, "Git deploy branch:"):
			perApp = strings.TrimSpace(strings.TrimPrefix(l, "Git deploy branch:"))
		}
	}
	if perApp != "" {
		return perApp
	}
	if global != "" {
		return global
	}
	return "master"
}

// pushRefspec 构造 git push 的 refspec：本地与部署分支同名时直接用分支名，
// 否则用 <本地>:<部署> 映射（兜住"本地 main、应用部署分支 master"的坑）。
func pushRefspec(local, deploy string) string {
	if deploy == "" || local == deploy {
		return local
	}
	return local + ":" + deploy
}

// gitDeployer 在 gitRemoter（GetURL/Add/SetURL）基础上加部署所需的当前分支与推送。
type gitDeployer interface {
	gitRemoter
	CurrentBranch() (string, error)
	Push(remote, refspec string) error
}

func (execGitRemoter) CurrentBranch() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("获取当前分支失败（当前目录是 git 仓库吗？）：%w", err)
	}
	b := strings.TrimSpace(string(out))
	if b == "HEAD" || b == "" {
		return "", fmt.Errorf("当前处于 detached HEAD：请用 `bk app deploy <app> <本地分支>` 指定要推送的分支")
	}
	return b, nil
}

func (execGitRemoter) Push(remote, refspec string) error {
	c := exec.Command("git", "push", remote, refspec)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	return c.Run()
}

// runAppDeploy 是可测核心：确保 remote 就绪、确定部署分支与本地分支、执行（或 dry-run）推送。
//
//   - remote 不存在 → 自动添加（dokku@主机:应用）；已存在 → 沿用。
//   - 部署分支：overrideBranch 优先，否则 getDeployBranch()（查 dokku git:report）。
//   - 本地分支：localRef 优先，否则当前分支。
//   - refspec：同名直接用分支名，异名用 <本地>:<部署>。
func runAppDeploy(w io.Writer, g gitDeployer, app, remoteName, host string, port int,
	getDeployBranch func() (string, error), localRef, overrideBranch string, dryRun bool) error {

	// 1) 确保 remote 就绪。
	url, err := deployRemoteURL("dokku", host, port, app)
	if err != nil {
		return err
	}
	existing, err := g.GetURL(remoteName)
	if err != nil {
		return err
	}
	if existing == "" {
		if err := g.Add(remoteName, url); err != nil {
			return fmt.Errorf("添加 git remote 失败：%w", err)
		}
		fmt.Fprintf(w, "✔ 已添加 remote %q → %s\n", remoteName, url)
	} else {
		fmt.Fprintf(w, "· remote %q → %s\n", remoteName, existing)
	}

	// 2) 部署分支。
	deployBranch := overrideBranch
	if deployBranch == "" {
		deployBranch, err = getDeployBranch()
		if err != nil {
			return fmt.Errorf("解析应用部署分支失败：%w（可用 --branch 指定）", err)
		}
	}

	// 3) 本地分支。
	local := localRef
	if local == "" {
		local, err = g.CurrentBranch()
		if err != nil {
			return err
		}
	}

	refspec := pushRefspec(local, deployBranch)
	fmt.Fprintf(w, "→ 部署 %s：git push %s %s（本地 %s → 部署分支 %s）\n",
		app, remoteName, refspec, local, deployBranch)

	if dryRun {
		fmt.Fprintln(w, "（--dry-run）未实际推送。")
		return nil
	}
	if err := g.Push(remoteName, refspec); err != nil {
		return fmt.Errorf("git push 失败：%w", err)
	}
	fmt.Fprintf(w, "✔ 已推送部署。用 `bk app logs %s -t` 看构建/运行日志。\n", app)
	return nil
}

var appDeployCmd = &cobra.Command{
	Use:   "deploy <app> [本地分支]",
	Short: "部署应用：自动配 remote、解析部署分支并 git push",
	Long: `一条命令完成部署：自动添加 Dokku git remote（若缺）、查应用的部署分支、
并用正确的 <本地分支>:<部署分支> 映射执行 git push。

主机地址自动解析（.bs.yaml 的 ssh.host 或在线主机目录）。部署分支经 dokku git:report
解析（per-app 优先，否则全局，再否则 master），可用 --branch 覆盖。本地分支默认当前分支，
也可作为第二个参数显式指定。

示例：
  bk app deploy web                 # 用当前分支部署 web
  bk app deploy web main            # 显式用本地 main 分支
  bk app deploy web --branch master # 覆盖部署分支
  bk app deploy web --dry-run       # 只看将执行的 git push，不推送`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		app := args[0]
		localRef := ""
		if len(args) == 2 {
			localRef = args[1]
		}

		host, port := appDeployHost, appDeployPort
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

		getDeployBranch := func() (string, error) {
			c, err := appClient(profile)
			if err != nil {
				return "", err
			}
			defer c.Close()
			out, err := c.Run(context.Background(), "git:report", app)
			if err != nil {
				return "", err
			}
			return resolveDeployBranch(out), nil
		}

		return runAppDeploy(cmd.OutOrStdout(), execGitRemoter{}, app, appDeployRemote, host, port,
			getDeployBranch, localRef, appDeployBranch, appDeployDryRun)
	},
}

func init() {
	appDeployCmd.Flags().StringVar(&appDeployRemote, "remote", "dokku", "git remote 名")
	appDeployCmd.Flags().StringVar(&appDeployBranch, "branch", "", "覆盖应用部署分支（默认从 dokku git:report 解析）")
	appDeployCmd.Flags().StringVar(&appDeployHost, "host", "", "覆盖部署主机地址（默认取 ssh.host / 在线主机目录）")
	appDeployCmd.Flags().IntVar(&appDeployPort, "port", 0, "覆盖 SSH 端口（默认取 ssh.port / 22）")
	appDeployCmd.Flags().BoolVar(&appDeployDryRun, "dry-run", false, "只打印将执行的 git push，不实际推送")
	appCmd.AddCommand(appDeployCmd)
}
