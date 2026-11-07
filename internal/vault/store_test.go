package vault

import (
	"encoding/json"
	"errors"
	"github.com/supabase-community/supabase-go"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func rpcStore(t *testing.T, expected string, reply string, check func(map[string]interface{})) *Store {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/rest/v1/rpc/"+expected {
			t.Errorf("unexpected RPC: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Profile") != "cli" || r.Header.Get("Authorization") != "Bearer user-session" {
			t.Error("RPC must use cli schema and verified user session")
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if _, exists := payload["owner"]; exists {
			t.Error("must not send an owner")
		}
		if check != nil {
			check(payload)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	c, err := supabase.NewClient(server.URL, "anon-key", &supabase.ClientOptions{Schema: "cli", Headers: map[string]string{"Authorization": "Bearer user-session"}})
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(c)
}
func TestStoreNativeRPCs(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		s := rpcStore(t, "bk_vault_set", "true", func(p map[string]interface{}) {
			if p["p_value"] != "plain-value" || p["p_app"] != "app" || p["p_key"] != "TOKEN" {
				t.Error("incorrect parameters")
			}
		})
		if err := s.Set("app", "TOKEN", "plain-value"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("get", func(t *testing.T) {
		s := rpcStore(t, "bk_vault_get", `"plain-value"`, nil)
		got, err := s.Get("app", "TOKEN")
		if err != nil || got != "plain-value" {
			t.Fatalf("get=%q, %v", got, err)
		}
	})
	t.Run("empty value", func(t *testing.T) {
		s := rpcStore(t, "bk_vault_get", `""`, nil)
		got, err := s.Get("app", "TOKEN")
		if err != nil || got != "" {
			t.Fatal("empty value must be valid")
		}
	})
	t.Run("metadata list", func(t *testing.T) {
		s := rpcStore(t, "bk_vault_list_keys", `["A","B"]`, nil)
		got, err := s.ListKeys("app")
		if err != nil || len(got) != 2 {
			t.Fatal(got, err)
		}
	})
	t.Run("export", func(t *testing.T) {
		s := rpcStore(t, "bk_vault_list", `[{"app":"app","key":"A","value":"secret"}]`, nil)
		got, err := s.List("app")
		if err != nil || len(got) != 1 || got[0].Value != "secret" {
			t.Fatal(got, err)
		}
	})
	t.Run("remove", func(t *testing.T) {
		s := rpcStore(t, "bk_vault_remove", "true", nil)
		if err := s.Remove("app", "A"); err != nil {
			t.Fatal(err)
		}
	})
}
func TestRPCFailureClassificationAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		code string
		want error
	}{{"P0002", ErrNotFound}, {"42501", ErrPermission}, {"PGRST301", ErrPermission}, {"22023", nil}, {"PGRST202", nil}, {"SECRET123", nil}} {
		t.Run(tc.code, func(t *testing.T) {
			s := rpcStore(t, "bk_vault_set", `{"code":"`+tc.code+`","message":"SENSITIVE-VALUE","details":"SENSITIVE-VALUE"}`, nil)
			err := s.Set("app", "A", "SENSITIVE-VALUE")
			if err == nil || strings.Contains(err.Error(), "SENSITIVE-VALUE") {
				t.Fatal("failure must be redacted")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("classification: %v", err)
			}
		})
	}
	for _, reply := range []string{"", `null`, `{}`, `false`, `"SENSITIVE-VALUE"`, `not-json SENSITIVE-VALUE`} {
		t.Run("invalid "+reply, func(t *testing.T) {
			s := rpcStore(t, "bk_vault_set", reply, nil)
			err := s.Set("app", "A", "SENSITIVE-VALUE")
			if err == nil || strings.Contains(err.Error(), "SENSITIVE-VALUE") {
				t.Fatal("unexpected write success or disclosure")
			}
		})
	}
}

func TestNativeVaultNullListsFailClosed(t *testing.T) {
	s := rpcStore(t, "bk_vault_list", "null", nil)
	if _, err := s.List("app"); err == nil {
		t.Fatal("null export response must fail")
	}
	s = rpcStore(t, "bk_vault_list_keys", "null", nil)
	if _, err := s.ListKeys("app"); err == nil {
		t.Fatal("null key response must fail")
	}
}
