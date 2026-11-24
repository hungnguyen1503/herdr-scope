//go:build windows

package herdr

import "strings"

// endpoint converts Herdr's socket path into the name the API client dials.
//
// Herdr's Windows endpoint is a named pipe whose name is the socket path
// itself — \\.\pipe\C:\Users\…\AppData\Roaming\herdr\herdr.sock — while
// HERDR_SOCKET_PATH carries the plain file-system path. The client library
// (github.com/cyperx84/herdr-api) hands the value straight to
// winio.DialPipeContext, which rejects a bare path with ERROR_INVALID_PARAMETER
// ("The parameter is incorrect"). Prefix the pipe namespace here, at the one
// place the transport is chosen, so every caller keeps using the path Herdr
// reports.
func endpoint(path string) string {
	if path == "" || strings.HasPrefix(path, `\\.\pipe\`) || strings.HasPrefix(path, `\\?\pipe\`) {
		return path
	}
	return `\\.\pipe\` + path
}
