package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/vxnsin/werft/internal/gitx"
	"github.com/vxnsin/werft/internal/lock"
	"github.com/vxnsin/werft/internal/manifest"
	"github.com/vxnsin/werft/internal/rt"
	"github.com/vxnsin/werft/internal/state"
)

// Rollback brings back the previous (or a given) version.
// Migrations are not reversed.
func (w *Werft) Rollback(name, to string) error {
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
	target := to
	if target == "" {
		if len(a.Previous) == 0 {
			return fmt.Errorf("no previous version is known for %s", name)
		}
		target = a.Previous[0].Version
	}
	if target == a.Version {
		return fmt.Errorf("%s is already running", target)
	}
	if _, err := os.Stat(w.Layout.Dir(name, target)); err != nil {
		return fmt.Errorf("release %s is gone (update.keep_releases decides how many are kept)", target)
	}
	m, err := w.releaseManifest(name, target)
	if err != nil {
		return err
	}
	w.UI.Title(a.Version+" → "+target, "rollback")

	answers := a.All()
	logf, err := w.logFile(name, "rollback")
	if err != nil {
		return err
	}
	defer logf.Close()
	r := w.runner(w.env(name, target, a.Version, m, answers), logf)

	if label := configLabel(m); label != "" {
		st := w.UI.Step("config", label)
		if err := w.writeConfig(name, target, m, answers); err != nil {
			return w.fail(st, err)
		}
		st.OK("")
	}
	if cur, err := w.releaseManifest(name, a.Version); err == nil && cur.Runtime.Type != m.Runtime.Type {
		if old, err := rt.For(cur.Runtime.Type); err == nil {
			old.Remove(w.rtContext(name, cur, w.data(name, a.Version, answers)))
		}
	}
	if err := w.start(name, target, m, answers, true, r); err != nil {
		return err
	}

	commit := ""
	var rest []state.Release
	for _, p := range a.Previous {
		if p.Version == target {
			commit = p.Commit
		} else {
			rest = append(rest, p)
		}
	}
	if commit == "" {
		commit, _ = gitx.Open(w.Store.RepoDir(name)).Commit(target)
	}
	a.Previous = rest
	a.PushPrevious(m.Update.KeepReleases)
	a.Version, a.Commit, a.UpdatedAt, a.Runtime = target, commit, time.Now().UTC(), m.Runtime.Type
	if err := w.Store.Save(a); err != nil {
		return err
	}
	w.UI.Done("rolled back", since(began))
	return nil
}

// Configure changes answers: specific ones with sets, otherwise all questions again.
func (w *Werft) Configure(name string, sets map[string]string) error {
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
	m, err := w.releaseManifest(name, a.Version)
	if err != nil {
		return err
	}
	answers := a.All()
	if len(sets) > 0 {
		for k, v := range sets {
			q, ok := m.Question(k)
			if !ok {
				return fmt.Errorf("the werft.yml of %s has no question %q", name, k)
			}
			nv, err := manifest.CheckAnswer(q, v)
			if err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
			answers[k] = nv
		}
	} else {
		if !w.Interactive {
			return errors.New("without a terminal this only works with --set id=value")
		}
		if len(m.Questions) == 0 {
			w.UI.Printf("%s has no questions.\n", name)
			return nil
		}
		asker := w.asker(nil)
		asker.Reconfigure = true
		got, err := asker.Ask(m.Questions, answers)
		if err != nil {
			return err
		}
		for k, v := range got {
			answers[k] = v
		}
		fmt.Fprintln(w.UI.Out)
	}

	if label := configLabel(m); label != "" {
		st := w.UI.Step("config", label)
		if err := w.writeConfig(name, a.Version, m, answers); err != nil {
			return w.fail(st, err)
		}
		st.OK("")
	}
	plain, secret := splitSecrets(m, answers)
	for k := range a.Secrets {
		if v, ok := plain[k]; ok {
			secret[k] = v
			delete(plain, k)
		}
	}
	a.Answers, a.Secrets = plain, secret
	if err := w.Store.Save(a); err != nil {
		return err
	}
	if m.Runtime.Type != "none" {
		runtime, err := rt.For(m.Runtime.Type)
		if err != nil {
			return err
		}
		c := w.rtContext(name, m, w.data(name, a.Version, answers))
		if err := runtime.Apply(c); err != nil {
			return err
		}
		st := w.UI.Step("restart", runtimeLabel(name, m))
		if err := runtime.Restart(c); err != nil {
			return w.fail(st, err)
		}
		st.OK("")
	}
	w.UI.Done("saved", "")
	return nil
}

