package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestVaultPasswordArgs(t *testing.T) {
	for _, tc := range []struct {
		name             string
		global, password bool
		args             []string
		invalid          bool
	}{
		{"global password", true, true, []string{"PASSWORD"}, false},
		{"app password", false, true, []string{"app", "PASSWORD"}, false},
		{"missing key", true, true, nil, true},
		{"multiple keys", true, true, []string{"A", "B"}, true},
		{"value in args", true, true, []string{"PASSWORD=secret"}, true},
		{"empty key", true, true, []string{""}, true},
		{"ordinary pairs", true, false, []string{"A=1", "B=2"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &cobra.Command{}
			c.Flags().Bool("global", tc.global, "")
			c.Flags().Bool("password", tc.password, "")
			if err := vaultSetArgs(c, tc.args); (err != nil) != tc.invalid {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}

func TestVaultPasswordInput(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		err         error
		writes      int
	}{
		{"password with special characters", " a=b $'秘密 ", nil, 1},
		{"empty", "", nil, 0},
		{"failed read", "partial-secret", errors.New("private error"), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			s := &nativeStore{}
			secret := []byte(tc.value)
			err := runVaultPasswordSet(&out, "global", "PASSWORD", s, func() ([]byte, error) { return secret, tc.err })
			if (err == nil) != (tc.writes == 1) || s.writes != tc.writes {
				t.Fatalf("unexpected result: %v, writes %d", err, s.writes)
			}
			if tc.writes == 1 && s.items[0].Value != tc.value {
				t.Fatal("password changed")
			}
			if tc.value != "" && strings.Contains(out.String(), tc.value) {
				t.Fatal("password leaked to output")
			}
			if err != nil && (strings.Contains(err.Error(), "partial-secret") || strings.Contains(err.Error(), "private error")) {
				t.Fatal("read error leaked input")
			}
			for _, b := range secret {
				if b != 0 {
					t.Fatal("read buffer not cleared")
				}
			}
		})
	}
}

func TestVaultPasswordRejectsNonTerminal(t *testing.T) {
	c := &cobra.Command{}
	c.SetIn(strings.NewReader("secret\n"))
	var out bytes.Buffer
	c.SetErr(&out)
	if _, err := readVaultPassword(c); err == nil {
		t.Fatal("accepted non-terminal input")
	}
	if out.Len() != 0 {
		t.Fatal("prompted for non-terminal input")
	}
}
