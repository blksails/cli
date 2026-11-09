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

func TestCompanyOwnershipIsExplicitAndNeverSendsIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["ownership"] != "company" || body["app"] != "global" {
			t.Errorf("unexpected request: %v", body)
		}
		for _, field := range []string{"scope", "company_id", "user_id", "owner"} {
			if _, present := body[field]; present {
				t.Errorf("caller supplied identity: %s", field)
			}
		}
		if r.Header.Get("Authorization") != "Bearer session" {
			t.Error("missing session")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":["KEY"]}`))
	}))
	defer server.Close()
	store, err := NewScopedStore(server.URL, "session", "company")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := store.ListKeys("global")
	if err != nil || len(keys) != 1 || keys[0] != "KEY" {
		t.Fatalf("keys: %v %v", keys, err)
	}
	if _, err := NewScopedStore(server.URL, "session", "system"); err == nil {
		t.Fatal("invalid ownership accepted")
	}
}

func TestShareRequestsSelectedKeysWithoutReadingPlaintext(t *testing.T) {
	s := testStore(t, "share", `{"data":2}`, 200, func(body map[string]interface{}) {
		keys, ok := body["keys"].([]interface{})
		if !ok || len(keys) != 2 || keys[0] != "A" || keys[1] != "B" || body["app"] != "global" {
			t.Error("wrong share payload")
		}
		if _, ok := body["ownership"]; ok {
			t.Error("share must originate from personal ownership")
		}
	})
	count, err := s.Share("global", []string{"A", "B"})
	if err != nil || count != 2 {
		t.Fatalf("share result: %d %v", count, err)
	}
	conflict := testStore(t, "share", `private-response`, 409, nil)
	if _, err := conflict.Share("global", []string{"A"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict: %v", err)
	}
}

func TestCompanyNamespaceRequests(t *testing.T) {
	for _, operation := range []string{"set", "share"} {
		t.Run(operation, func(t *testing.T) {
			reply := `{"data":true}`
			if operation == "share" {
				reply = `{"data":1}`
			}
			s := testStore(t, operation, reply, 200, func(body map[string]interface{}) {
				if body["company_namespace"] != "blacksail" {
					t.Errorf("namespace = %v", body["company_namespace"])
				}
			})
			if err := s.SetCompanyNamespace("blacksail"); err != nil {
				t.Fatal(err)
			}
			if operation == "share" {
				_, err := s.Share("global", []string{"KEY"})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				s.ownership = "company"
				if err := s.Set("global", "KEY", "value"); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestInvalidCompanyNamespace(t *testing.T) {
	for _, namespace := range []string{" ", "../other", "a/b", strings.Repeat("a", 64)} {
		s := &Store{}
		if err := s.SetCompanyNamespace(namespace); err == nil {
			t.Errorf("accepted %q", namespace)
		}
	}
}
