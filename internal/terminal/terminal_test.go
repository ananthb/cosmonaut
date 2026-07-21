package terminal

import "testing"

// TestShellQuote exercises every category of input we care about: empty
// strings, plain identifiers, paths with whitespace, and the full set of
// shell-special characters (`'`, `$`, `;`, backtick) that motivated the
// fix. The expectations match POSIX single-quote semantics where the only
// escape is `'\”` for an embedded single quote.
func TestShellQuote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "''"},
		{"plain path", "/home/user/project", "/home/user/project"},
		{"path with space", "/home/user/my project", "'/home/user/my project'"},
		{"path with single quote", "/tmp/it's", `'/tmp/it'\''s'`},
		{"path with dollar", "/tmp/$HOME", "'/tmp/$HOME'"},
		{"path with semicolon", "/tmp/a;rm -rf /", "'/tmp/a;rm -rf /'"},
		{"path with backtick", "/tmp/`whoami`", "'/tmp/`whoami`'"},
		// The blocklist regressions: newline injected a second remote
		// command, leading ~ tilde-expanded, # started a comment.
		{"path with newline", "/tmp/x\nrm -rf ~", "'/tmp/x\nrm -rf ~'"},
		{"path with carriage return", "/tmp/x\rls", "'/tmp/x\rls'"},
		{"leading tilde", "~/project", "'~/project'"},
		{"hash comment", "/tmp/a#b", "'/tmp/a#b'"},
		{"non-ascii", "/tmp/ürlaub", "'/tmp/ürlaub'"},
		{"allowlisted punctuation", "user@host:a,b%c+d=e-f_g.h/i", "user@host:a,b%c+d=e-f_g.h/i"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ShellQuote(tc.in)
			if got != tc.want {
				t.Fatalf("ShellQuote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
