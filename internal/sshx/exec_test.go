package sshx

import "testing"

func TestShellJoin(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"apps:create", "myapp"}, "apps:create myapp"},
		{[]string{"config:set", "myapp", "KEY=hello world"}, `config:set myapp 'KEY=hello world'`},
		{[]string{"echo", "it's"}, `echo 'it'\''s'`},
		{[]string{"x", ""}, "x ''"},
		{[]string{"a=b/c.d:e"}, "a=b/c.d:e"},
	}
	for _, c := range cases {
		if got := ShellJoin(c.args); got != c.want {
			t.Errorf("ShellJoin(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}
