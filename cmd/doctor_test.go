package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"pkg.blksails.net/bk/internal/auth"
	"pkg.blksails.net/bk/internal/sshx"
)

// doctorNow 是一个固定的注入时间，使会话有效/过期判定与墙上时钟无关。
var doctorNow = time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)

// validDoctorSession 构造一个携带非空 token、过期时间在未来的会话，
// 供「全部通过」与「无 token 泄漏」场景使用。
func validDoctorSession(access, refresh, email string) *auth.Session {
	return &auth.Session{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenType:    "bearer",
		ExpiresIn:    3600,
		ExpiresAt:    doctorNow.Add(time.Hour).Unix(),
		User:         auth.User{Email: email},
	}
}

// okProbe 是一个永远成功的可达性探针（模拟 SSH 主机可达，无需真实网络）。
func okProbe(sshx.Config) error { return nil }

// failProbe 是一个永远失败的可达性探针（模拟 SSH 主机不可达，无需真实网络）。
func failProbe(sshx.Config) error { return errors.New("dial tcp: connection refused") }

// findCheck 按名称在结果集中查找某一项检查。
func findCheck(t *testing.T, results []checkResult, name string) checkResult {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("check %q not found in results %+v", name, results)
	return checkResult{}
}

// TestRunDoctorChecks_AllPass 验证 Requirement 12.1/12.2/12.3/12.5：
// 配置可解析、会话有效、SSH 可达（fake 探针返回 nil）→ overallOK 为真，
// 三项检查均 OK，且汇总输出对每项报告 OK。
func TestRunDoctorChecks_AllPass(t *testing.T) {
	const (
		access  = "doctorAccessTokenSECRET1234567890"
		refresh = "doctorRefreshTokenSECRET0987654321"
	)
	in := doctorInputs{
		ConfigOK:      true,
		ConfigErr:     nil,
		Profile:       "production",
		Session:       validDoctorSession(access, refresh, "alice@example.com"),
		Now:           doctorNow,
		SSHConfigured: true,
		SSHConfig:     sshx.Config{Host: "node1"},
		SSHProbe:      okProbe,
	}

	results, ok := runDoctorChecks(in)
	if !ok {
		t.Fatalf("expected overallOK true when all checks pass, got false; results=%+v", results)
	}
	for _, r := range results {
		if r.Skipped {
			t.Errorf("no check should be skipped in all-pass scenario, got skipped %q", r.Name)
		}
		if !r.OK {
			t.Errorf("check %q should be OK in all-pass scenario, got %+v", r.Name, r)
		}
	}

	var out bytes.Buffer
	writeDoctorReport(&out, results, ok)
	msg := out.String()
	if !strings.Contains(msg, "OK") {
		t.Errorf("summary should report OK for passing checks, got %q", msg)
	}
	assertNoTokenLeak(t, msg, access, refresh)
}

// TestRunDoctorChecks_ConfigUnparseable 验证 Requirement 12.1/12.4/12.6：
// `.bs.yaml` 无法解析时该项失败、给出可执行修复建议，且整体非零（overallOK 假）。
func TestRunDoctorChecks_ConfigUnparseable(t *testing.T) {
	in := doctorInputs{
		ConfigOK:      false,
		ConfigErr:     errors.New("yaml: line 3: mapping values not allowed"),
		Profile:       "default",
		Session:       validDoctorSession("a-token", "r-token", "x@example.com"),
		Now:           doctorNow,
		SSHConfigured: false,
	}

	results, ok := runDoctorChecks(in)
	if ok {
		t.Fatalf("expected overallOK false when config is unparseable, got true; results=%+v", results)
	}
	cfg := findCheck(t, results, doctorCheckConfig)
	if cfg.OK {
		t.Errorf("config check should fail, got %+v", cfg)
	}
	if strings.TrimSpace(cfg.Suggestion) == "" {
		t.Errorf("failed config check must carry an actionable suggestion, got %+v", cfg)
	}

	var out bytes.Buffer
	writeDoctorReport(&out, results, ok)
	if msg := out.String(); !strings.Contains(msg, cfg.Suggestion) {
		t.Errorf("report should surface the config suggestion %q, got %q", cfg.Suggestion, msg)
	}
}

