// Package release manages the release directories of an app:
//
//	/opt/werft/<name>/releases/<version>   unpacked release
//	/opt/werft/<name>/current              symlink to the running release
//	/opt/werft/<name>/shared/              survives updates
package release

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Layout struct{ Root string }

func (l Layout) AppDir(name string) string   { return filepath.Join(l.Root, name) }
func (l Layout) Current(name string) string  { return filepath.Join(l.AppDir(name), "current") }
func (l Layout) Shared(name string) string   { return filepath.Join(l.AppDir(name), "shared") }
func (l Layout) Releases(name string) string { return filepath.Join(l.AppDir(name), "releases") }

func (l Layout) Dir(name, version string) string {
	return filepath.Join(l.Releases(name), DirName(version))
}

// DirName turns a version into a directory name (branches may contain /).
func DirName(version string) string {
	return strings.NewReplacer("/", "_", "\\", "_").Replace(version)
}

// Switch points current at a release. The new link is created next to it and
// replaces the old one with a rename, so there is never a moment without current.
func (l Layout) Switch(name, version string) error {
	cur := l.Current(name)
	tmp := cur + ".neu"
	os.Remove(tmp)
	if err := link(filepath.Join("releases", DirName(version)), tmp); err != nil {
		return err
	}
	return replace(tmp, cur)
}

// CurrentVersion reads what current points to.
func (l Layout) CurrentVersion(name string) (string, error) {
	target, err := os.Readlink(l.Current(name))
	if err != nil {
		return "", err
	}
	return filepath.Base(target), nil
}

// List returns the release directories, newest first.
func (l Layout) List(name string) ([]string, error) {
	entries, err := os.ReadDir(l.Releases(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type rel struct {
		name string
		mod  int64
	}
	var rs []rel
	for _, e := range entries {
		if info, err := e.Info(); err == nil && e.IsDir() {
			rs = append(rs, rel{e.Name(), info.ModTime().UnixNano()})
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].mod > rs[j].mod })
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.name
	}
	return out, nil
}

// Prune keeps keep releases in total; everything in protect counts and always stays.
func (l Layout) Prune(name string, keep int, protect ...string) error {
	all, err := l.List(name)
	if err != nil {
		return err
	}
	protected := map[string]bool{}
	for _, p := range protect {
		protected[DirName(p)] = true
	}
	free := keep - len(protected)
	for _, r := range all {
		if protected[r] {
			continue
		}
		if free > 0 {
			free--
			continue
		}
		if err := os.RemoveAll(filepath.Join(l.Releases(name), r)); err != nil {
			return err
		}
	}
	return nil
}

// LinkShared replaces the shared paths in a release with links into shared/.
// If shared does not have them yet, the content from the release is the starting point.
func (l Layout) LinkShared(name, version string, paths []string) error {
	rel := l.Dir(name, version)
	for _, p := range paths {
		p = filepath.Clean(filepath.FromSlash(p))
		inRelease := filepath.Join(rel, p)
		inShared := filepath.Join(l.Shared(name), p)
		if _, err := os.Lstat(inShared); errors.Is(err, fs.ErrNotExist) {
			if err := os.MkdirAll(filepath.Dir(inShared), 0o755); err != nil {
				return err
			}
			if _, err := os.Lstat(inRelease); err == nil {
				if err := os.Rename(inRelease, inShared); err != nil {
					return fmt.Errorf("shared %s: %w", p, err)
				}
			} else if err := os.MkdirAll(inShared, 0o755); err != nil {
				return err
			}
		}
		if err := os.RemoveAll(inRelease); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(inRelease), 0o755); err != nil {
			return err
		}
		if err := link(inShared, inRelease); err != nil {
			return fmt.Errorf("shared %s: %w", p, err)
		}
	}
	return nil
}
