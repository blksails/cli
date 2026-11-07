package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"pkg.blksails.net/bk/internal/vault"
	"strings"
	"testing"
)

type nativeStore struct {
	items  []vault.Secret
	err    error
	writes int
}

func (s *nativeStore) Set(app, key, value string) error {
	s.writes++
	if s.err != nil {
		return s.err
	}
	s.items = append(s.items, vault.Secret{App: app, Key: key, Value: value})
	return nil
}
func (s *nativeStore) Get(app, key string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	for _, r := range s.items {
		if r.App == app && r.Key == key {
			return r.Value, nil
		}
	}
	return "", vault.ErrNotFound
}
func (s *nativeStore) List(app string) ([]vault.Secret, error) { return s.items, s.err }

type nativeRunner struct {
	called  bool
	options processOptions
	name    string
	args    []string
}

func (r *nativeRunner) Run(ctx context.Context, name string, args []string, o processOptions) error {
	r.called = true
	r.options = o
	r.name = name
	r.args = args
	return nil
}
func TestNativeVaultSetAndGet(t *testing.T) {
	s := &nativeStore{}
	var out bytes.Buffer
	if err := runVaultSet(&out, "registry", []string{"USER=alice", "PASSWORD=secret=with=equals"}, s); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "secret") {
		t.Fatal("set disclosed plaintext")
	}
	if s.items[1].Value != "secret=with=equals" {
		t.Fatal("must send unchanged value to native Vault")
	}
	out.Reset()
	if err := runVaultGet(&out, "registry", "PASSWORD", s); err != nil {
		t.Fatal(err)
	}
	if out.String() != "secret=with=equals\n" {
		t.Fatal("get output")
	}
}
func TestNativeVaultRejectsInvalidPairsBeforeWriting(t *testing.T) {
	for _, bad := range []string{"invalid-secret", "=secret"} {
		s := &nativeStore{}
		var out bytes.Buffer
		err := runVaultSet(&out, "app", []string{"A=first", bad}, s)
		if err == nil || s.writes != 0 || out.Len() != 0 || strings.Contains(err.Error(), "secret") {
			t.Fatal("invalid input must not write or echo payload")
		}
	}
}
func TestNativeVaultReadFailureHasNoOutput(t *testing.T) {
	s := &nativeStore{err: vault.ErrPermission}
	var out bytes.Buffer
	if err := runVaultGet(&out, "app", "A", s); !errors.Is(err, vault.ErrPermission) || out.Len() != 0 {
		t.Fatal("get failure")
	}
	if err := runVaultExport(&out, "app", s); err == nil || out.Len() != 0 {
		t.Fatal("export failure")
	}
}
func TestNativeVaultExport(t *testing.T) {
	s := &nativeStore{items: []vault.Secret{{Key: "A", Value: "1"}, {Key: "B", Value: "2"}}}
	var out bytes.Buffer
	if err := runVaultExport(&out, "app", s); err != nil || out.String() != "A=1\nB=2\n" {
		t.Fatal("export")
	}
}
func TestNativeVaultRunPreservesInvocationAndHidesSecrets(t *testing.T) {
	s := &nativeStore{items: []vault.Secret{{Key: "TOKEN", Value: "secret"}}}
	r := &nativeRunner{}
	var out bytes.Buffer
	stdin := strings.NewReader("in")
	opts := processOptions{Env: []string{"KEEP=parent", "TOKEN=old"}, Dir: "/tmp", Stdin: stdin, Stdout: &out, Stderr: io.Discard}
	if err := runVaultRun(context.Background(), "app", []string{"docker", "login", "--password-stdin"}, s, opts, r); err != nil {
		t.Fatal(err)
	}
	if !r.called || r.name != "docker" || strings.Join(r.args, " ") != "login --password-stdin" || r.options.Dir != "/tmp" || r.options.Stdin != stdin || out.Len() != 0 {
		t.Fatal("invocation changed or leaked output")
	}
	if strings.Join(r.options.Env, "|") != "KEEP=parent|TOKEN=secret" {
		t.Fatal("environment override")
	}
}
func TestNativeVaultRunFailsClosed(t *testing.T) {
	for _, s := range []*nativeStore{{err: vault.ErrPermission}, {items: []vault.Secret{{Key: "BAD-NAME", Value: "secret"}}}, {items: []vault.Secret{{Key: "TOKEN", Value: "secret\x00"}}}} {
		r := &nativeRunner{}
		err := runVaultRun(context.Background(), "app", []string{"command"}, s, processOptions{}, r)
		if err == nil || r.called || strings.Contains(err.Error(), "secret\x00") {
			t.Fatal("invalid secrets must not start command")
		}
	}
}
