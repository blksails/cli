package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestResolveDeployBranch(t *testing.T) {
	// per-app 优先
	rep := `=====> web git information
       Git deploy branch:             main
       Git global deploy branch:      master`
	if b := resolveDeployBranch(rep); b != "main" {
		t.Fatalf("per-app 应优先，得 %q", b)
	}
	// per-app 空 → 用 global
	rep2 := `       Git deploy branch:
       Git global deploy branch:      master`
	if b := resolveDeployBranch(rep2); b != "master" {
		t.Fatalf("应回退 global master，得 %q", b)
	}
	// 都没有 → master
	if b := resolveDeployBranch("no branch info here"); b != "master" {
		t.Fatalf("应回退 master，得 %q", b)
	}
}

func TestPushRefspec(t *testing.T) {
	if got := pushRefspec("main", "main"); got != "main" {
		t.Fatalf("同名应直接用分支名，得 %q", got)
	}
	if got := pushRefspec("main", "master"); got != "main:master" {
		t.Fatalf("异名应映射，得 %q", got)
	}
	if got := pushRefspec("feature", ""); got != "feature" {
		t.Fatalf("部署分支空应直接用本地，得 %q", got)
	}
}

// fakeGitDeployer 实现 gitDeployer，记录 push 调用。
type fakeGitDeployer struct {
	existing   string
	branch     string
	added      [2]string
	pushed     [2]string
	branchErr  error
}

func (f *fakeGitDeployer) GetURL(string) (string, error)  { return f.existing, nil }
func (f *fakeGitDeployer) Add(n, u string) error          { f.added = [2]string{n, u}; return nil }
func (f *fakeGitDeployer) SetURL(string, string) error    { return nil }
func (f *fakeGitDeployer) CurrentBranch() (string, error) { return f.branch, f.branchErr }
func (f *fakeGitDeployer) Push(remote, refspec string) error {
	f.pushed = [2]string{remote, refspec}
	return nil
}

func TestRunAppDeploy_AddsRemoteAndPushesMapped(t *testing.T) {
	f := &fakeGitDeployer{existing: "", branch: "main"}
	var buf bytes.Buffer
	getBranch := func() (string, error) { return "master", nil } // 应用部署分支 master
	err := runAppDeploy(&buf, f, "web", "dokku", "1.2.3.4", 22, getBranch, "", "", false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.added != [2]string{"dokku", "dokku@1.2.3.4:web"} {
		t.Fatalf("应自动加 remote，实际 %v", f.added)
	}
	if f.pushed != [2]string{"dokku", "main:master"} {
		t.Fatalf("应映射推送 main:master，实际 %v", f.pushed)
	}
}

func TestRunAppDeploy_SameBranchNoMapping(t *testing.T) {
	f := &fakeGitDeployer{existing: "dokku@h:web", branch: "main"}
	var buf bytes.Buffer
	getBranch := func() (string, error) { return "main", nil }
	if err := runAppDeploy(&buf, f, "web", "dokku", "h", 22, getBranch, "", "", false); err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.added[0] != "" {
		t.Fatal("remote 已存在不应再 add")
	}
	if f.pushed != [2]string{"dokku", "main"} {
		t.Fatalf("同名应直接推 main，实际 %v", f.pushed)
	}
}

func TestRunAppDeploy_OverrideBranchAndExplicitLocal(t *testing.T) {
	f := &fakeGitDeployer{existing: "dokku@h:web", branch: "should-not-be-used"}
	var buf bytes.Buffer
	called := false
	getBranch := func() (string, error) { called = true; return "x", nil }
	// localRef=release，overrideBranch=master → 不应调用 getDeployBranch
	if err := runAppDeploy(&buf, f, "web", "dokku", "h", 22, getBranch, "release", "master", false); err != nil {
		t.Fatalf("err: %v", err)
	}
	if called {
		t.Fatal("--branch 覆盖时不应查 dokku")
	}
	if f.pushed != [2]string{"dokku", "release:master"} {
		t.Fatalf("应推 release:master，实际 %v", f.pushed)
	}
}

func TestRunAppDeploy_DryRunNoPush(t *testing.T) {
	f := &fakeGitDeployer{existing: "dokku@h:web", branch: "main"}
	var buf bytes.Buffer
	getBranch := func() (string, error) { return "main", nil }
	if err := runAppDeploy(&buf, f, "web", "dokku", "h", 22, getBranch, "", "", true); err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.pushed[0] != "" {
		t.Fatal("--dry-run 不应推送")
	}
	if !strings.Contains(buf.String(), "dry-run") {
		t.Fatalf("应提示 dry-run，实际：%q", buf.String())
	}
}
