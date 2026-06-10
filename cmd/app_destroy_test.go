/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// app_destroy_test.go 验证 confirmDestroy 二次确认纯逻辑：
// 匹配应用名或肯定词返回 true；不匹配/空输入返回 false；并将含应用名的
// 警示文本写入注入的 io.Writer（design「confirmDestroy」；Requirement 3.1/3.2）。

func TestConfirmDestroyMatches(t *testing.T) {
	cases := []struct {
		name  string
		app   string
		input string
		want  bool
	}{
		{name: "input equals app name", app: "myapp", input: "myapp\n", want: true},
		{name: "input equals app name with surrounding whitespace", app: "myapp", input: "  myapp  \n", want: true},
		{name: "affirmative y", app: "myapp", input: "y\n", want: true},
		{name: "affirmative yes", app: "myapp", input: "yes\n", want: true},
		{name: "affirmative uppercase YES", app: "myapp", input: "YES\n", want: true},
		{name: "affirmative y with whitespace", app: "myapp", input: "  y  \n", want: true},
		{name: "wrong app name", app: "myapp", input: "otherapp\n", want: false},
		{name: "explicit no", app: "myapp", input: "n\n", want: false},
		{name: "explicit negative word", app: "myapp", input: "no\n", want: false},
		{name: "empty input", app: "myapp", input: "\n", want: false},
		{name: "whitespace only input", app: "myapp", input: "   \n", want: false},
		{name: "no newline EOF empty", app: "myapp", input: "", want: false},
		{name: "app-name match must be case sensitive", app: "MyApp", input: "myapp\n", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			got, err := confirmDestroy(strings.NewReader(tc.input), &out, tc.app)
			if err != nil {
				t.Fatalf("confirmDestroy returned unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("confirmDestroy(%q, app=%q) = %v, want %v", tc.input, tc.app, got, tc.want)
			}
		})
	}
}

func TestConfirmDestroyWritesWarningNamingApp(t *testing.T) {
	var out bytes.Buffer
	const app = "production-db"

	if _, err := confirmDestroy(strings.NewReader("n\n"), &out, app); err != nil {
		t.Fatalf("confirmDestroy returned unexpected error: %v", err)
	}

	written := out.String()
	if !strings.Contains(written, app) {
		t.Fatalf("expected warning output to name the app %q, got: %q", app, written)
	}
}

func TestConfirmDestroyNoRemoteSideEffectOnReject(t *testing.T) {
	// confirmDestroy 必须是无副作用纯助手：拒绝时仅返回 false，不触达远端。
	// 这里通过「空 reader / 拒绝输入返回 false 且无 error」间接保证调用方可据此中止。
	var out bytes.Buffer
	got, err := confirmDestroy(strings.NewReader("definitely-not-the-app\n"), &out, "myapp")
	if err != nil {
		t.Fatalf("confirmDestroy returned unexpected error: %v", err)
	}
	if got {
		t.Fatalf("expected confirmDestroy to reject mismatched input, got true")
	}
}

// fakeAppDestroyer 是 appDestroyer 的间谍 fake：记录是否被调用、调用参数，
// 并可注入返回文本/错误，使 runAppDestroy 在不触达真实 SSH/Dokku 的前提下被验证。
type fakeAppDestroyer struct {
	called  bool
	gotName string
	result  string
	err     error
}

func (f *fakeAppDestroyer) AppsDestroy(_ context.Context, name string) (string, error) {
	f.called = true
	f.gotName = name
	return f.result, f.err
}

// Requirement 3.2：未带 --force 且确认被拒绝时，必须中止销毁、不触达远端、非零退出。
func TestRunAppDestroyNoForceRejectedDoesNotTouchRemote(t *testing.T) {
	fake := &fakeAppDestroyer{result: "should-not-be-written"}
	var out bytes.Buffer

	// 输入既不匹配应用名也非肯定词 → confirmDestroy 返回 false。
	err := runAppDestroy(context.Background(), strings.NewReader("nope\n"), &out, fake, "myapp", false)

	if err == nil {
		t.Fatalf("expected non-nil error (abort → non-zero exit) when confirmation rejected")
	}
	if fake.called {
		t.Fatalf("Requirement 3.2 violated: AppsDestroy must NOT be called when confirmation is rejected")
	}
	if strings.Contains(out.String(), "should-not-be-written") {
		t.Fatalf("aborted destroy must not write remote result, got: %q", out.String())
	}
}

// Requirement 3.3：带 --force 时跳过交互式确认直接销毁；确认助手不应被咨询（stdin 不被读取）。
func TestRunAppDestroyForceSkipsConfirmAndDestroys(t *testing.T) {
	fake := &fakeAppDestroyer{result: "-----> Destroying myapp (including all add-ons)\n"}
	var out bytes.Buffer

	// 注入一个会在被读取时报错的 reader：force 路径绝不能读取它。
	in := &errReader{err: errors.New("stdin must not be read when --force is set")}

	err := runAppDestroy(context.Background(), in, &out, fake, "myapp", true)
	if err != nil {
		t.Fatalf("force destroy should succeed, got error: %v", err)
	}
	if !fake.called {
		t.Fatalf("Requirement 3.3 violated: AppsDestroy must be called when --force is set")
	}
	if fake.gotName != "myapp" {
		t.Fatalf("AppsDestroy called with wrong name: got %q, want %q", fake.gotName, "myapp")
	}
	if !strings.Contains(out.String(), "Destroying myapp") {
		t.Fatalf("Requirement 3.4: success should write the dokku result, got: %q", out.String())
	}
}

// Requirement 3.1/3.4：未带 --force 但确认匹配（输入应用名或 y）时调用销毁并以零退出展示结果。
func TestRunAppDestroyNoForceConfirmedDestroys(t *testing.T) {
	for _, input := range []string{"myapp\n", "y\n"} {
		fake := &fakeAppDestroyer{result: "-----> Destroying myapp\n"}
		var out bytes.Buffer

		err := runAppDestroy(context.Background(), strings.NewReader(input), &out, fake, "myapp", false)
		if err != nil {
			t.Fatalf("confirmed destroy (input %q) should succeed, got error: %v", input, err)
		}
		if !fake.called {
			t.Fatalf("Requirement 3.1: AppsDestroy must be called when confirmation matches (input %q)", input)
		}
		if !strings.Contains(out.String(), "Destroying myapp") {
			t.Fatalf("Requirement 3.4: success should write the dokku result, got: %q", out.String())
		}
	}
}

// Requirement 3.6：目标不存在或被 Dokku 拒绝时，透传 dokku 错误（%w 包装）并非零退出。
func TestRunAppDestroyPropagatesDokkuError(t *testing.T) {
	dokkuErr := errors.New("App ghost-app does not exist")
	fake := &fakeAppDestroyer{err: dokkuErr}
	var out bytes.Buffer

	err := runAppDestroy(context.Background(), strings.NewReader("y\n"), &out, fake, "ghost-app", false)
	if err == nil {
		t.Fatalf("expected non-nil error when AppsDestroy fails")
	}
	if !errors.Is(err, dokkuErr) {
		t.Fatalf("expected dokku error to be wrapped (errors.Is), got: %v", err)
	}
}

// errReader 是一个在被读取时立即返回错误的 io.Reader，用于证明 force 路径不读取 stdin。
type errReader struct{ err error }

func (e *errReader) Read(_ []byte) (int, error) { return 0, e.err }