// TestRunDoctorChecks_NoSession 验证 Requirement 12.2/12.4/12.6：
// 当前 profile 无会话时登录态检查失败，建议运行 `bk auth login`，整体非零。
func TestRunDoctorChecks_NoSession(t *testing.T) {
	in := doctorInputs{
		ConfigOK:      true,
		Profile:       "default",
		Session:       nil, // 未登录
		Now:           doctorNow,
		SSHConfigured: false,
	}

	results, ok := runDoctorChecks(in)
	if ok {
		t.Fatalf("expected overallOK false when there is no session, got true; results=%+v", results)
	}
	login := findCheck(t, results, doctorCheckLogin)
	if login.OK {
		t.Errorf("login check should fail when no session, got %+v", login)
	}

	var out bytes.Buffer
	writeDoctorReport(&out, results, ok)
	if msg := out.String(); !strings.Contains(msg, "bk auth login") {
		t.Errorf("report should suggest `bk auth login` when not logged in, got %q", msg)
	}
}

// TestRunDoctorChecks_SSHNotConfigured 验证 Requirement 12.3/12.5：
// 未配置 ssh 块时 SSH 检查被标记为跳过（不算失败）；其它检查通过 → 整体零退出。
func TestRunDoctorChecks_SSHNotConfigured(t *testing.T) {
	probeCalled := false
	in := doctorInputs{
		ConfigOK:      true,
		Profile:       "default",
		Session:       validDoctorSession("a", "b", "c@example.com"),
		Now:           doctorNow,
		SSHConfigured: false,
		SSHProbe: func(sshx.Config) error {
			probeCalled = true
			return errors.New("should not be called")
		},
	}

	results, ok := runDoctorChecks(in)
	if !ok {
		t.Fatalf("skipped SSH check must not fail overall when others pass; results=%+v", results)
	}
	if probeCalled {
		t.Errorf("SSH probe must not run when ssh block is not configured")
	}
	ssh := findCheck(t, results, doctorCheckSSH)
	if !ssh.Skipped {
		t.Errorf("SSH check should be Skipped when not configured, got %+v", ssh)
	}
	if !ssh.OK {
		t.Errorf("a skipped SSH check must not count as a failure, got %+v", ssh)
	}
}

// TestRunDoctorChecks_SSHUnreachable 验证 Requirement 12.3/12.4/12.6：
// 已配置 ssh 块但探针返回错误 → SSH 检查失败、给出 ssh.host 检查建议，整体非零；
// 但配置与登录态两项仍照常运行并通过（单项 SSH 失败不影响其它检查）。
func TestRunDoctorChecks_SSHUnreachable(t *testing.T) {
	in := doctorInputs{
		ConfigOK:      true,
		Profile:       "default",
		Session:       validDoctorSession("a", "b", "c@example.com"),
		Now:           doctorNow,
		SSHConfigured: true,
		SSHConfig:     sshx.Config{Host: "unreachable-host"},
		SSHProbe:      failProbe,
	}

	results, ok := runDoctorChecks(in)
	if ok {
		t.Fatalf("expected overallOK false when SSH host is unreachable, got true; results=%+v", results)
	}

	// 配置与登录态两项仍运行并通过。
	if cfg := findCheck(t, results, doctorCheckConfig); !cfg.OK {
		t.Errorf("config check should still pass despite SSH failure, got %+v", cfg)
	}
	if login := findCheck(t, results, doctorCheckLogin); !login.OK {
		t.Errorf("login check should still pass despite SSH failure, got %+v", login)
	}

	ssh := findCheck(t, results, doctorCheckSSH)
	if ssh.OK || ssh.Skipped {
		t.Errorf("SSH check should be a hard failure when unreachable, got %+v", ssh)
	}
	if strings.TrimSpace(ssh.Suggestion) == "" {
		t.Errorf("failed SSH check must carry a suggestion (e.g. check ssh.host), got %+v", ssh)
	}

	var out bytes.Buffer
	writeDoctorReport(&out, results, ok)
	if msg := out.String(); !strings.Contains(msg, "ssh.host") {
		t.Errorf("report should suggest checking ssh.host on SSH failure, got %q", msg)
	}
}

// TestRunDoctorChecks_ExpiredSession 验证 Requirement 12.2/12.4/12.6：
// 会话已过期 → 登录态检查失败，建议重新登录，整体非零。
func TestRunDoctorChecks_ExpiredSession(t *testing.T) {
	s := validDoctorSession("a", "b", "c@example.com")
	s.ExpiresAt = doctorNow.Add(-time.Hour).Unix() // 过期

	in := doctorInputs{
		ConfigOK:      true,
		Profile:       "default",
		Session:       s,
		Now:           doctorNow,
		SSHConfigured: false,
	}

	results, ok := runDoctorChecks(in)
	if ok {
		t.Fatalf("expected overallOK false when session expired, got true; results=%+v", results)
	}
	login := findCheck(t, results, doctorCheckLogin)
	if login.OK {
		t.Errorf("login check should fail for an expired session, got %+v", login)
	}

	var out bytes.Buffer
	writeDoctorReport(&out, results, ok)
	if msg := out.String(); !strings.Contains(msg, "login") {
		t.Errorf("report should hint re-login for an expired session, got %q", msg)
	}
}

