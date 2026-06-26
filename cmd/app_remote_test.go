package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestDeployRemoteURL(t *testing.T) {
	cases := []struct {
		gitUser, host string
		port          int
		app, want     string
	}{
		{"dokku", "1.2.3.4", 22, "web", "dokku@1.2.3.4:web"},
		{"dokku", "1.2.3.4", 0, "web", "dokku@1.2.3.4:web"},     // 0 视为默认 22
		{"", "h", 0, "web", "dokku@h:web"},                      // gitUser 空回退 dokku
		{"dokku", "h", 2222, "api", "ssh://dokku@h:2222/api"},   // 非标准端口 ssh:// 形式
		{"deploy", "h", 22, "app", "deploy@h:app"},              // 自定义用户
	}
	for _, c := range cases {
		got, err := deployRemoteURL(c.gitUser, c.host, c.port, c.app)
		if err != nil {
			t.Fatalf("unexpected error for %+v: %v", c, err)
		}
		if got != c.want {
			t.Errorf("deployRemoteURL(%q,%q,%d,%q)=%q want %q", c.gitUser, c.host, c.port, c.app, got, c.want)
		}
	}
}

func TestDeployRemoteURL_Errors(t *testing.T) {
	if _, err := deployRemoteURL("dokku", "", 22, "web"); err == nil {
		t.Error("空 host 应报错")
	}
	if _, err := deployRemoteURL("dokku", "h", 22, ""); err == nil {
		t.Error("空 app 应报错")
	}
}

// fakeGitRemoter 记录调用并以预置状态模拟 git remote。
type fakeGitRemoter struct {
	existing  string
	added     [2]string
	setURLled [2]string
}

func (f *fakeGitRemoter) GetURL(string) (string, error) { return f.existing, nil }
func (f *fakeGitRemoter) Add(name, url string) error    { f.added = [2]string{name, url}; return nil }
func (f *fakeGitRemoter) SetURL(name, url string) error { f.setURLled = [2]string{name, url}; return nil }

func TestRunAppRemote_AddWhenMissing(t *testing.T) {
	f := &fakeGitRemoter{existing: ""}
	var buf bytes.Buffer
	if err := runAppRemote(&buf, f, "web", "dokku", "dokku", "1.2.3.4", 22, false, false); err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.added != [2]string{"dokku", "dokku@1.2.3.4:web"} {
		t.Fatalf("应 add remote，实际 %v", f.added)
	}
}

func TestRunAppRemote_IdempotentSameURL(t *testing.T) {
	f := &fakeGitRemoter{existing: "dokku@1.2.3.4:web"}
	var buf bytes.Buffer
	if err := runAppRemote(&buf, f, "web", "dokku", "dokku", "1.2.3.4", 22, false, false); err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.added[0] != "" || f.setURLled[0] != "" {
		t.Fatal("URL 相同时应幂等，不 add 也不 set-url")
	}
	if !strings.Contains(buf.String(), "无需改动") {
		t.Fatalf("应提示无需改动，实际：%q", buf.String())
	}
}

func TestRunAppRemote_DifferentURLNeedsForce(t *testing.T) {
	f := &fakeGitRemoter{existing: "dokku@old:web"}
	var buf bytes.Buffer
	err := runAppRemote(&buf, f, "web", "dokku", "dokku", "new", 22, false, false)
	if err == nil {
		t.Fatal("URL 不同且无 --force 应报错")
	}
	if f.setURLled[0] != "" {
		t.Fatal("无 --force 时不应 set-url")
	}
}

func TestRunAppRemote_ForceOverwrites(t *testing.T) {
	f := &fakeGitRemoter{existing: "dokku@old:web"}
	var buf bytes.Buffer
	if err := runAppRemote(&buf, f, "web", "dokku", "dokku", "new", 22, false, true); err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.setURLled != [2]string{"dokku", "dokku@new:web"} {
		t.Fatalf("--force 应 set-url，实际 %v", f.setURLled)
	}
}

func TestRunAppRemote_Print(t *testing.T) {
	f := &fakeGitRemoter{existing: "should-not-matter"}
	var buf bytes.Buffer
	if err := runAppRemote(&buf, f, "web", "dokku", "dokku", "1.2.3.4", 2222, true, false); err != nil {
		t.Fatalf("err: %v", err)
	}
	if strings.TrimSpace(buf.String()) != "ssh://dokku@1.2.3.4:2222/web" {
		t.Fatalf("--print 应只输出 URL，实际：%q", buf.String())
	}
	if f.added[0] != "" || f.setURLled[0] != "" {
		t.Fatal("--print 不应改动仓库")
	}
}
