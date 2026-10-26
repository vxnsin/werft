package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vxnsin/werft/internal/backup"
	"github.com/vxnsin/werft/internal/gitx"
	"github.com/vxnsin/werft/internal/lock"
	"github.com/vxnsin/werft/internal/manifest"
	"github.com/vxnsin/werft/internal/rt"
	"github.com/vxnsin/werft/internal/semver"
	"github.com/vxnsin/werft/internal/state"
)

type UpdateOptions struct {
	Ref     string            // take exactly this ref
	Check   bool              // only look
	Auto    bool              // from the timer: policy from werft.yml, no questions
	Policy  string            // patch, minor or major; default for manual runs: major
	Answers map[string]string // for new questions
}

// ErrAutoSkipped: an automatic update was skipped on purpose.
var ErrAutoSkipped = errors.New("automatic update skipped")

func (w *Werft) UpdateAll(o UpdateOptions) error {
	apps, err := w.Store.List()
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		w.UI.Printf("Nothing installed yet.\n")
		return nil
	}
	failed := 0
	for _, a := range apps {
		w.UI.Printf("\n%s\n", a.Name)
		if err := w.Update(a.Name, o); err != nil {
			w.UI.Error(err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d updates failed", failed, len(apps))
	}
	return nil
}

func (w *Werft) Update(name string, o UpdateOptions) error {
	began := time.Now()
	if !w.Store.Exists(name) {
		return fmt.Errorf("%s is %w", name, state.ErrNotInstalled)
	}
	lk, err := lock.Acquire(w.Store.AppDir(name), name)
	if err != nil {
		return err
	}
	defer lk.Release()
	a, err := w.Store.Load(name)
	if err != nil {
		return err
	}
	if a.Kind == "git" {
		return w.updateGit(a, o, began)
	}
	oldM, err := w.releaseManifest(name, a.Version)
	if err != nil {
		w.UI.Warn("cannot read werft.yml of %s: %v", a.Version, err)
		oldM = nil
	}
	repo := gitx.Open(w.Store.RepoDir(name))

	st := w.UI.Step("git", "fetch")
	if err := repo.Fetch(); err != nil {
		return w.fail(st, err)
	}
	ref, version, commit, err := w.target(repo, a, oldM, o)
	if err != nil {
		return w.fail(st, err)
	}
	if ref == "" {
		st.Skip("up to date")
		w.UI.Note("%s is up to date (%s)", name, a.Version)
		return nil
	}
	st.OK(version)

	count, _ := repo.Count(a.Commit, commit)
	w.UI.Title(a.Version+" → "+version, plural(count, "commit"))
	if o.Check {
		log, _ := repo.Log(a.Commit, commit, 15)
		for _, l := range log {
			w.UI.Note("%s", l)
		}
		return nil
	}

	newM, err := readManifest(repo, commit)
	if err != nil {
		return err
	}
	if err := problemsErr(newM.Validate()); err != nil {
		return err
	}
	if newM.Source.VerifySignature && isTag(repo, ref) {
		if err := repo.VerifyTag(ref); err != nil {
			return err
		}
	}

	if err := w.gate(w.review(newM, repoReader{repo, commit}), o.Auto, "audit "+name); err != nil {
		return err
	}

	// New or changed commands never run without asking.
	if changes := w.commandChanges(name, a.Version, oldM, newM, repo, commit); len(changes) > 0 {
		w.UI.Printf("  These commands are new or changed:\n")
		for _, c := range changes {
			w.UI.Printf("    %s\n", c)
		}
		if o.Auto {
			return fmt.Errorf("%w: commands changed, run it by hand: werft update %s", ErrAutoSkipped, name)
		}
		if !w.confirm("Run them?") {
			return errors.New("aborted")
		}
		fmt.Fprintln(w.UI.Out)
	}

	known := a.All()
	asker := w.asker(o.Answers)
	if o.Auto {
		asker.Interactive = false
	}
	answers, err := asker.Ask(newM.Questions, known)
	if err != nil {
		return err
	}
	// Old answers stay so a rollback can still fill its templates.
	for k, v := range known {
		if _, ok := answers[k]; !ok {
			answers[k] = v
		}
	}

	st = w.UI.Step("check", "requirements")
	if err := w.checker().Check(newM.Requires, false); err != nil {
		return w.fail(st, err)
	}
	st.OK("")

	logf, err := w.logFile(name, "update")
	if err != nil {
		return err
	}
	defer logf.Close()
	r := w.runner(w.env(name, version, a.Version, newM, answers), logf)

	if err := w.build(name, version, commit, repo, newM, r, false); err != nil {
		os.RemoveAll(w.Layout.Dir(name, version))
		return err
	}

	// From here on the running version is touched.
	if paths := newM.Update.Backup.Paths; len(paths) > 0 {
		data := w.data(name, a.Version, answers)
		rendered := make([]string, 0, len(paths))
		for _, p := range paths {
			rp, err := manifest.Render(p, data)
			if err != nil {
				return fmt.Errorf("update.backup.paths: %w", err)
			}
			rendered = append(rendered, rp)
		}
		st := w.UI.Step("backup", strings.Join(rendered, " "))
		file, err := backup.Create(w.Store.BackupDir(name), a.Version, rendered)
		if err != nil {
			return w.fail(st, err)
		}
		if file == "" {
			st.Skip("nothing there")
		} else {
			st.OK("")
		}
	}

	rollback := func(cause error) error {
		if !newM.Health.Rollback() {
			return cause
		}
		w.UI.Warn("update to %s failed, going back to %s", version, a.Version)
		if err := w.revert(name, a.Version, oldM, known); err != nil {
			return fmt.Errorf("%w; the rollback failed as well: %v", cause, err)
		}
		return fmt.Errorf("%s is running again, the update failed: %w", a.Version, cause)
	}

	if label := configLabel(newM); label != "" {
		st := w.UI.Step("config", label)
		if err := w.writeConfig(name, version, newM, answers); err != nil {
			return rollback(w.fail(st, err))
		}
		st.OK("")
	}
	applied, err := w.migrate(name, version, newM, r, a.Migrations)
	if err != nil {
		return rollback(err)
	}
	if a.Runtime != "" && a.Runtime != newM.Runtime.Type && oldM != nil {
		if old, err := rt.For(a.Runtime); err == nil {
			old.Remove(w.rtContext(name, oldM, w.data(name, a.Version, known)))
		}
	}
	if err := w.start(name, version, newM, answers, newM.Update.Restart == "always", r); err != nil {
		return rollback(err)
	}
	if newM.Hooks.PostUpdate != "" {
		st := w.UI.Step("hook", newM.Hooks.PostUpdate)
		if err := r.Script(w.Layout.Dir(name, version), newM.Hooks.PostUpdate); err != nil {
			// The new version is already running; this is a warning, not a rollback.
			w.fail(st, err)
		} else {
			st.OK("")
		}
	}

	previous := a.Version
	plain, secret := splitSecrets(newM, answers)
	for k := range a.Secrets {
		if v, ok := plain[k]; ok {
			secret[k] = v
			delete(plain, k)
		}
	}
	a.PushPrevious(newM.Update.KeepReleases)
	a.Version, a.Commit, a.UpdatedAt = version, commit, time.Now().UTC()
	a.Answers, a.Secrets, a.Migrations = plain, secret, applied
	a.Channel, a.Runtime = newM.Source.Channel, newM.Runtime.Type
	if err := w.Store.Save(a); err != nil {
		return err
	}

	if err := w.Layout.Prune(name, newM.Update.KeepReleases, version, previous); err != nil {
		w.UI.Warn("old releases not removed: %v", err)
	}
	if err := backup.Prune(w.Store.BackupDir(name), newM.Update.Backup.Keep); err != nil {
		w.UI.Warn("old backups not removed: %v", err)
	}
	w.UI.Done("done", since(began))
	return nil
}

// target finds the ref to update to. An empty ref means: nothing new.
func (w *Werft) target(repo *gitx.Repo, a *state.App, oldM *manifest.Manifest, o UpdateOptions) (ref, version, commit string, err error) {
	switch {
	case o.Ref != "":
		ref = o.Ref
	case a.Channel == "branch":
		ref = a.Branch
		if commit, err = repo.Commit("refs/heads/" + ref); err != nil {
			return "", "", "", err
		}
	default:
		tags, err := repo.Tags()
		if err != nil {
			return "", "", "", err
		}
		policy := "major"
		if o.Auto && a.Auto != nil {
			policy = a.Auto.Policy
		}
		if o.Policy != "" {
			policy = o.Policy
		}
		latest, ok := semver.Latest(tags, a.Version, policy, false)
		if !ok {
			return "", "", "", nil
		}
		ref = latest
	}
	if commit == "" {
		if commit, err = repo.Commit(ref); err != nil {
			return "", "", "", err
		}
	}
	if commit == a.Commit {
		return "", "", "", nil
	}
	return ref, versionFor(repo, ref, commit), commit, nil
}

// revert brings back an older version: configuration, current, restart.
func (w *Werft) revert(name, version string, m *manifest.Manifest, answers map[string]string) error {
	if m == nil {
		var err error
		if m, err = w.releaseManifest(name, version); err != nil {
			return err
		}
	}
	if err := w.writeConfig(name, version, m, answers); err != nil {
		return err
	}
	if err := w.Layout.Switch(name, version); err != nil {
		return err
	}
	runtime, err := rt.For(m.Runtime.Type)
	if err != nil {
		return err
	}
	c := w.rtContext(name, m, w.data(name, version, answers))
	if err := runtime.Apply(c); err != nil {
		return err
	}
	if m.Runtime.Type == "none" {
		return nil
	}
	return runtime.Restart(c)
}

// commandChanges compares everything werft runs between the old and the new version.
func (w *Werft) commandChanges(name, oldVersion string, oldM, newM *manifest.Manifest, repo *gitx.Repo, commit string) []string {
	if oldM == nil {
		return []string{"the old werft.yml is missing, everything counts as new"}
	}
	var out []string
	diffSteps := func(label string, before, after []manifest.Step) {
		had, has := map[string]bool{}, map[string]bool{}
		for _, s := range before {
			had[s.Run] = true
		}
		for _, s := range after {
			has[s.Run] = true
			if !had[s.Run] {
				out = append(out, "+ "+label+": "+oneLine(s.Run))
			}
		}
		for _, s := range before {
			if !has[s.Run] {
				out = append(out, "- "+label+": "+oneLine(s.Run))
			}
		}
	}
	diffSteps("install", oldM.Install, newM.Install)
	diffSteps("update.steps", oldM.Update.Steps, newM.Update.Steps)
	if oldM.Runtime.Systemd.Exec != newM.Runtime.Systemd.Exec && newM.Runtime.Type == "systemd" {
		out = append(out, "~ runtime.systemd.exec: "+newM.Runtime.Systemd.Exec)
	}
	if oldM.Health != nil && newM.Health != nil && oldM.Health.Cmd != newM.Health.Cmd && newM.Health.Cmd != "" {
		out = append(out, "~ health.cmd: "+newM.Health.Cmd)
	}
	oldHooks := hookMap(oldM)
	oldDir := w.Layout.Dir(name, oldVersion)
	for _, h := range hookList(newM) {
		if h.path == "" {
			continue
		}
		after, err := repo.ShowFile(commit, h.path)
		if err != nil {
			out = append(out, "! hooks."+h.name+": "+h.path+" is missing")
			continue
		}
		before, err := os.ReadFile(filepath.Join(oldDir, filepath.FromSlash(oldHooks[h.name])))
		switch {
		case oldHooks[h.name] == "" || err != nil:
			out = append(out, "+ hooks."+h.name+": "+h.path)
		case !bytes.Equal(before, after):
			out = append(out, "~ hooks."+h.name+": "+h.path+" changed")
		}
	}
	return out
}

type hook struct{ name, path string }

func hookList(m *manifest.Manifest) []hook {
	return []hook{
		{"pre_install", m.Hooks.PreInstall}, {"post_install", m.Hooks.PostInstall},
		{"pre_update", m.Hooks.PreUpdate}, {"post_update", m.Hooks.PostUpdate},
		{"pre_uninstall", m.Hooks.PreUninstall},
	}
}

func hookMap(m *manifest.Manifest) map[string]string {
	out := map[string]string{}
	for _, h := range hookList(m) {
		out[h.name] = h.path
	}
	return out
}

func oneLine(s string) string {
	first, rest, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if rest != "" {
		first += " …"
	}
	return first
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