// Uninstall removes an app. With keepData, shared/, .env, templates and backups stay.
func (w *Werft) Uninstall(name string, keepData bool) error {
	if !w.Store.Exists(name) {
		return fmt.Errorf("%s is %w", name, state.ErrNotInstalled)
	}
	a, err := w.Store.Load(name)
	if err != nil {
		return err
	}
	what := "service, releases, configuration, data in shared/ and backups"
	if keepData {
		what = "service and releases; data, configuration and backups stay"
	}
	if !w.confirm(fmt.Sprintf("Remove %s (%s)?", name, what)) {
		return errors.New("aborted")
	}
	lk, err := lock.Acquire(w.Store.AppDir(name), name)
	if err != nil {
		return err
	}
	defer lk.Release()

	m, err := w.releaseManifest(name, a.Version)
	if err != nil {
		w.UI.Warn("cannot read werft.yml of %s (%v); please check the service by hand", a.Version, err)
	} else {
		answers := a.All()
		if m.Hooks.PreUninstall != "" {
			r := w.runner(w.env(name, a.Version, "", m, answers), nil)
			st := w.UI.Step("hook", m.Hooks.PreUninstall)
			if err := r.Script(w.Layout.Dir(name, a.Version), m.Hooks.PreUninstall); err != nil {
				return w.fail(st, err)
			}
			st.OK("")
		}
		if m.Runtime.Type != "none" {
			runtime, err := rt.For(m.Runtime.Type)
			if err != nil {
				return err
			}
			st := w.UI.Step("service", runtimeLabel(name, m))
			if err := runtime.Remove(w.rtContext(name, m, w.data(name, a.Version, answers))); err != nil {
				return w.fail(st, err)
			}
			st.OK("removed")
		}
		if err := w.removeAuto(name); err != nil {
			w.UI.Warn("timer for automatic updates: %v", err)
		}
		if !keepData {
			removeConfig(m)
		}
	}

	st := w.UI.Step("files", w.Layout.AppDir(name))
	if keepData {
		os.RemoveAll(w.Layout.Releases(name))
		os.Remove(w.Layout.Current(name))
	} else if err := os.RemoveAll(w.Layout.AppDir(name)); err != nil {
		return w.fail(st, err)
	}
	if err := w.Store.Remove(name, keepData); err != nil {
		return w.fail(st, err)
	}
	st.OK("removed")
	w.UI.Done("removed", "")
	return nil
}

// List shows all apps in a table.
func (w *Werft) List() error {
	apps, err := w.Store.List()
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		w.UI.Printf("Nothing installed yet. Start with: werft install <git-url>\n")
		return nil
	}
	tw := tabwriter.NewWriter(w.UI.Out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "NAME\tVERSION\tRUNTIME\tSTATUS\tUPDATED")
	for _, a := range apps {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", a.Name, a.Version, a.Runtime, w.status(a), a.UpdatedAt.Local().Format("2006-01-02 15:04"))
	}
	return tw.Flush()
}

func (w *Werft) status(a *state.App) string {
	m, err := w.releaseManifest(a.Name, a.Version)
	if err != nil {
		return "release missing"
	}
	runtime, err := rt.For(m.Runtime.Type)
	if err != nil {
		return err.Error()
	}
	return runtime.Status(w.rtContext(a.Name, m, w.data(a.Name, a.Version, a.All())))
}

