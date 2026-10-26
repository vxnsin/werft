package gitx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Tree is a normal working tree, for repositories added with werft track.
// When werft runs as root, git runs as the owner of the directory, so root
// never takes over files in someone else's clone.
type Tree struct {
	Dir  string
	User string // run as this user; empty means the current one
}

func OpenTree(dir string) *Tree {
	t := &Tree{Dir: dir}
	if os.Geteuid() == 0 {
		if owner := Owner(dir); owner != "" && owner != "root" {
			t.User = owner
		}
	}
	return t
}

// Command builds a command that runs as the owner of the tree.
func (t *Tree) Command(name string, args ...string) *exec.Cmd {
	if t.User != "" {
		args = append([]string{"-u", t.User, "--", name}, args...)
		name = "runuser"
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = t.Dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	return cmd
}

func (t *Tree) run(args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := t.Command("git", args...)
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

func (t *Tree) Head() (string, error)   { return t.run("rev-parse", "HEAD") }
func (t *Tree) Branch() (string, error) { return t.run("rev-parse", "--abbrev-ref", "HEAD") }
func (t *Tree) Fetch() error            { _, err := t.run("fetch", "--quiet"); return err }

// Upstream is the commit the branch tracks, as of the last fetch.
func (t *Tree) Upstream() (string, error) { return t.run("rev-parse", "@{upstream}") }

func (t *Tree) Commit(ref string) (string, error) {
	out, err := t.run("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil || out == "" {
		return "", fmt.Errorf("%q does not exist in the repository", ref)
	}
	return out, nil
}

func (t *Tree) RemoteURL() string {
	url, _ := t.run("config", "--get", "remote.origin.url")
	return url
}

// Dirty reports changes to tracked files; untracked files do not count.
func (t *Tree) Dirty() (bool, error) {
	out, err := t.run("status", "--porcelain", "--untracked-files=no")
	return out != "", err
}

// FastForward moves the branch to commit, only if that needs no merge.
func (t *Tree) FastForward(commit string) error {
	_, err := t.run("merge", "--ff-only", "--quiet", commit)
	return err
}

func (t *Tree) ResetHard(commit string) error {
	_, err := t.run("reset", "--hard", "--quiet", commit)
	return err
}

func (t *Tree) Count(from, to string) (int, error) {
	out, err := t.run("rev-list", "--count", from+".."+to)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

func (t *Tree) Log(from, to string, max int) ([]string, error) {
	out, err := t.run("log", "--oneline", "--no-decorate", "-n", strconv.Itoa(max), from+".."+to)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}
