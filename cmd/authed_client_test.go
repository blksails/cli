package cmd

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/supabase-community/supabase-go"
	"pkg.blksails.net/bk/internal/auth"
)

// fakeRefresher 是测试用 Refresher：记录是否被调用，并返回预置会话/错误。
type fakeRefresher struct {
	called     bool
	gotToken   string
	returnSess auth.Session
	returnErr  error
}

func (f *fakeRefresher) RefreshToken(refreshToken string) (auth.Session, error) {
	f.called = true
	f.gotToken = refreshToken
	if f.returnErr != nil {
		return auth.Session{}, f.returnErr
	}
	return f.returnSess, nil
}

// testUserID 必须是合法 UUID：auth.FromAuthConfig 内部用 uuid.MustParse。
const testUserID = "00000000-0000-0000-0000-000000000001"

// newBaseClient 构造一个底座 supabase client。NewClient 仅装配内部 client，不发起
// 网络请求；后续 UpdateAuthSession（经 FromAuthConfig）同样是纯内存操作。
func newBaseClient(t *testing.T) *supabase.Client {
	t.Helper()
	c, err := supabase.NewClient("http://localhost:0", "dummy-key", &supabase.ClientOptions{Schema: "blacksail"})
	if err != nil {
		t.Fatalf("build base client: %v", err)
	}
	return c
}

func seedAuthFile(t *testing.T, profile string, sess auth.Session) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := auth.AddAuthConfig(path, &auth.AuthConfig{Profile: profile, Session: sess}); err != nil {
		t.Fatalf("seed auth file: %v", err)
	}
	return path
}

func validSession(now time.Time) auth.Session {
	return auth.Session{
		AccessToken:  "valid-access",
		RefreshToken: "valid-refresh",
		TokenType:    "bearer",
		ExpiresIn:    3600,
		ExpiresAt:    now.Add(time.Hour).Unix(), // 远未过期，越过 skew 余量
		User:         auth.User{ID: testUserID, Email: "u@example.com"},
	}
}

func expiredSession(now time.Time) auth.Session {
	return auth.Session{
		AccessToken:  "old-access",
		RefreshToken: "good-refresh",
		TokenType:    "bearer",
		ExpiresIn:    3600,
		ExpiresAt:    now.Add(-time.Hour).Unix(), // 已过期
		User:         auth.User{ID: testUserID, Email: "u@example.com"},
	}
}

// (a) 有效会话 → 返回非 nil client，无错误，refresher 不被调用（Requirement 8.2）。
func TestAuthedClientWith_ValidSession_ReturnsClient(t *testing.T) {
	now := time.Now()
	path := seedAuthFile(t, "default", validSession(now))
	ref := &fakeRefresher{}

	client, err := authedClientWith(path, "default", now, ref, newBaseClient(t))
	if err != nil {
		t.Fatalf("expected no error for valid session, got: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client for valid session")
	}
	if ref.called {
		t.Fatal("refresher must NOT be called when session is valid")
	}
}

// (b) 无该 profile 会话 → 返回可识别为 ErrReloginRequired 的错误，client 为 nil
// （Requirement 8.3, 7.4）。
func TestAuthedClientWith_NoSession_ReturnsReloginRequired(t *testing.T) {
	now := time.Now()
	// 文件里只有别的 profile，目标 profile 缺失。
	path := seedAuthFile(t, "other", validSession(now))
	ref := &fakeRefresher{}

	client, err := authedClientWith(path, "missing", now, ref, newBaseClient(t))
	if err == nil {
		t.Fatal("expected error when profile has no session")
	}
	if !errors.Is(err, auth.ErrReloginRequired) {
		t.Fatalf("expected errors.Is ErrReloginRequired, got: %v", err)
	}
	if client != nil {
		t.Fatal("expected nil client when profile has no session")
	}
	if ref.called {
		t.Fatal("refresher must NOT be called when profile is missing")
	}
}

// (c) 过期会话 + refresher 成功 → 返回非 nil client，新会话被回写磁盘，无错误
// （Requirement 8.4, 10.2/10.3）。
func TestAuthedClientWith_ExpiredRefreshSuccess_WritesBack(t *testing.T) {
	now := time.Now()
	path := seedAuthFile(t, "default", expiredSession(now))

	newExpiresAt := now.Add(2 * time.Hour).Unix()
	ref := &fakeRefresher{returnSess: auth.Session{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		TokenType:    "bearer",
		ExpiresIn:    7200,
		ExpiresAt:    newExpiresAt,
		User:         auth.User{ID: testUserID, Email: "u@example.com"},
	}}

	client, err := authedClientWith(path, "default", now, ref, newBaseClient(t))
	if err != nil {
		t.Fatalf("expected no error after successful refresh, got: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client after successful refresh")
	}
	if !ref.called {
		t.Fatal("refresher must be called for expired session")
	}
	if ref.gotToken != "good-refresh" {
		t.Fatalf("refresher got token %q, want good-refresh", ref.gotToken)
	}

	// 断言新会话已回写磁盘：重新加载应看到刷新后的 access token 与过期时间。
	configs, lerr := auth.LoadAuthConfig(path)
	if lerr != nil {
		t.Fatalf("reload auth file: %v", lerr)
	}
	var found *auth.AuthConfig
	for _, c := range configs {
		if c != nil && c.Profile == "default" {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatal("profile default missing after refresh write-back")
	}
	if found.Session.AccessToken != "new-access" {
		t.Fatalf("auth.json not updated: access token = %q, want new-access", found.Session.AccessToken)
	}
	if found.Session.ExpiresAt != newExpiresAt {
		t.Fatalf("auth.json not updated: ExpiresAt = %d, want %d", found.Session.ExpiresAt, newExpiresAt)
	}
}

// (d) 过期会话 + refresher 失败 → 返回可识别为 ErrReloginRequired 的错误，无 client
// （Requirement 8.4, 10.4）。
func TestAuthedClientWith_ExpiredRefreshFailure_ReturnsReloginRequired(t *testing.T) {
	now := time.Now()
	path := seedAuthFile(t, "default", expiredSession(now))
	ref := &fakeRefresher{returnErr: errors.New("network down")}

	client, err := authedClientWith(path, "default", now, ref, newBaseClient(t))
	if err == nil {
		t.Fatal("expected error when refresh fails")
	}
	if !errors.Is(err, auth.ErrReloginRequired) {
		t.Fatalf("expected errors.Is ErrReloginRequired, got: %v", err)
	}
	if client != nil {
		t.Fatal("expected nil client when refresh fails")
	}
	if !ref.called {
		t.Fatal("refresher must be called for expired session before failing")
	}
}
