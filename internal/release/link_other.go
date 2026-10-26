//go:build !windows

package release

import "os"

func link(target, path string) error { return os.Symlink(target, path) }

// replace swaps the old link in one step.
func replace(tmp, path string) error { return os.Rename(tmp, path) }