// Status shows everything werft knows about an app.
func (w *Werft) Status(name string) error {
	a, err := w.Store.Load(name)
	if err != nil {
		return err
	}
	row := func(k, v string) { w.UI.Printf("  %-13s %s\n", k, v) }
	w.UI.Printf("\n")
	row(a.Name, a.Version+" ("+short(a.Commit)+")")
	row("source", a.Source)
	channel := a.Channel
	if a.Channel == "branch" {
		channel += " " + a.Branch
	}
	row("channel", channel)
	row("runtime", a.Runtime+" · "+w.status(a))
	row("installed", a.InstalledAt.Local().Format("2006-01-02 15:04"))
	row("updated", a.UpdatedAt.Local().Format("2006-01-02 15:04"))
	if len(a.Previous) > 0 {
		var vs []string
		for _, p := range a.Previous {
			vs = append(vs, p.Version)
		}
		row("previous", strings.Join(vs, ", "))
	}
	row("directory", w.Layout.Current(name))
	if entries, _ := os.ReadDir(w.Store.BackupDir(name)); len(entries) > 0 {
		row("backups", fmt.Sprintf("%d in %s", len(entries), w.Store.BackupDir(name)))
	}
	if m, err := w.releaseManifest(name, a.Version); err == nil {
		auto := "off"
		if m.AutoUpdate.Enabled {
			auto = m.AutoUpdate.Schedule + " (" + m.AutoUpdate.Policy + ")"
		}
		row("auto-update", auto)
	}
	w.UI.Printf("\n")
	return nil
}

// Service starts, stops or restarts an app.
func (w *Werft) Service(name, action string) error {
	a, err := w.Store.Load(name)
	if err != nil {
		return err
	}
	m, err := w.releaseManifest(name, a.Version)
	if err != nil {
		return err
	}
	runtime, err := rt.For(m.Runtime.Type)
	if err != nil {
		return err
	}
	c := w.rtContext(name, m, w.data(name, a.Version, a.All()))
	st := w.UI.Step(action, runtimeLabel(name, m))
	switch action {
	case "start":
		err = runtime.Start(c)
	case "stop":
		err = runtime.Stop(c)
	default:
		err = runtime.Restart(c)
	}
	if err != nil {
		return w.fail(st, err)
	}
	st.OK("")
	return nil
}

func (w *Werft) Logs(name string, follow bool, lines int) error {
	a, err := w.Store.Load(name)
	if err != nil {
		return err
	}
	m, err := w.releaseManifest(name, a.Version)
	if err != nil {
		return err
	}
	runtime, err := rt.For(m.Runtime.Type)
	if err != nil {
		return err
	}
	return runtime.Logs(w.rtContext(name, m, w.data(name, a.Version, a.All())), follow, lines)
}

// Backups lists the backups of an app.
func (w *Werft) Backups(name string) ([]string, error) {
	if !w.Store.Exists(name) {
		return nil, fmt.Errorf("%s is %w", name, state.ErrNotInstalled)
	}
	entries, err := os.ReadDir(w.Store.BackupDir(name))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		out = append(out, filepath.Join(w.Store.BackupDir(name), e.Name()))
	}
	return out, nil
}

var nonName = regexp.MustCompile(`[^a-z0-9-]+`)

// Init writes a commented werft.yml into dir.
func Init(dir string) (string, error) {
	path := filepath.Join(dir, manifest.FileName)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	name := strings.Trim(nonName.ReplaceAllString(strings.ToLower(filepath.Base(abs)), "-"), "-")
	if name == "" {
		name = "my-app"
	}
	content := strings.ReplaceAll(initTemplate, "NAME", name)
	return path, os.WriteFile(path, []byte(content), 0o644)
}

const initTemplate = `# yaml-language-server: $schema=https://raw.githubusercontent.com/vxnsin/werft/main/schema/werft.schema.json
# werft.yml: how werft installs and updates this project.
# All fields: https://github.com/vxnsin/werft/blob/main/docs/werft-yml.md
schema: 1
name: NAME
description: ""

source:
  channel: tags        # tags: only releases like v1.2.0 | branch: every commit
  # branch: main

# requires:
#   os: [debian>=12]
#   commands: [python3>=3.11]
#   apt: [ffmpeg]
#   ports: [8080/tcp]

# questions:
#   - id: port
#     prompt: Which port should NAME listen on?
#     type: port
#     default: 8080
#   - id: api_key
#     type: password
#     generate: random(32)

# config:
#   env_file: /etc/NAME/.env

# Directories that survive updates (data, uploads, databases)
# shared: [data]

install:
  - echo "build steps go here"

# update:
#   backup:
#     paths: [/etc/NAME]
#   migrations: .werft/migrations

runtime:
  type: none           # systemd | compose | none
  # systemd:
  #   exec: ./start.sh
  #   user: NAME

# health:
#   http: http://localhost:{{ .port }}/health
`
