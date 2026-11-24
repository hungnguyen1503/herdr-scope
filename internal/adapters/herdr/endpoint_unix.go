//go:build !windows

package herdr

// endpoint is the socket path itself on Unix: Herdr listens on a Unix domain
// socket there, so the API client dials the value verbatim.
func endpoint(path string) string { return path }
