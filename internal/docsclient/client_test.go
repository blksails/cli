package docsclient

import (
	"bytes"
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

func TestSheetMethodsUseV1Endpoints(t *testing.T) {
	var requests []struct {
		method string
		path   string
		body   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		requests = append(requests, struct {
			method string
			path   string
			body   string
		}{r.Method, r.URL.RequestURI(), body.String()})
		data := any(nil)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/files/file$1/sheets":
			data = map[string]any{"sheets": []map[string]any{{"sheet_id": "s1", "title": "工作表1", "row_count": 20, "column_count": 5}}}
		case r.Method == http.MethodGet:
			data = map[string]any{"range": "A1:B1", "values": [][]string{{"a", "b"}}}
		case r.Method == http.MethodPut:
			data = map[string]any{"updated_rows": 1, "updated_cells": 2}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/files/file$1/sheets":
			data = map[string]any{"sheet_id": "s2", "title": "新增", "row_count": 10, "column_count": 3}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "test-token")

	if _, err := c.ListSheets(context.Background(), "file$1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadRange(context.Background(), "file$1", "s/1", "A1:B1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WriteRange(context.Background(), "file$1", "s/1", "A2:B2", [][]string{{"c", "d"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddSheet(context.Background(), "file$1", "新增", 10, 3); err != nil {
		t.Fatal(err)
	}
	if err := c.ClearRange(context.Background(), "file$1", "s/1", "A1:B2"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteDimension(context.Background(), "file$1", "s/1", "ROWS", 2, 3); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSheet(context.Background(), "file$1", "s/1"); err != nil {
		t.Fatal(err)
	}

	if got := requests[1].path; got != "/api/v1/files/file$1/sheets/s%2F1/values?range=A1%3AB1" {
		t.Fatalf("read path = %q", got)
	}
	if got := requests[2].body; got != `{"range":"A2:B2","values":[["c","d"]]}` {
		t.Fatalf("write body = %q", got)
	}
	if got := requests[5].body; got != `{"dimension":"ROWS","end":3,"start":2}` {
		t.Fatalf("delete dimension body = %q", got)
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
