package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"pkg.blksails.net/bk/internal/vault"
)

type fakeProcessRunner struct {
	calls   int
	name    string
	args    []string
	options processOptions
	err     error
}

func (f *fakeProcessRunner) Run(_ context.Context, name string, args []string, options processOptions) error {
	f.calls++
	f.name = name
	f.args = append([]string(nil), args...)
	f.options = options
	return f.err
}

func envMap(entries []string) map[string]string {
	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}

func TestRunVaultRunInjectsSecretsAndPreservesProcessOptions(t *testing.T) {
	lister := &fakeVaultListerFull{secrets: []vault.Secret{
		{App: "domain-ops", Key: "CDNCERT_EMAIL", Value: "cipher-email"},
		{App: "domain-ops", Key: "CDNCERT_TOKEN", Value: "cipher-token"},
	}}
	decrypt := mapDecrypt(map[string]string{
		"cipher-email": "admin@example.com",
		"cipher-token": "secret-token",
	})
	stdin := strings.NewReader("input")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	runner := &fakeProcessRunner{}
	options := processOptions{
		Env:    []string{"KEEP=parent", "CDNCERT_TOKEN=old"},
		Dir:    "/tmp/work",
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	}

	err := runVaultRun(context.Background(), "domain-ops", []string{"cdncert", "-domain", "example.com"}, []byte("key"), lister, decrypt, options, runner)
	if err != nil {
		t.Fatalf("runVaultRun returned error: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
	if runner.name != "cdncert" {
		t.Fatalf("runner name = %q, want cdncert", runner.name)
	}
	if got := strings.Join(runner.args, "|"); got != "-domain|example.com" {
		t.Fatalf("runner args = %q", got)
	}
	gotEnv := envMap(runner.options.Env)
	if gotEnv["KEEP"] != "parent" || gotEnv["CDNCERT_EMAIL"] != "admin@example.com" || gotEnv["CDNCERT_TOKEN"] != "secret-token" {
		t.Fatalf("unexpected merged env: %#v", gotEnv)
	}
	if runner.options.Dir != options.Dir || runner.options.Stdin != stdin || runner.options.Stdout != stdout || runner.options.Stderr != stderr {
		t.Fatal("process options were not preserved")
	}
}

func TestRunVaultRunDecryptFailureNeverStartsProcessOrLeaksPlaintext(t *testing.T) {
	lister := &fakeVaultListerFull{secrets: []vault.Secret{
		{App: "domain-ops", Key: "FIRST", Value: "cipher-first"},
		{App: "domain-ops", Key: "SECOND", Value: "tampered"},
	}}
	decrypt := mapDecrypt(map[string]string{"cipher-first": "already-decrypted-secret"})
	runner := &fakeProcessRunner{}

	err := runVaultRun(context.Background(), "domain-ops", []string{"true"}, []byte("key"), lister, decrypt, processOptions{}, runner)
	if err == nil {
		t.Fatal("expected decrypt error")
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.calls)
	}
	if strings.Contains(err.Error(), "already-decrypted-secret") {
		t.Fatalf("error leaked plaintext: %v", err)
	}
}

func TestRunVaultRunListFailureNeverStartsProcess(t *testing.T) {
	wantErr := errors.New("list failed")
	lister := &fakeVaultListerFull{err: wantErr}
	runner := &fakeProcessRunner{}

	err := runVaultRun(context.Background(), "domain-ops", []string{"true"}, []byte("key"), lister, mapDecrypt(nil), processOptions{}, runner)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapping %v", err, wantErr)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.calls)
	}
}

func TestRunVaultRunRejectsInvalidEnvironmentKeyBeforeStart(t *testing.T) {
	lister := &fakeVaultListerFull{secrets: []vault.Secret{{App: "domain-ops", Key: "NOT-AN-ENV", Value: "cipher"}}}
	runner := &fakeProcessRunner{}

	err := runVaultRun(context.Background(), "domain-ops", []string{"true"}, []byte("key"), lister, mapDecrypt(map[string]string{"cipher": "value"}), processOptions{}, runner)
	if err == nil || !strings.Contains(err.Error(), "环境变量名") {
		t.Fatalf("error = %v, want invalid environment name", err)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.calls)
	}
}

func TestRunVaultRunEmptyVaultStillStartsProcess(t *testing.T) {
	runner := &fakeProcessRunner{}
	options := processOptions{Env: []string{"KEEP=value"}}

	err := runVaultRun(context.Background(), "empty", []string{"env"}, []byte("key"), &fakeVaultListerFull{}, mapDecrypt(nil), options, runner)
	if err != nil {
		t.Fatalf("runVaultRun returned error: %v", err)
	}
	if runner.calls != 1 || runner.name != "env" {
		t.Fatalf("runner = calls:%d name:%q", runner.calls, runner.name)
	}
	if got := envMap(runner.options.Env)["KEEP"]; got != "value" {
		t.Fatalf("KEEP = %q, want value", got)
	}
}

func TestRunVaultRunRequiresCommand(t *testing.T) {
	runner := &fakeProcessRunner{}
	err := runVaultRun(context.Background(), "domain-ops", nil, []byte("key"), &fakeVaultListerFull{}, mapDecrypt(nil), processOptions{}, runner)
	if err == nil {
		t.Fatal("expected missing command error")
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.calls)
	}
}

func TestVaultRunCmdRegistrationAndArguments(t *testing.T) {
	if vaultRunCmd.Args == nil {
		t.Fatal("vaultRunCmd.Args is nil")
	}
	if err := vaultRunCmd.Args(vaultRunCmd, []string{"app", "command"}); err != nil {
		t.Fatalf("valid args rejected: %v", err)
	}
	if err := vaultRunCmd.Args(vaultRunCmd, []string{"app"}); err == nil {
		t.Fatal("missing command accepted")
	}
	var found bool
	for _, command := range vaultCmd.Commands() {
		if command == vaultRunCmd {
			found = true
		}
	}
	if !found {
		t.Fatal("vaultRunCmd not registered on vaultCmd")
	}
}

func TestExecProcessRunnerPreservesChildExitStatus(t *testing.T) {
	err := (execProcessRunner{}).Run(context.Background(), os.Args[0], []string{
		"-test.run=TestVaultRunExitHelperProcess", "--",
	}, processOptions{
		Env:    append(os.Environ(), "BK_VAULT_RUN_EXIT_HELPER=1"),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err == nil {
		t.Fatal("expected child exit error")
	}
	if got := commandExitCode(err); got != 23 {
		t.Fatalf("child exit code = %d, want 23 (error: %v)", got, err)
	}
}

func TestVaultRunExitHelperProcess(t *testing.T) {
	if os.Getenv("BK_VAULT_RUN_EXIT_HELPER") != "1" {
		return
	}
	os.Exit(23)
}

var _ io.Reader = (*strings.Reader)(nil)
