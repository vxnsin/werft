package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vxnsin/werft/internal/gitx"
	"github.com/vxnsin/werft/internal/manifest"
	"github.com/vxnsin/werft/internal/rt"
	"github.com/vxnsin/werft/internal/runner"
	"github.com/vxnsin/werft/internal/state"
)

// Tracked repositories are plain git clones without a werft.yml. werft
// fetches, fast-forwards the branch, runs a command and restarts a service.

type TrackOptions struct {
	Dir     string
	Name    string
	After   string // command run in the tree after a pull, e.g. npm ci && npm run build
	Restart string // systemd unit restarted after a pull
}

func (w *Werft) Track(o TrackOptions) error {
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return fmt.Errorf("%s is not a git working tree", dir)
	}
	tree := gitx.OpenTree(dir)
	branch, err := tree.Branch()
	if err != nil {
		return err
	}
	if branch == "HEAD" {
		return fmt.Errorf("%s is not on a branch", dir)
	}
	if _, err := tree.Upstream(); err != nil {
		return fmt.Errorf("branch %s has no upstream, set one with: git -C %s branch -u origin/%s", branch, dir, branch)
	}
	head, err := tree.Head()
	if err != nil {
		return err
	}
	name := o.Name
	if name == "" {
		name = strings.Trim(nonName.ReplaceAllString(strings.ToLower(filepath.Base(dir)), "-"), "-")
	}
	if !manifest.ValidName(name) {
		return fmt.Errorf("%q cannot be a name, pick one with --name", name)
	}
	if w.Store.Exists(name) {
		return fmt.Errorf("%s already exists", name)
	}
	unit := strings.TrimSuffix(o.Restart, ".service")

	now := time.Now().UTC()
	a := &state.App{
		Name:        name,
		Source:      tree.RemoteURL(),
		Channel:     "branch",
		Branch:      branch,
		Version:     short(head),
		Commit:      head,
		Answers:     map[string]string{},
		Secrets:     map[string]string{},
		Runtime:     "git",
		Kind:        "git",
		Dir:         dir,
		After:       o.After,
		Restart:     unit,
		InstalledAt: now,
		UpdatedAt:   now,
	}
	if err := w.Store.Save(a); err != nil {
		return err
	}
	w.auditAfter(o.After)
	st := w.UI.Step("track", dir)
	st.OK(branch + " @ " + short(head))
	w.UI.Note("update with: werft update %s", name)
	w.UI.Note("automatically: werft auto set %s --at 02:00", name)
	return nil
}

func (w *Werft) Untrack(name string) error {
	a, err := w.Store.Load(name)
	if err != nil {
		return err
	}
	if a.Kind != "git" {
		return fmt.Errorf("%s was installed by werft, remove it with: werft uninstall %s", name, name)
	}
	if err := w.removeAuto(name); err != nil {
		return err
	}
	if err := w.Store.Remove(name, false); err != nil {
		return err
	}
	w.UI.Done("untracked", "")
	w.UI.Note("%s stays as it is", a.Dir)
	return nil
}

// updateGit is Update for a tracked repository.
func (w *Werft) updateGit(a *state.App, o UpdateOptions, began time.Time) error {
	tree := gitx.OpenTree(a.Dir)
	st := w.UI.Step("git", "fetch")
	if err := tree.Fetch(); err != nil {
		return w.fail(st, err)
	}
	head, err := tree.Head()
	if err != nil {
		return w.fail(st, err)
	}
	target, err := tree.Upstream()
	if o.Ref != "" {
		target, err = tree.Commit(o.Ref)
	}
	if err != nil {
		return w.fail(st, err)
	}
	if target == head {
		st.Skip("up to date")
		if a.Commit != head {
			// someone pulled by hand; remember where it is now
			a.Commit, a.Version, a.UpdatedAt = head, short(head), time.Now().UTC()
			w.Store.Save(a)
		}
		w.UI.Note("%s is up to date (%s)", a.Name, short(head))
		return nil
	}
	st.OK(short(target))

	count, _ := tree.Count(head, target)
	w.UI.Title(short(head)+" → "+short(target), plural(count, "commit"))
	if o.Check {
		log, _ := tree.Log(head, target, 15)
		for _, l := range log {
			w.UI.Note("%s", l)
		}
		return nil
	}
	if dirty, err := tree.Dirty(); err != nil || dirty {
		if err == nil {
			err = fmt.Errorf("%s has local changes, not pulling", a.Dir)
		}
		return err
	}

	logf, err := w.logFile(a.Name, "update")
	if err != nil {
		return err
	}
	defer logf.Close()
	r := w.gitRunner(a, tree, short(target), short(head), logf)

	st = w.UI.Step("pull", a.Branch)
	if err := tree.FastForward(target); err != nil {
		return w.fail(st, err)
	}
	st.OK("")
	if err := w.afterPull(a, r); err != nil {
		w.UI.Warn("going back to %s", short(head))
		if rerr := w.resetGit(a, tree, r, head); rerr != nil {
			return fmt.Errorf("%w; going back failed as well: %v", err, rerr)
		}
		return fmt.Errorf("%s is running again, the update failed: %w", short(head), err)
	}

	a.PushPrevious(5)
	a.Version, a.Commit, a.UpdatedAt = short(target), target, time.Now().UTC()
	if err := w.Store.Save(a); err != nil {
		return err
	}
	w.UI.Done("done", since(began))
	return nil
}

