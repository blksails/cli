package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/sshkeys"
)

// fakeKeyLister 是 keyLister 的可注入测试替身：按预置返回记录或错误，
// 使 runSSHKeyList 可在不触达 Supabase 的前提下被验证（design「Unit（Store，fake client）」）。
type fakeKeyLister struct {
	recs []sshkeys.KeyRecord
	err  error
}

func (f fakeKeyLister) ListMine() ([]sshkeys.KeyRecord, error) {
	return f.recs, f.err
}

// listPrivateKeyMarkers 是 PEM 私钥块会出现的额外标志串；list 输出绝不应包含其中任何一个
// （Requirement 4.2：list 输出不显示任何私钥内容）。复用 sshKeyProvision_test.go 的
// assertNoPrivateKeyLeak 覆盖 "PRIVATE KEY"，此处补充 BEGIN 块标志。
var listPrivateKeyMarkers = []string{
	"BEGIN OPENSSH",
	"-----BEGIN",
}

// assertNoListPrivateKeyLeak 在复用 assertNoPrivateKeyLeak 之上，额外断言 out 不含
// PEM BEGIN 块标志（Requirement 4.2）。
func assertNoListPrivateKeyLeak(t *testing.T, out string) {
	t.Helper()
	assertNoPrivateKeyLeak(t, out)
	for _, m := range listPrivateKeyMarkers {
		if strings.Contains(out, m) {
			t.Fatalf("list 输出泄露了私钥标志 %q：\n%s", m, out)
		}
	}
}

// TestRunSSHKeyList_MultipleRecords 覆盖 Requirement 4.1/4.2：多记录被列出，
// 含名称/指纹/主机/状态，且不含任何私钥内容。
func TestRunSSHKeyList_MultipleRecords(t *testing.T) {
	recs := []sshkeys.KeyRecord{
		{
			Name:        "bk-alice-app1",
			Host:        "app1.example.com",
			Fingerprint: "SHA256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Status:      sshkeys.StatusPending,
			CreatedAt:   "2026-06-01T10:00:00Z",
		},
		{
			Name:        "bk-alice-app2",
			Host:        "app2.example.com",
			Fingerprint: "SHA256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Status:      sshkeys.StatusInstalled,
			CreatedAt:   "2026-06-02T11:00:00Z",
			InstalledAt: "2026-06-03T12:00:00Z",
		},
		{
			Name:        "bk-alice-app3",
			Host:        "app3.example.com",
			Fingerprint: "SHA256:ccccccccccccccccccccccccccccccccccccccccccc",
			Status:      sshkeys.StatusRevoked,
			CreatedAt:   "2026-06-04T13:00:00Z",
			RevokedAt:   "2026-06-05T14:00:00Z",
		},
	}

	var buf bytes.Buffer
	if err := runSSHKeyList(&buf, fakeKeyLister{recs: recs}); err != nil {
		t.Fatalf("runSSHKeyList 返回意外错误：%v", err)
	}
	out := buf.String()

	for _, r := range recs {
		if !strings.Contains(out, r.Name) {
			t.Errorf("输出缺少名称 %q：\n%s", r.Name, out)
		}
		if !strings.Contains(out, r.Fingerprint) {
			t.Errorf("输出缺少指纹 %q：\n%s", r.Fingerprint, out)
		}
		if !strings.Contains(out, r.Host) {
			t.Errorf("输出缺少主机 %q：\n%s", r.Host, out)
		}
		if !strings.Contains(out, string(r.Status)) {
			t.Errorf("输出缺少状态 %q：\n%s", r.Status, out)
		}
	}

	// 相关时间应出现（Requirement 4.1）。
	if !strings.Contains(out, "2026-06-01T10:00:00Z") {
		t.Errorf("输出缺少 Created 时间：\n%s", out)
	}
	if !strings.Contains(out, "2026-06-03T12:00:00Z") {
		t.Errorf("输出缺少 Installed 时间：\n%s", out)
	}
	if !strings.Contains(out, "2026-06-05T14:00:00Z") {
		t.Errorf("输出缺少 Revoked 时间：\n%s", out)
	}

	// 应是表格（含表头列名）。
	if !strings.Contains(out, "Name") || !strings.Contains(out, "Fingerprint") ||
		!strings.Contains(out, "Host") || !strings.Contains(out, "Status") {
		t.Errorf("输出缺少表头列：\n%s", out)
	}

	assertNoListPrivateKeyLeak(t, out)
}

// TestRunSSHKeyList_Empty 覆盖 Requirement 4.3：无记录时给出友好空提示且不报错。
func TestRunSSHKeyList_Empty(t *testing.T) {
	var buf bytes.Buffer
	if err := runSSHKeyList(&buf, fakeKeyLister{recs: nil}); err != nil {
		t.Fatalf("空集应返回 nil，却得到错误：%v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "暂无") {
		t.Errorf("空集应给出友好空提示，实际输出：%q", out)
	}
	if !strings.Contains(out, "provision") {
		t.Errorf("空提示应引导用户运行 provision，实际输出：%q", out)
	}
	assertNoListPrivateKeyLeak(t, out)
}

// TestRunSSHKeyList_Error 覆盖错误透传：ListMine 失败时 runSSHKeyList 返回非 nil。
func TestRunSSHKeyList_Error(t *testing.T) {
	wantErr := errors.New("boom")
	var buf bytes.Buffer
	err := runSSHKeyList(&buf, fakeKeyLister{err: wantErr})
	if err == nil {
		t.Fatalf("ListMine 出错时应返回非 nil 错误")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("应透传底层错误，得到：%v", err)
	}
}
