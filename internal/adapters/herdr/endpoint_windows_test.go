//go:build windows

package herdr

import "testing"

// TestEndpoint covers the Windows rewrite that keeps a plain
// HERDR_SOCKET_PATH usable: herdr names its pipe after the path, the client
// library dials the value verbatim, so the pipe namespace has to be added.
func TestEndpoint(t *testing.T) {
	cases := []struct{ in, want string }{
		{`C:\Users\me\AppData\Roaming\herdr\herdr.sock`, `\\.\pipe\C:\Users\me\AppData\Roaming\herdr\herdr.sock`},
		{`C:\Users\me\AppData\Roaming\herdr\sessions\work 2\herdr.sock`, `\\.\pipe\C:\Users\me\AppData\Roaming\herdr\sessions\work 2\herdr.sock`},
		{`\\.\pipe\C:\Users\me\AppData\Roaming\herdr\herdr.sock`, `\\.\pipe\C:\Users\me\AppData\Roaming\herdr\herdr.sock`},
		{`\\?\pipe\C:\Users\me\herdr.sock`, `\\?\pipe\C:\Users\me\herdr.sock`},
		{"", ""},
	}
	for _, c := range cases {
		if got := endpoint(c.in); got != c.want {
			t.Errorf("endpoint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