// TestRunDoctorChecks_NoTokenLeak 验证 Requirement 12.7：
// 即使会话携带非空 access/refresh token，doctor 输出也绝不包含 token 明文。
func TestRunDoctorChecks_NoTokenLeak(t *testing.T) {
	const (
		access  = "leakAccessTokenSECRETvalueXYZ123"
		refresh = "leakRefreshTokenSECRETvalueABC987"
	)
	in := doctorInputs{
		ConfigOK:      true,
		Profile:       "production",
		Session:       validDoctorSession(access, refresh, "alice@example.com"),
		Now:           doctorNow,
		SSHConfigured: true,
		SSHConfig:     sshx.Config{Host: "node1"},
		SSHProbe:      okProbe,
	}

	results, ok := runDoctorChecks(in)
	var out bytes.Buffer
	writeDoctorReport(&out, results, ok)
	assertNoTokenLeak(t, out.String(), access, refresh)
}

// --- Task 7.2: 退出码与脱敏回归测试 -----------------------------------------
//
// 以下用例针对 5.1 review 标记的回归缺口补充覆盖：
//   - 12.5（全部通过 → 零退出）/ 12.6（关键失败 → 非零退出）：直接验证 RunE 将
//     overallOK 映射为退出码的薄缝（doctorExitError）。
//   - 配置失败路径仍运行并报告登录态与 SSH 两项检查（所有检查始终执行）。
//   - 12.3（未配 ssh → SKIP，非 FAIL）/ 12.7（输出无 token）：从 writeDoctorReport
//     渲染出的完整报告文本上回归断言（SKIP 标记 + 零 token 子串），即便会话携带
//     非空 token。

// TestDoctorExitError_AllPassZeroExit 验证 Requirement 12.5：
// 当所有关键检查通过（overallOK 为真）时，RunE 的退出映射返回 nil，使命令以零退出码结束。
func TestDoctorExitError_AllPassZeroExit(t *testing.T) {
	if err := doctorExitError(true); err != nil {
		t.Fatalf("doctorExitError(true) should return nil for zero exit, got %v", err)
	}
}

// TestDoctorExitError_CriticalFailNonZeroExit 验证 Requirement 12.6：
// 当存在关键检查失败（overallOK 为假）时，RunE 的退出映射返回非 nil error，
// 使 Execute() 以非零退出码结束，便于脚本据退出码判定健康状态。
func TestDoctorExitError_CriticalFailNonZeroExit(t *testing.T) {
	err := doctorExitError(false)
	if err == nil {
		t.Fatalf("doctorExitError(false) must return a non-nil error to force non-zero exit")
	}
	// 退出错误本身不应泄露任何敏感信息；这里仅确保它是一个非空可读消息。
	if strings.TrimSpace(err.Error()) == "" {
		t.Errorf("doctor exit error should carry a human-readable message, got empty")
	}
}

// TestDoctorExitError_MatchesRunEPipeline 验证 12.5/12.6 的端到端一致性：
// 把同一组注入输入喂给纯核心 runDoctorChecks，再经 doctorExitError 映射，
// 全通过场景得到 nil（零退出），存在关键失败场景得到非 nil（非零退出）。
// 这覆盖了 cobra RunE 中 (results, overallOK) → 退出码 的映射缝，而无需真实装配
// gatherDoctorInputs / realSSHProbe。
func TestDoctorExitError_MatchesRunEPipeline(t *testing.T) {
	allPass := doctorInputs{
		ConfigOK:      true,
		Profile:       "production",
		Session:       validDoctorSession("a-token", "r-token", "alice@example.com"),
		Now:           doctorNow,
		SSHConfigured: true,
		SSHConfig:     sshx.Config{Host: "node1"},
		SSHProbe:      okProbe,
	}
	if _, ok := runDoctorChecks(allPass); !ok {
		t.Fatalf("precondition: all-pass inputs should yield overallOK true")
	} else if err := doctorExitError(ok); err != nil {
		t.Errorf("all-pass pipeline should map to zero exit (nil error), got %v", err)
	}

	criticalFail := doctorInputs{
		ConfigOK:      true,
		Profile:       "default",
		Session:       nil, // 未登录 → 关键失败
		Now:           doctorNow,
		SSHConfigured: false,
	}
	if _, ok := runDoctorChecks(criticalFail); ok {
		t.Fatalf("precondition: no-session inputs should yield overallOK false")
	} else if err := doctorExitError(ok); err == nil {
		t.Errorf("critical-failure pipeline should map to non-zero exit (non-nil error), got nil")
	}
}

