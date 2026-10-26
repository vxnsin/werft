//go:build !unix

package gitx

// Owner is only needed when werft runs as root on Linux.
func Owner(dir string) string { return "" }
