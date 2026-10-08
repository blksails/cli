package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/auth"
	"pkg.blksails.net/bk/internal/docsclient"
)

type fakeDocsAPI struct {
	items map[string][]docsclient.File
}

func (f *fakeDocsAPI) List(_ context.Context, folder string, _ docsclient.ListOptions) (*docsclient.ListResult, error) {
	return &docsclient.ListResult{Items: f.items[folder]}, nil
}
func (*fakeDocsAPI) Content(context.Context, string, string) (*docsclient.ContentResult, error) {
	return nil, fmt.Errorf("unused")
}
func (*fakeDocsAPI) Append(context.Context, string, string) error { return fmt.Errorf("unused") }
func (*fakeDocsAPI) Export(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("unused")
}
func (*fakeDocsAPI) Download(context.Context, string, io.Writer) error { return fmt.Errorf("unused") }
func (*fakeDocsAPI) Upload(context.Context, string, string) (*docsclient.UploadResult, error) {
	return nil, fmt.Errorf("unused")
}
func (*fakeDocsAPI) CreateFolder(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("unused")
}
func (*fakeDocsAPI) Status(context.Context) (*docsclient.AuthStatus, error) {
	return nil, fmt.Errorf("unused")
}

func TestDocsCommandRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"docs", "cat"})
	if err != nil || cmd != docsCatCmd {
		t.Fatalf("docs cat not registered: cmd=%v err=%v", cmd, err)
	}
}

func TestDocsProviderFlagRegistered(t *testing.T) {
	if flag := docsCmd.PersistentFlags().Lookup("provider"); flag == nil {
		t.Fatal("docs --provider flag is not registered")
	}
}

func TestResolveDocsProvider(t *testing.T) {
	for _, input := range []string{"", "tdocs", " TDOCS "} {
		got, err := resolveDocsProvider(input)
		if err != nil || got != docsProviderTDocs {
			t.Fatalf("resolveDocsProvider(%q) = %q, %v", input, got, err)
		}
	}
	if _, err := resolveDocsProvider("feishu"); err == nil || !strings.Contains(err.Error(), "当前支持") {
		t.Fatalf("unsupported provider error = %v", err)
	}
}

func TestResolveDocsFileByPath(t *testing.T) {
	api := &fakeDocsAPI{items: map[string][]docsclient.File{
		"":         {{ID: "folder-1", Title: "项目", Type: "folder"}},
		"folder-1": {{ID: "doc-1", Title: "发布计划", Type: "doc"}},
	}}
	got, err := resolveDocsFile(context.Background(), api, "项目/发布计划")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "doc-1" {
		t.Fatalf("id=%q", got.ID)
	}
}

func TestDocsIdentityPrefersJWTClaims(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"jwt-user","app_metadata":{"company_id":"company-7"}}`))
	user, company := docsIdentity(auth.Session{AccessToken: "header." + payload + ".sig", User: auth.User{ID: "stored-user"}})
	if user != "jwt-user" || company != "company-7" {
		t.Fatalf("identity = %q, %q", user, company)
	}
}

func TestResolveDocsFileReportsAmbiguousTitle(t *testing.T) {
	api := &fakeDocsAPI{items: map[string][]docsclient.File{"": {
		{ID: "1", Title: "项目计划 A", Type: "doc"}, {ID: "2", Title: "项目计划 B", Type: "doc"},
	}}}
	_, err := resolveDocsFile(context.Background(), api, "项目计划")
	if err == nil || !strings.Contains(err.Error(), "匹配到多个") {
		t.Fatalf("unexpected error: %v", err)
	}
}
