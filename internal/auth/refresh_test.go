package auth

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// fakeRefresher 是测试用的 Refresher 实现，记录被调用情况并返回预置结果。
type fakeRefresher struct {
	called       bool
	gotToken     string
	returnSess   Session
	returnErr    error
}

func (f *fakeRefresher) RefreshToken(refreshToken string) (Session, error) {
	f.called = true
	f.gotToken = refreshToken
	if f.returnErr != nil {
		return Session{}, f.returnErr
	}
	return f.returnSess, nil
}

const testSkew = 60 * time.Second

func writeAuthFile(t *testing.T, profile string, sess Session) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	cfg := &AuthConfig{Profile: profile, Session: sess}
	if err := AddAuthConfig(path, cfg); err != nil {
		t.Fatalf("seed auth file: %v", err)
	}
	return path
}

func expiredSession(now time.Time) Session {
	return Session{
		AccessToken:  "old-access",
		RefreshToken: "good-refresh",
		TokenType:    "bearer",
		ExpiresIn:    3600,
		ExpiresAt:    now.Add(-time.Hour).Unix(), // 已过期
		User:         User{ID: "00000000-0000-0000-0000-000000000001", Email: "u@example.com"},
	}
}

// (a) 过期会话 + 有效 refresh → 调用 refresher，新会话回写磁盘，返回新会话。
func TestEnsureFresh_ExpiredRefreshSuccess_WritesBack(t *testing.T) {
	now := time.Now()
	old := expiredSession(now)
	path := writeAuthFile(t, "default", old)

	newExpiresAt := now.Add(2 * time.Hour).Unix()
	ref := &fakeRefresher{returnSess: Session{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		TokenType:    "bearer",
		ExpiresIn:    7200,
		ExpiresAt:    newExpiresAt,
		User:         old.User,
	}}

	got, err := EnsureFresh(path, "default", now, testSkew, ref)
	if err != nil {
		t.Fatalf("EnsureFresh returned error: %v", err)
	}
	if !ref.called {
		t.Fatal("expected refresher to be called for expired session")
	}
	if ref.gotToken != "good-refresh" {
		t.Fatalf("refresher got token %q, want good-refresh", ref.gotToken)
	}
	if got.AccessToken != "new-access" || got.ExpiresAt != newExpiresAt {
		t.Fatalf("returned session not refreshed: %+v", got)
	}

	// 断言新会话已持久化回磁盘。
	configs, err := LoadAuthConfig(path)
	if err != nil {
		t.Fatalf("reload auth file: %v", err)
	}
	var persisted *Session
	for _, c := range configs {
		if c.Profile == "default" {
			persisted = &c.Session
		}
	}
	if persisted == nil {
		t.Fatal("profile default not found after write-back")
	}
	if persisted.AccessToken != "new-access" || persisted.RefreshToken != "new-refresh" || persisted.ExpiresAt != newExpiresAt {
		t.Fatalf("persisted session not updated: %+v", *persisted)
	}
}

// (b) 过期 + refresher 返回错误 → 返回 ErrReloginRequired（errors.Is）。
func TestEnsureFresh_ExpiredRefreshFails_ReloginRequired(t *testing.T) {
	now := time.Now()
	path := writeAuthFile(t, "default", expiredSession(now))
	ref := &fakeRefresher{returnErr: errors.New("token revoked")}

	_, err := EnsureFresh(path, "default", now, testSkew, ref)
	if !errors.Is(err, ErrReloginRequired) {
		t.Fatalf("expected ErrReloginRequired, got %v", err)
	}
	if !ref.called {
		t.Fatal("expected refresher to be called before failing")
	}
}

// (c) 过期 + 空 refresh token → ErrReloginRequired，且不调用 refresher。
func TestEnsureFresh_ExpiredNoRefreshToken_ReloginRequired(t *testing.T) {
	now := time.Now()
	sess := expiredSession(now)
	sess.RefreshToken = ""
	path := writeAuthFile(t, "default", sess)
	ref := &fakeRefresher{}

	_, err := EnsureFresh(path, "default", now, testSkew, ref)
	if !errors.Is(err, ErrReloginRequired) {
		t.Fatalf("expected ErrReloginRequired, got %v", err)
	}
	if ref.called {
		t.Fatal("refresher must NOT be called when refresh token is empty")
	}
}

// (d) 未过期 → 原样返回，不调用 refresher。
func TestEnsureFresh_NotExpired_ReturnedUnchanged(t *testing.T) {
	now := time.Now()
	sess := expiredSession(now)
	sess.ExpiresAt = now.Add(2 * time.Hour).Unix() // 未过期
	path := writeAuthFile(t, "default", sess)
	ref := &fakeRefresher{}

	got, err := EnsureFresh(path, "default", now, testSkew, ref)
	if err != nil {
		t.Fatalf("EnsureFresh returned error: %v", err)
	}
	if ref.called {
		t.Fatal("refresher must NOT be called for a valid session")
	}
	if got.AccessToken != sess.AccessToken || got.ExpiresAt != sess.ExpiresAt {
		t.Fatalf("session changed unexpectedly: %+v", got)
	}
}

// profile 不存在 → ErrReloginRequired，不调用 refresher。
func TestEnsureFresh_ProfileMissing_ReloginRequired(t *testing.T) {
	now := time.Now()
	path := writeAuthFile(t, "default", expiredSession(now))
	ref := &fakeRefresher{}

	_, err := EnsureFresh(path, "other", now, testSkew, ref)
	if !errors.Is(err, ErrReloginRequired) {
		t.Fatalf("expected ErrReloginRequired for missing profile, got %v", err)
	}
	if ref.called {
		t.Fatal("refresher must NOT be called for missing profile")
	}
}
