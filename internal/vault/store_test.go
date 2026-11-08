package vault

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testStore(t *testing.T, operation, reply string, status int, check func(map[string]interface{})) *Store {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/vault" || r.Header.Get("Authorization") != "Bearer session" {
			t.Error("missing authenticated request")
		}
		var body map[string]interface{}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid JSON")
		}
		if body["operation"] != operation {
			t.Error("wrong operation")
		}
		if _, ok := body["owner"]; ok {
			t.Error("client supplied owner")
		}
		if check != nil {
			check(body)
		}
		w.WriteHeader(status)
		w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	s, err := NewStore(server.URL+"/api/vault", "session")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestVaultOperations(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		s := testStore(t, "set", `{"data":true}`, 200, func(b map[string]interface{}) {
			if b["value"] != "secret" || b["app"] != "app" || b["key"] != "KEY" {
				t.Error("wrong input")
			}
		})
		if err := s.Set("app", "KEY", "secret"); err != nil {
			t.Fatal(err)
		}
	})
	for _, value := range []string{"", "secret"} {
		t.Run("get", func(t *testing.T) {
			reply, _ := json.Marshal(map[string]string{"data": value})
			s := testStore(t, "get", string(reply), 200, nil)
			got, err := s.Get("app", "KEY")
			if err != nil || got != value {
				t.Fatalf("get failed: %v", err)
			}
		})
	}
	t.Run("keys", func(t *testing.T) {
		s := testStore(t, "keys", `{"data":["KEY"]}`, 200, nil)
		got, err := s.ListKeys("app")
		if err != nil || len(got) != 1 || got[0] != "KEY" {
			t.Fatal("keys failed")
		}
	})
	t.Run("list", func(t *testing.T) {
		s := testStore(t, "list", `{"data":[{"app":"app","key":"KEY","value":"secret"}]}`, 200, nil)
		got, err := s.List("app")
		if err != nil || len(got) != 1 || got[0].Value != "secret" {
			t.Fatal("list failed")
		}
	})
	t.Run("remove", func(t *testing.T) {
		s := testStore(t, "remove", `{"data":true}`, 200, nil)
		if err := s.Remove("app", "KEY"); err != nil {
			t.Fatal(err)
		}
	})
}
func TestErrorsNeverEchoPayload(t *testing.T) {
	for _, status := range []int{401, 403, 404, 500, 302} {
		s := testStore(t, "get", `secret-should-not-leak`, status, nil)
		_, err := s.Get("app", "KEY")
		if err == nil || strings.Contains(err.Error(), "secret-should-not-leak") {
			t.Fatal("unsafe error")
		}
		if status == 404 && !errors.Is(err, ErrNotFound) {
			t.Fatal("missing not-found mapping")
		}
		if (status == 401 || status == 403) && !errors.Is(err, ErrPermission) {
			t.Fatal("missing auth mapping")
		}
	}
}
func TestRejectsUnsafeEndpointAndInvalidResponse(t *testing.T) {
	for _, endpoint := range []string{"http://example.com/api/vault", "https://user:pass@example.com/api/vault", "https://example.com/api/vault?token=secret"} {
		if _, err := NewStore(endpoint, "session"); err == nil {
			t.Fatal("unsafe endpoint")
		}
	}
	for _, reply := range []string{`null`, `{"data":null}`, `{"data":{"secret":"echo"}}`, `not-json`} {
		s := testStore(t, "get", reply, 200, nil)
		if _, err := s.Get("app", "KEY"); err == nil {
			t.Fatal("invalid response accepted")
		}
	}
}
func TestDoesNotFollowRedirectWithToken(t *testing.T) {
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer source.Close()
	s, _ := NewStore(source.URL, "session")
	if _, err := s.Get("app", "KEY"); err == nil || reached {
		t.Fatal("followed redirect")
	}
}