// TestRunDoctorChecks_ConfigFailStillRunsAllChecks 回归补强（5.1 review 标记）：
// 配置项失败时，其余检查仍照常运行——结果集中必须仍包含登录态与 SSH 两项检查条目，
// 且其各自的状态由各自输入决定，而非因配置失败被跳过/省略。
func TestRunDoctorChecks_ConfigFailStillRunsAllChecks(t *testing.T) {
	in := doctorInputs{
		ConfigOK:      false,
		ConfigErr:     errors.New("yaml: line 3: mapping values not allowed"),
		Profile:       "production",
		Session:       validDoctorSession("a-token", "r-token", "alice@example.com"),
		Now:           doctorNow,
		SSHConfigured: true,
		SSHConfig:     sshx.Config{Host: "node1"},
		SSHProbe:      okProbe,
	}

	results, ok := runDoctorChecks(in)
	if ok {
		t.Fatalf("expected overallOK false when config fails, got true; results=%+v", results)
	}

	// 配置失败。
	if cfg := findCheck(t, results, doctorCheckConfig); cfg.OK {
		t.Errorf("config check should fail, got %+v", cfg)
	}
	// 关键回归：登录态与 SSH 两项检查仍存在于结果集，且仍照常评估（这里均通过）。
	login := findCheck(t, results, doctorCheckLogin)
	if !login.OK {
		t.Errorf("login check should still run and pass despite config failure, got %+v", login)
	}
	ssh := findCheck(t, results, doctorCheckSSH)
	if !ssh.OK || ssh.Skipped {
		t.Errorf("ssh check should still run and pass despite config failure, got %+v", ssh)
	}

	// 渲染报告中应同时出现三项检查名（证明全部检查都被报告）。
	var out bytes.Buffer
	writeDoctorReport(&out, results, ok)
	msg := out.String()
	for _, name := range []string{doctorCheckConfig, doctorCheckLogin, doctorCheckSSH} {
		if !strings.Contains(msg, name) {
			t.Errorf("report should contain check %q even when config fails, got %q", name, msg)
		}
	}
}

// TestWriteDoctorReport_SSHSkipMarkerAndNoTokenLeak 回归补强 12.3 + 12.7：
// 即便当前 profile 携带非空 access/refresh token，且 ssh 未配置，
// 渲染出的完整报告必须 (a) 对 SSH 项呈现 SKIP 标记（而非 FAIL），
// (b) 完全不含任一 token 明文子串。该用例从渲染输出而非 checkResult 字段断言，
// 覆盖「报告文本层」的脱敏与跳过语义。
func TestWriteDoctorReport_SSHSkipMarkerAndNoTokenLeak(t *testing.T) {
	const (
		access  = "renderAccessTokenSECRETqwerty4567"
		refresh = "renderRefreshTokenSECRETzxcvb8901"
	)
	in := doctorInputs{
		ConfigOK:      true,
		Profile:       "production",
		Session:       validDoctorSession(access, refresh, "alice@example.com"),
		Now:           doctorNow,
		SSHConfigured: false, // 未配置 ssh → 应跳过，而非失败
	}

	results, ok := runDoctorChecks(in)
	if !ok {
		t.Fatalf("expected overallOK true (ssh skip must not fail overall); results=%+v", results)
	}

	var out bytes.Buffer
	writeDoctorReport(&out, results, ok)
	msg := out.String()

	// (a) 报告文本中 SSH 项呈现 SKIP，且不呈现 FAIL（针对该行的跳过语义）。
	if !strings.Contains(msg, "[SKIP] "+doctorCheckSSH) {
		t.Errorf("report should mark unconfigured ssh check as [SKIP], got %q", msg)
	}
	if strings.Contains(msg, "[FAIL] "+doctorCheckSSH) {
		t.Errorf("unconfigured ssh check must not render as [FAIL], got %q", msg)
	}

	// (b) 即便会话携带非空 token，渲染报告也绝不含 token 明文子串。
	assertNoTokenLeak(t, msg, access, refresh)
}
