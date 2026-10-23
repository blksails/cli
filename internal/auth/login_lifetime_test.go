package auth

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestLoginDeadlineSurvivesRefreshAndLegacyMigration(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	path := writeAuthFile(t, "default", expiredSession(now))
	ref := &fakeRefresher{returnSess: expiredSession(now)}
	ref.returnSess.ExpiresAt = now.Add(time.Hour).Unix()
	if _, err := EnsureFresh(path, "default", now, testSkew, ref); err != nil {
		t.Fatal(err)
	}
	configs, _ := LoadAuthConfig(path)
	deadline := now.Add(LoginLifetime).Unix()
	if configs[0].LoginExpiresAt != deadline {
		t.Fatal("legacy login must receive a 365-day deadline")
	}
	if _, err := EnsureFresh(path, "default", now.Add(24*time.Hour), testSkew, ref); err != nil {
		t.Fatal(err)
	}
	configs, _ = LoadAuthConfig(path)
	if configs[0].LoginExpiresAt != deadline {
		t.Fatal("refresh moved the login deadline")
	}
}

func TestLoginDeadlineBoundaryRejectsEvenValidJWT(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	for _, offset := range []time.Duration{-time.Second, 0} {
		cfg := &AuthConfig{Profile: "default", Session: expiredSession(now), LoginExpiresAt: now.Add(offset).Unix()}
		cfg.Session.ExpiresAt = now.Add(time.Hour).Unix()
		path := writeAuthFile(t, "default", cfg.Session)
		if err := AddAuthConfig(path, cfg); err != nil {
			t.Fatal(err)
		}
		ref := &fakeRefresher{}
		if _, err := EnsureFresh(path, "default", now, testSkew, ref); !errors.Is(err, ErrReloginRequired) {
			t.Fatalf("expected login expiry: %v", err)
		}
		if ref.called {
			t.Fatal("expired login must not refresh")
		}
	}
}

func TestTemporaryRefreshFailurePreservesCredentials(t *testing.T) {
	now := time.Now()
	path := writeAuthFile(t, "default", expiredSession(now))
	before, _ := os.ReadFile(path)
	_, err := EnsureFresh(path, "default", now, testSkew, &fakeRefresher{returnErr: errors.New("Bad Gateway")})
	if !errors.Is(err, ErrRefreshUnavailable) || errors.Is(err, ErrReloginRequired) {
		t.Fatalf("wrong failure classification: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("temporary failure changed saved credentials")
	}
}
