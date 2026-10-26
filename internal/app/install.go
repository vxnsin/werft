package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vxnsin/werft/internal/gitx"
	"github.com/vxnsin/werft/internal/lock"
	"github.com/vxnsin/werft/internal/manifest"
	"github.com/vxnsin/werft/internal/release"
	"github.com/vxnsin/werft/internal/rt"
	"github.com/vxnsin/werft/internal/semver"
	"github.com/vxnsin/werft/internal/state"
)

type InstallOptions struct {
	Source  string            // git URL or path
	Ref     string            // tag, branch or commit; empty: newest release
	Name    string            // a different name than the one in werft.yml
	Answers map[string]string // from --answers
}

func (w *Werft) Install(o InstallOptions) error {
	began := time.Now()
	w.held = nil
	if err := os.MkdirAll(filepath.Join(w.Store.Home, "apps"), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(w.Store.Home, ".clone-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	st := w.UI.Step("git", shortSource(o.Source))
	repo, err := gitx.Mirror(o.Source, filepath.Join(tmp, "repo.git"))
	if err != nil {
		return w.fail(st, err)
	}
	ref, err := firstRef(repo, o.Ref)
	if err != nil {
		return w.fail(st, err)
	}
	commit, err := repo.Commit(ref)
	if err != nil {
		return w.fail(st, err)
	}
	m, err := readManifest(repo, commit)
	if err != nil {
		return w.fail(st, err)
	}
	if err := problemsErr(m.Validate()); err != nil {
		return w.fail(st, err)
	}
	if m.Source.VerifySignature && isTag(repo, ref) {
		if err := repo.VerifyTag(ref); err != nil {
			return w.fail(st, err)
		}
	}
	version := versionFor(repo, ref, commit)
	st.OK(version)

	name := o.Name
	if name == "" {
		name = m.Name
	}
	if w.Store.Exists(name) {
		return fmt.Errorf("%s is already installed, update it with: werft update %s", name, name)
	}
	w.UI.Title(name+" "+version, m.Description)

	st = w.UI.Step("check", "requirements")
	if err := w.checker().Check(m.Requires, true); err != nil {
		return w.fail(st, err)
	}
	st.OK("")

	if err := w.gate(w.review(m, repoReader{repo, commit}), false, "audit "+name); err != nil {
		return err
	}

	answers, err := w.asker(name, o.Answers).Ask(m.Questions, nil)
	if err != nil {
		return err
	}
	if len(m.Questions) > 0 && w.Interactive {
		fmt.Fprintln(w.UI.Out)
	}

	lk, err := lock.Acquire(w.Store.AppDir(name), name)
	if err != nil {
		return err
	}
	defer lk.Release()
	if err := os.Rename(filepath.Join(tmp, "repo.git"), w.Store.RepoDir(name)); err != nil {
		return err
	}
	repo = gitx.Open(w.Store.RepoDir(name))

	// If anything fails, nothing half-done is left behind.
	done, configWritten := false, false
	defer func() {
		if done {
			return
		}
		w.UI.Note("installation aborted, werft is cleaning up")
		if runtime, err := rt.For(m.Runtime.Type); err == nil {
			runtime.Remove(w.rtContext(name, m, w.data(name, version, answers)))
		}
		if configWritten {
			removeConfig(m)
		}
		os.RemoveAll(w.Layout.AppDir(name))
		w.Store.Remove(name, false)
	}()

	logf, err := w.logFile(name, "install")
	if err != nil {
		return err
	}
	defer logf.Close()
	r := w.runner(w.env(name, version, "", m, answers), logf)

	if err := w.build(name, version, commit, repo, m, r, true); err != nil {
		return err
	}
	if label := configLabel(m); label != "" {
		st := w.UI.Step("config", label)
		configWritten = true
		if err := w.writeConfig(name, version, m, answers); err != nil {
			return w.fail(st, err)
		}
		st.OK("")
	}
	// A fresh install has to set up the current state by itself;
	// existing migrations count as done.
	applied, err := w.migrations(name, version, m)
	if err != nil {
		return err
	}
	if err := w.start(name, version, m, answers, true, r); err != nil {
		return err
	}
	if m.Hooks.PostInstall != "" {
		st := w.UI.Step("hook", m.Hooks.PostInstall)
		if err := r.Script(w.Layout.Dir(name, version), m.Hooks.PostInstall); err != nil {
			return w.fail(st, err)
		}
		st.OK("")
	}

	plain, secret := splitSecrets(m, answers)
	now := time.Now().UTC()
	a := &state.App{
		Name:        name,
		Source:      o.Source,
		Channel:     m.Source.Channel,
		Branch:      branchFor(repo, m, o.Ref),
		Version:     version,
		Commit:      commit,
		Answers:     plain,
		Secrets:     secret,
		Migrations:  applied,
		Runtime:     m.Runtime.Type,
		Warden:      w.held,
		InstalledAt: now,
		UpdatedAt:   now,
	}
	if err := w.Store.Save(a); err != nil {
		return err
	}
	done = true

	w.UI.Done("installed", since(began))
	return nil
}

// firstRef decides what gets installed: what was asked for, otherwise the
// newest release tag or, with channel: branch, the branch.
func firstRef(repo *gitx.Repo, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	head, err := repo.DefaultBranch()
	if err != nil {
		return "", err
	}
	m, err := readManifest(repo, head)
	if err != nil {
		return "", fmt.Errorf("%w (on %s)", err, head)
	}
	if m.Source.Channel == "branch" {
		return m.Source.Branch, nil
	}
	tags, err := repo.Tags()
	if err != nil {
		return "", err
	}
	latest, ok := semver.Latest(tags, "", "major", false)
	if !ok {
		return "", fmt.Errorf("no release tags like v1.0.0 found; install with --ref %s or set channel: branch", head)
	}
	return latest, nil
}

func isTag(repo *gitx.Repo, ref string) bool {
	_, err := repo.Commit("refs/tags/" + ref)
	return err == nil
}

// versionFor names a release: tags keep their name, anything else gets the
// commit appended (main-a1b2c3d).
func versionFor(repo *gitx.Repo, ref, commit string) string {
	if isTag(repo, ref) {
		return ref
	}
	if strings.HasPrefix(commit, ref) {
		return short(commit)
	}
	return release.DirName(ref) + "-" + short(commit)
}

// branchFor remembers the branch that updates follow.
func branchFor(repo *gitx.Repo, m *manifest.Manifest, ref string) string {
	if ref != "" {
		if _, err := repo.Commit("refs/heads/" + ref); err == nil {
			return ref
		}
	}
	return m.Source.Branch
}

func configLabel(m *manifest.Manifest) string {
	if m.Config.EnvFile != "" {
		return m.Config.EnvFile
	}
	if len(m.Config.Templates) > 0 {
		return m.Config.Templates[0].Dest
	}
	return ""
}

func removeConfig(m *manifest.Manifest) {
	if m.Config.EnvFile != "" {
		os.Remove(m.Config.EnvFile)
	}
	for _, t := range m.Config.Templates {
		os.Remove(t.Dest)
	}
}

func shortSource(src string) string {
	if _, err := os.Stat(src); err == nil {
		return filepath.Base(src)
	}
	s := strings.TrimSuffix(src, ".git")
	for _, p := range []string{"https://", "http://", "ssh://", "git@"} {
		s = strings.TrimPrefix(s, p)
	}
	return strings.Replace(s, ":", "/", 1)
}