// rollbackGit puts a tracked repository back on an earlier commit.
func (w *Werft) rollbackGit(a *state.App, to string) error {
	began := time.Now()
	tree := gitx.OpenTree(a.Dir)
	target := ""
	switch {
	case to != "":
		c, err := tree.Commit(to)
		if err != nil {
			return err
		}
		target = c
	case len(a.Previous) > 0:
		target = a.Previous[0].Commit
	default:
		return fmt.Errorf("no previous version is known for %s", a.Name)
	}
	if dirty, err := tree.Dirty(); err != nil || dirty {
		if err == nil {
			err = fmt.Errorf("%s has local changes, not resetting", a.Dir)
		}
		return err
	}
	w.UI.Title(a.Version+" → "+short(target), "rollback")
	logf, err := w.logFile(a.Name, "rollback")
	if err != nil {
		return err
	}
	defer logf.Close()
	if err := w.resetGit(a, tree, w.gitRunner(a, tree, short(target), a.Version, logf), target); err != nil {
		return err
	}
	var rest []state.Release
	for _, p := range a.Previous {
		if p.Commit != target {
			rest = append(rest, p)
		}
	}
	a.Previous = rest
	a.PushPrevious(5)
	a.Version, a.Commit, a.UpdatedAt = short(target), target, time.Now().UTC()
	if err := w.Store.Save(a); err != nil {
		return err
	}
	w.UI.Done("rolled back", since(began))
	return nil
}

func (w *Werft) gitRunner(a *state.App, tree *gitx.Tree, version, previous string, log *os.File) *runner.Runner {
	r := w.runner([]string{
		"WERFT_APP=" + a.Name,
		"WERFT_VERSION=" + version,
		"WERFT_PREVIOUS_VERSION=" + previous,
		"WERFT_RELEASE_DIR=" + a.Dir,
	}, log)
	r.As = tree.User
	return r
}

// afterPull runs the after command and restarts the service.
func (w *Werft) afterPull(a *state.App, r *runner.Runner) error {
	if a.After != "" {
		st := w.UI.Step("after", oneLine(a.After))
		if err := r.Run(a.Dir, a.After); err != nil {
			return w.fail(st, err)
		}
		st.OK("")
	}
	if a.Restart != "" {
		runtime, c := gitService(a)
		st := w.UI.Step("restart", a.Restart+".service")
		if err := runtime.Restart(c); err != nil {
			return w.fail(st, err)
		}
		st.OK("")
	}
	return nil
}

func (w *Werft) resetGit(a *state.App, tree *gitx.Tree, r *runner.Runner, commit string) error {
	st := w.UI.Step("reset", short(commit))
	if err := tree.ResetHard(commit); err != nil {
		return w.fail(st, err)
	}
	st.OK("")
	return w.afterPull(a, r)
}

// gitService gives the restart unit of a tracked repository the shape of a
// systemd runtime, so start, stop, status and logs work as for other apps.
func gitService(a *state.App) (rt.Runtime, rt.Context) {
	m := &manifest.Manifest{}
	m.Runtime.Type = "systemd"
	m.Runtime.Systemd.Unit = a.Restart
	return rt.Systemd{}, rt.Context{App: a.Name, Current: a.Dir, Manifest: m}
}

var errNoService = errors.New("this repository has no service, add one with werft track --restart")
