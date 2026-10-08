package docsclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListSendsBearerAndOptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		if r.URL.Path != "/api/v1/files" || r.URL.Query().Get("recursive") != "true" || r.URL.Query().Get("q") != "roadmap" {
			t.Fatalf("unexpected URL: %s", r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"items": []map[string]string{{"id": "1", "title": "Roadmap", "type": "doc"}}}})
	}))
	defer srv.Close()
	c, err := New(srv.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.List(context.Background(), "", ListOptions{Query: "roadmap", Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].Title != "Roadmap" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestUnauthorizedDoesNotLeakToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusUnauthorized) }))
	defer srv.Close()
	c, _ := New(srv.URL, "secret-token")
	_, err := c.Status(context.Background())
	if err == nil || err.Error() != "docs: 登录已失效，请运行 `bk auth login`" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewRejectsInvalidEndpoint(t *testing.T) {
	if _, err := New("file:///tmp/socket", "token"); err == nil {
		t.Fatal("expected invalid endpoint error")
	}
}
