/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestAppParseKeyValues 覆盖 KEY=VALUE 列表解析：合法多对、缺 '='、空列表、
// 空值 KEY=、值含 '='（按首个 '=' 切分）（Requirement 5.1/5.3）。
func TestAppParseKeyValues(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "valid multi pair",
			in:   []string{"FOO=bar", "BAZ=qux"},
			want: map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name: "empty value KEY=",
			in:   []string{"FOO="},
			want: map[string]string{"FOO": ""},
		},
		{
			name: "value contains equals splits on first",
			in:   []string{"DSN=user=admin;pass=1"},
			want: map[string]string{"DSN": "user=admin;pass=1"},
		},
		{
			name:    "item missing equals",
			in:      []string{"FOO=bar", "INVALID"},
			wantErr: true,
		},
		{
			name:    "empty list",
			in:      []string{},
			wantErr: true,
		},
		{
			name:    "nil list",
			in:      nil,
			wantErr: true,
		},
		{
			name:    "empty key",
			in:      []string{"=bar"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := appParseKeyValues(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result=%v)", got)
				}
				if strings.TrimSpace(err.Error()) == "" {
					t.Fatalf("expected readable error message, got empty")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAppParseProcessCount 覆盖 process=count 解析：合法、非整数、负数、
// 缺 '='、空 process、空 count（Requirement 8.1/8.2）。
func TestAppParseProcessCount(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantProc string
		wantCnt  int
		wantErr  bool
	}{
		{name: "valid", in: "web=2", wantProc: "web", wantCnt: 2},
		{name: "zero count", in: "worker=0", wantProc: "worker", wantCnt: 0},
		{name: "non integer", in: "web=two", wantErr: true},
		{name: "negative", in: "web=-1", wantErr: true},
		{name: "missing equals", in: "web2", wantErr: true},
		{name: "empty process", in: "=2", wantErr: true},
		{name: "empty count", in: "web=", wantErr: true},
		{name: "empty string", in: "", wantErr: true},
		{name: "float count", in: "web=1.5", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proc, cnt, err := appParseProcessCount(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (proc=%q cnt=%d)", proc, cnt)
				}
				if strings.TrimSpace(err.Error()) == "" {
					t.Fatalf("expected readable error message, got empty")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if proc != tc.wantProc || cnt != tc.wantCnt {
				t.Fatalf("got (%q,%d), want (%q,%d)", proc, cnt, tc.wantProc, tc.wantCnt)
			}
		})
	}
}

// TestAppRenderAppsTable 覆盖应用清单渲染：含内容时表头+各应用占行；空清单走友好提示。
func TestAppRenderAppsTable(t *testing.T) {
	t.Run("with apps", func(t *testing.T) {
		var buf bytes.Buffer
		appRenderAppsTable(&buf, []string{"alpha", "beta-app"})
		out := buf.String()
		for _, want := range []string{"App", "alpha", "beta-app"} {
			if !strings.Contains(out, want) {
				t.Fatalf("output missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("empty list", func(t *testing.T) {
		var buf bytes.Buffer
		appRenderAppsTable(&buf, nil)
		out := buf.String()
		if strings.TrimSpace(out) == "" {
			t.Fatalf("empty apps list should produce a friendly message, got empty")
		}
		// 不应渲染数据行表头作为唯一输出而无提示语
		if out == "App\n" {
			t.Fatalf("empty apps list should produce friendly hint, not bare header")
		}
	})
}

// TestAppRenderConfigTable 覆盖环境变量映射渲染：含内容时 KEY/VALUE 对齐展示；空映射走友好提示。
func TestAppRenderConfigTable(t *testing.T) {
	t.Run("with env", func(t *testing.T) {
		var buf bytes.Buffer
		appRenderConfigTable(&buf, map[string]string{"FOO": "bar", "BAZ": "qux"})
		out := buf.String()
		for _, want := range []string{"FOO", "bar", "BAZ", "qux"} {
			if !strings.Contains(out, want) {
				t.Fatalf("output missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("empty map", func(t *testing.T) {
		var buf bytes.Buffer
		appRenderConfigTable(&buf, map[string]string{})
		out := buf.String()
		if strings.TrimSpace(out) == "" {
			t.Fatalf("empty env map should produce a friendly message, got empty")
		}
	})
}

// TestAppRenderTable 覆盖通用对齐表格助手（design Service Interface）。
func TestAppRenderTable(t *testing.T) {
	var buf bytes.Buffer
	appRenderTable(&buf, []string{"K", "V"}, [][]string{{"a", "1"}, {"bb", "22"}})
	out := buf.String()
	for _, want := range []string{"K", "V", "a", "bb", "22"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// TestAppFailMessage 验证失败助手把 error（已含 dokku stderr）写入 stderr writer。
func TestAppFailMessage(t *testing.T) {
	var buf bytes.Buffer
	appFailMessage(&buf, errors.New("dokku apps:create: boom"))
	out := buf.String()
	if !strings.Contains(out, "boom") {
		t.Fatalf("fail message must include underlying error text, got:\n%s", out)
	}
}

// TestAppExitCode 验证失败助手的退出码语义：错误→非零；nil→零。
func TestAppExitCode(t *testing.T) {
	if code := appExitCode(errors.New("x")); code == 0 {
		t.Fatalf("non-nil error must map to non-zero exit code, got %d", code)
	}
	if code := appExitCode(nil); code != 0 {
		t.Fatalf("nil error must map to zero exit code, got %d", code)
	}
}
