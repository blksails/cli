package cmd

import (
	"github.com/spf13/cobra"
	"reflect"
	"testing"
)

func TestVaultGlobalArguments(t *testing.T) {
	for _, tc := range []struct {
		name     string
		global   bool
		args     []string
		min, max int
		invalid  bool
	}{
		{"global list", true, nil, 0, 0, false},
		{"app list", false, []string{"myapp"}, 0, 0, false},
		{"missing app", false, nil, 0, 0, true},
		{"global rejects app", true, []string{"myapp"}, 0, 0, true},
		{"global get", true, []string{"TOKEN"}, 1, 1, false},
		{"app get", false, []string{"myapp", "TOKEN"}, 1, 1, false},
		{"get missing app", false, []string{"TOKEN"}, 1, 1, true},
		{"global get rejects app", true, []string{"myapp", "TOKEN"}, 1, 1, true},
		{"global set", true, []string{"TOKEN=a=b", "A=1"}, 1, -1, false},
		{"global set missing value", true, nil, 1, -1, true},
		{"app set", false, []string{"myapp", "TOKEN=a=b"}, 1, -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &cobra.Command{}
			c.Flags().Bool("global", tc.global, "")
			err := vaultArgs(tc.min, tc.max)(c, tc.args)
			if (err != nil) != tc.invalid {
				t.Fatalf("unexpected error: %v", err)
			}
			if err == nil {
				app, rest := vaultScope(c, tc.args)
				if tc.global {
					if app != "global" || !reflect.DeepEqual(rest, tc.args) {
						t.Fatalf("scope = %q %v", app, rest)
					}
				} else if app != tc.args[0] || !reflect.DeepEqual(rest, tc.args[1:]) {
					t.Fatalf("scope = %q %v", app, rest)
				}
			}
		})
	}
}

func TestVaultRunGlobalAndLegacyParsing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		app     string
		command []string
		invalid bool
	}{
		{"global", []string{"--global", "--", "server", "--port", "8080"}, "global", []string{"server", "--port", "8080"}, false},
		{"app", []string{"myapp", "--", "server", "--port", "8080"}, "myapp", []string{"server", "--port", "8080"}, false},
		{"legacy", []string{"myapp", "server"}, "myapp", []string{"server"}, false},
		{"missing global flag", []string{"--", "server"}, "", nil, true},
		{"global missing command", []string{"--global", "--"}, "", nil, true},
		{"global rejects app", []string{"--global", "myapp", "--", "server"}, "", nil, true},
		{"app missing command", []string{"myapp", "--"}, "", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &cobra.Command{Use: "run", RunE: func(c *cobra.Command, args []string) error {
				app, command, err := vaultRunScope(c, args)
				if (err != nil) != tc.invalid {
					t.Fatalf("unexpected error: %v", err)
				}
				if err == nil && (app != tc.app || !reflect.DeepEqual(command, tc.command)) {
					t.Fatalf("scope = %q %v", app, command)
				}
				return nil
			}}
			c.Flags().Bool("global", false, "")
			c.SetArgs(tc.args)
			if err := c.Execute(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVaultOwnershipFlags(t *testing.T) {
	for _, args := range [][]string{
		{"set", "--share", "--personal", "myapp", "KEY=value"},
		{"set", "--share", "--personal", "--global", "KEY=value"},
	} {
		root := &cobra.Command{Use: "vault"}
		root.PersistentFlags().Bool("share", false, "")
		root.PersistentFlags().Bool("personal", false, "")
		root.MarkFlagsMutuallyExclusive("personal", "share")
		root.PersistentFlags().Bool("global", false, "")
		ran := false
		root.AddCommand(&cobra.Command{Use: "set", Args: vaultArgs(1, -1), Run: func(*cobra.Command, []string) { ran = true }})
		root.SetArgs(args)
		if err := root.Execute(); err == nil || ran {
			t.Fatal("conflicting ownership flags must fail before execution")
		}
	}
}
