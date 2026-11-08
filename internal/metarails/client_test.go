package metarails

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/features/materials/downloadZip" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("unexpected request: %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			Entries []DownloadEntry `json:"entries"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Entries) != 1 || body.Entries[0].ID != 42 || body.Entries[0].Path != "hero.png" {
			t.Fatalf("entries = %#v", body.Entries)
		}
		zw := zip.NewWriter(w)
		entry, _ := zw.Create("hero.png")
		_, _ = entry.Write([]byte("image"))
		_ = zw.Close()
	}))
	defer server.Close()
	c, _ := New(server.URL, "token")
	var out bytes.Buffer
	if err := c.Download(context.Background(), []DownloadEntry{{ID: 42, Path: "hero.png"}}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("empty download")
	}
}

func TestUploadContract(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hero.png")
	if err := os.WriteFile(path, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/materials/upload" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatal("bad request")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("material_type") != "IMAGE" || r.FormValue("folder_id") != "9" || r.FormValue("tags") != `["cover"]` {
			t.Fatalf("form = %#v", r.Form)
		}
		file, header, err := r.FormFile("files")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		got, _ := io.ReadAll(file)
		if header.Filename != "hero.png" || string(got) != "image" {
			t.Fatalf("file=%q data=%q", header.Filename, got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"total":1,"success":1,"fail":0,"results":[{"file_name":"hero.png","material_id":7}]}`)
	}))
	defer server.Close()
	c, _ := New(server.URL, "token")
	result, err := c.Upload(context.Background(), []string{path}, "IMAGE", 9, []string{"cover"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success != 1 || result.Results[0].MaterialID != 7 {
		t.Fatalf("result=%#v", result)
	}
}

func TestResponseErrorExtractsMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"unauthorized","message":"login required"}}`)
	}))
	defer server.Close()
	c, _ := New(server.URL, "token")
	err := c.Download(context.Background(), []DownloadEntry{{ID: 1, Path: "1"}}, io.Discard)
	if err == nil || err.Error() != "download materials: HTTP 401: login required" {
		t.Fatalf("err=%v", err)
	}
}
