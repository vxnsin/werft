package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// werft runs on Linux. On Windows (only for development and tests) a symlink
// needs admin rights, a junction does not. Junctions need absolute targets.
func link(target, path string) error {
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", path, target).CombinedOutput(); err != nil {
		return fmt.Errorf("mklink: %s", out)
	}
	return nil
}

// replace cannot rename over a junction, so it removes the old one first.
func replace(tmp, path string) error {
	os.Remove(path)
	return os.Rename(tmp, path)
}
