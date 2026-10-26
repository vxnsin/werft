// Package gitx talks to git. Every app has a bare mirror at
// /var/lib/werft/apps/<name>/repo.git; releases are taken from it with git archive.
package gitx

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Repo struct{ Dir string }

func Open(dir string) *Repo { return &Repo{Dir: dir} }

func git(args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	return cmd
}

func run(args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := git(args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimSpace(out.String()), nil
}

func (r *Repo) run(args ...string) (string, error) {
	return run(append([]string{"--git-dir", r.Dir}, args...)...)
}

// Mirror clones url as a mirror into dir.
func Mirror(url, dir string) (*Repo, error) {
	if _, err := run("clone", "--mirror", "--quiet", url, dir); err != nil {
		return nil, err
	}
	return Open(dir), nil
}

func (r *Repo) Fetch() error {
	_, err := r.run("fetch", "--prune", "--quiet", "origin")
	return err
}

func (r *Repo) Tags() ([]string, error) {
	out, err := r.run("tag", "--list")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// DefaultBranch is the branch HEAD points to in the mirror.
func (r *Repo) DefaultBranch() (string, error) {
	return r.run("symbolic-ref", "--short", "HEAD")
}

// Commit resolves a reference (tag, branch, SHA) to a commit.
func (r *Repo) Commit(ref string) (string, error) {
	out, err := r.run("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil || out == "" {
		return "", fmt.Errorf("%q does not exist in the repository", ref)
	}
	return out, nil
}

// ShowFile reads a file at a given ref without checking anything out.
func (r *Repo) ShowFile(ref, path string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := git("--git-dir", r.Dir, "show", ref+":"+path)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s is missing in %s", path, ref)
	}
	return out.Bytes(), nil
}

// Log returns the commits between from and to, newest first.
func (r *Repo) Log(from, to string, max int) ([]string, error) {
	rng := to
	if from != "" {
		rng = from + ".." + to
	}
	out, err := r.run("log", "--oneline", "--no-decorate", "-n", strconv.Itoa(max), rng)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

func (r *Repo) Count(from, to string) (int, error) {
	rng := to
	if from != "" {
		rng = from + ".." + to
	}
	out, err := r.run("rev-list", "--count", rng)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

func (r *Repo) VerifyTag(tag string) error {
	if _, err := r.run("verify-tag", tag); err != nil {
		return fmt.Errorf("cannot verify the signature of %s: %w", tag, err)
	}
	return nil
}

// Export unpacks ref into dest, without .git.
func (r *Repo) Export(ref, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	var errb bytes.Buffer
	cmd := git("--git-dir", r.Dir, "archive", "--format=tar", ref)
	cmd.Stderr = &errb
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	untarErr := untar(stdout, dest)
	io.Copy(io.Discard, stdout)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git archive: %s", strings.TrimSpace(errb.String()))
	}
	return untarErr
}

func untar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(h.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("unsafe path in archive: %s", h.Name)
		}
		target := filepath.Join(dest, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		}
	}
}

// Files lists the entries of a directory at ref, without reading them.
func (r *Repo) Files(ref, dir string) ([]string, error) {
	out, err := r.run("ls-tree", "--name-only", ref+":"+strings.TrimSuffix(dir, "/"))
	if err != nil || out == "" {
		return nil, err
	}
	names := strings.Split(out, "\n")
	for i, n := range names {
		names[i] = strings.TrimPrefix(n, dir+"/")
	}
	return names, nil
}
