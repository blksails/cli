/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

// fakeVaultLister 实现 vaultLister，注入受控 keys/err，使 runVaultList 可在不触达真实
// Supabase 的前提下被验证。
type fakeVaultLister struct {
	keys     []string
	err      error
	gotApp   string
	gotCalls int
}

func (f *fakeVaultLister) ListKeys(app string) ([]string, error) {
	f.gotCalls++
	f.gotApp = app
	if f.err != nil {
		return nil, f.err
	}
	return f.keys, nil
}

// keys 存在：每行一个 key、按返回顺序、绝不含任何 value 内容（R3.1/R3.2/R3.4）。
func TestRunVaultList_KeysPresent(t *testing.T) {
	lister := &fakeVaultLister{keys: []string{"A_KEY", "B_KEY"}}
	var buf bytes.Buffer

	if err := runVaultList(&buf, "myapp", lister); err != nil {
		t.Fatalf("runVaultList returned error: %v", err)
	}
	if lister.gotApp != "myapp" {
		t.Errorf("ListKeys called with app=%q, want %q", lister.gotApp, "myapp")
	}

	// 逐行断言：每行恰为一个 key，顺序与返回一致。
	var lines []string
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	want := []string{"A_KEY", "B_KEY"}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines %q, want %d %q", len(lines), lines, len(want), want)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d = %q, want %q", i, lines[i], w)
		}
	}
}

// 空集合：写出非空友好提示并以零退出（nil error）（R3.3）。
func TestRunVaultList_Empty(t *testing.T) {
	lister := &fakeVaultLister{keys: []string{}}
	var buf bytes.Buffer

	if err := runVaultList(&buf, "emptyapp", lister); err != nil {
		t.Fatalf("runVaultList returned error on empty set: %v", err)
	}
	out := strings.TrimSpace(buf.String())
	if out == "" {
		t.Fatalf("expected a friendly empty hint, got empty output")
	}
}

// lister 错误：返回非 nil（→ 非零退出）。
func TestRunVaultList_ListerError(t *testing.T) {
	wantErr := errors.New("boom")
	lister := &fakeVaultLister{err: wantErr}
	var buf bytes.Buffer

	err := runVaultList(&buf, "myapp", lister)
	if err == nil {
		t.Fatal("expected error from lister failure, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error %v does not wrap underlying %v", err, wantErr)
	}
}

// 命令装配：vaultListCmd 要求恰一个参数（ExactArgs(1)），并 self-register 到 vaultCmd。
func TestVaultListCmd_ExactArgs(t *testing.T) {
	if vaultListCmd.Args == nil {
		t.Fatal("vaultListCmd.Args is nil, want cobra.ExactArgs(1)")
	}
	if err := vaultListCmd.Args(vaultListCmd, []string{"app"}); err != nil {
		t.Errorf("ExactArgs(1) rejected 1 arg: %v", err)
	}
	if err := vaultListCmd.Args(vaultListCmd, []string{}); err == nil {
		t.Error("ExactArgs(1) accepted 0 args, want error")
	}
	if err := vaultListCmd.Args(vaultListCmd, []string{"app", "extra"}); err == nil {
		t.Error("ExactArgs(1) accepted 2 args, want error")
	}

	// 确认已注册到 vaultCmd 命令组。
	var found bool
	for _, c := range vaultCmd.Commands() {
		if c == vaultListCmd {
			found = true
			break
		}
	}
	if !found {
		t.Error("vaultListCmd not registered on vaultCmd")
	}
}
