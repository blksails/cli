package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchReleasePrefersMirrorWithoutGitHubToken(t *testing.T) {
	var githubHits int
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		githubHits++
		http.Error(w, "unexpected GitHub request", http.StatusInternalServerError)
	}))
	defer github.Close()

	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bk/releases/latest.json" {
			t.Fatalf("path = %q, want /bk/releases/latest.json", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("mirror received Authorization header %q", got)
		}
		io.WriteString(w, `{"tag_name":"v0.1.6","name":"bk v0.1.6","assets":[{"name":"checksums.txt","url":"https://mirror.example/checksums.txt"}]}`)
	}))
	defer mirror.Close()

	rel, err := fetchReleaseFromSources(http.DefaultClient, mirror.URL+"/bk", github.URL, "", "")
	if err != nil {
		t.Fatalf("fetchReleaseFromSources() error = %v", err)
	}
	if rel.TagName != "v0.1.6" || rel.Source != releaseSourceMirror {
		t.Fatalf("release = %#v", rel)
	}
	if githubHits != 0 {
		t.Fatalf("GitHub hit count = %d, want 0", githubHits)
	}
}

func TestFetchReleaseSpecificVersionFromMirror(t *testing.T) {
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bk/releases/v0.1.6/release.json" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		io.WriteString(w, `{"tag_name":"v0.1.6","assets":[]}`)
	}))
	defer mirror.Close()

	rel, err := fetchReleaseFromSources(http.DefaultClient, mirror.URL+"/bk/", "http://github.invalid", "", "v0.1.6")
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v0.1.6" {
		t.Fatalf("tag = %q", rel.TagName)
	}
}

func TestFetchReleaseFallsBackToGitHub(t *testing.T) {
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "mirror unavailable", http.StatusServiceUnavailable)
	}))
	defer mirror.Close()

	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/blksails/cli/releases/latest" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer gh-test" {
			t.Fatalf("Authorization = %q", got)
		}
		io.WriteString(w, `{"tag_name":"v0.1.6","assets":[]}`)
	}))
	defer github.Close()

	rel, err := fetchReleaseFromSources(http.DefaultClient, mirror.URL, github.URL, "gh-test", "")
	if err != nil {
		t.Fatalf("fallback error = %v", err)
	}
	if rel.Source != releaseSourceGitHub {
		t.Fatalf("source = %q", rel.Source)
	}
}

func TestFetchReleaseReportsMirrorFailureWhenGitHubTokenMissing(t *testing.T) {
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer mirror.Close()

	_, err := fetchReleaseFromSources(http.DefaultClient, mirror.URL, "http://github.invalid", "", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "OSS 镜像") || !strings.Contains(got, "GitHub token") {
		t.Fatalf("error = %q", got)
	}
}

func TestDownloadReleaseAssetNeverSendsTokenToMirror(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization leaked to mirror: %q", got)
		}
		io.WriteString(w, "archive")
	}))
	defer server.Close()

	got, err := downloadReleaseAsset(http.DefaultClient, releaseSourceMirror, "gh-secret", server.URL+"/asset")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "archive" {
		t.Fatalf("body = %q", got)
	}
}

func TestNormalizeMirrorURL(t *testing.T) {
	tests := map[string]string{
		"https://example.com/bk/": "https://example.com/bk",
		" off ":                   "",
		"":                        "",
	}
	for input, want := range tests {
		got, err := normalizeMirrorURL(input)
		if err != nil {
			t.Fatalf("normalizeMirrorURL(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeMirrorURL(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := normalizeMirrorURL("ftp://example.com/bk"); err == nil {
		t.Fatal("expected unsupported scheme error")
	}
}
