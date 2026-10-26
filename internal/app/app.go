// Package app is the flow of werft: install, update, roll back.
package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vxnsin/werft/internal/cfgfiles"
	"github.com/vxnsin/werft/internal/gitx"
	"github.com/vxnsin/werft/internal/health"
	"github.com/vxnsin/werft/internal/manifest"
	"github.com/vxnsin/werft/internal/prompt"
	"github.com/vxnsin/werft/internal/release"
	"github.com/vxnsin/werft/internal/requires"
	"github.com/vxnsin/werft/internal/rt"
	"github.com/vxnsin/werft/internal/runner"
	"github.com/vxnsin/werft/internal/state"
	"github.com/vxnsin/werft/internal/ui"
	"golang.org/x/term"
)

type Werft struct {
	Store       state.Store
	Layout      release.Layout
	UI          *ui.UI
	In          *bufio.Reader
	Interactive bool // questions allowed (stdin is a terminal)
	Yes         bool // answer every confirmation with yes
	Verbose     bool // show the output of steps live
	Self        string

	held                    []string // ports held at warden for the app being worked on
	wardenChecked, wardenUp bool
}

// FromEnv uses the default paths; WERFT_HOME and WERFT_APPS override them.
func FromEnv() *Werft {
	home := os.Getenv("WERFT_HOME")
	if home == "" {
		home = "/var/lib/werft"
	}
	apps := os.Getenv("WERFT_APPS")
	if apps == "" {
		apps = "/opt/werft"
	}
	self, _ := os.Executable()
	return &Werft{
		Store:       state.Store{Home: home},
		Layout:      release.Layout{Root: apps},
		UI:          ui.New(),
		In:          bufio.NewReader(os.Stdin),
		Interactive: term.IsTerminal(int(os.Stdin.Fd())),
		Self:        self,
	}
}

// confirm asks yes/no, the default is no. Without a terminal and without --yes that means no.
func (w *Werft) confirm(question string) bool {
	if w.Yes {
		return true
	}
	if !w.Interactive {
		return false
	}
	fmt.Fprintf(w.UI.Out, "  %s [y/N] ", question)
	line, _ := w.In.ReadString(10)
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func (w *Werft) asker(name string, preset map[string]string) *prompt.Asker {
	a := prompt.New(w.In, w.UI.Out, w.Interactive)
	a.Preset = preset
	a.Claim = w.claimer(name)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		a.ReadSecret = func() (string, error) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(w.UI.Out)
			return string(b), err
		}
	}
	return a
}

func (w *Werft) checker() requires.Checker {
	return requires.Checker{Confirm: w.confirm}
}

// splitSecrets separates secret answers from the others.
func splitSecrets(m *manifest.Manifest, answers map[string]string) (plain, secret map[string]string) {
	plain, secret = map[string]string{}, map[string]string{}
	for k, v := range answers {
		if q, ok := m.Question(k); ok && q.IsSecret() {
			secret[k] = v
		} else {
			plain[k] = v
		}
	}
	return plain, secret
}

// data are the values for {{ }} in templates, exec, health and backup.paths.
func (w *Werft) data(name, version string, answers map[string]string) map[string]string {
	d := make(map[string]string, len(answers)+4)
	for k, v := range answers {
		d[k] = v
	}
	d["werft_app"] = name
	d["werft_version"] = version
	d["werft_current"] = w.Layout.Current(name)
	d["werft_shared"] = w.Layout.Shared(name)
	return d
}

// env is the environment for steps, hooks and migrations.
func (w *Werft) env(name, version, previous string, m *manifest.Manifest, answers map[string]string) []string {
	env := []string{
		"WERFT_APP=" + name,
		"WERFT_VERSION=" + version,
		"WERFT_PREVIOUS_VERSION=" + previous,
		"WERFT_RELEASE_DIR=" + w.Layout.Dir(name, version),
		"WERFT_SHARED_DIR=" + w.Layout.Shared(name),
	}
	for _, q := range m.Questions {
		if v := answers[q.ID]; v != "" {
			env = append(env, q.EnvName()+"="+v)
		}
	}
	fixed, _ := w.fixedEnv(name, version, m, answers)
	for k, v := range fixed {
		env = append(env, k+"="+v)
	}
	return env
}

// fixedEnv renders config.env, the values that are not questions.
func (w *Werft) fixedEnv(name, version string, m *manifest.Manifest, answers map[string]string) (map[string]string, error) {
	data := w.data(name, version, answers)
	out := make(map[string]string, len(m.Config.Env)+2)
	for k, v := range wardenEnv(name, m, answers, w.held) {
		out[k] = v
	}
	for k, v := range m.Config.Env {
		rv, err := manifest.Render(v, data)
		if err != nil {
			return nil, fmt.Errorf("config.env.%s: %w", k, err)
		}
		out[k] = rv
	}
	return out, nil
}

// logFile creates a log file for this run.
func (w *Werft) logFile(name, what string) (*os.File, error) {
	dir := w.Store.LogDir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, time.Now().UTC().Format("20060102-150405")+"-"+what+".log")
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

func (w *Werft) runner(env []string, log io.Writer) *runner.Runner {
	r := &runner.Runner{Env: env, Log: log}
	if w.Verbose {
		r.Verbose = w.UI.Out
	}
	return r
}

// fail closes a step line with an error and shows the last output.
func (w *Werft) fail(st *ui.Step, err error) error {
	st.Fail(err)
	var re *runner.Error
	if errors.As(err, &re) && !w.Verbose {
		w.UI.Output(re.Output, 15)
	}
	return err
}

func readManifest(repo *gitx.Repo, ref string) (*manifest.Manifest, error) {
	data, err := repo.ShowFile(ref, manifest.FileName)
	if err != nil {
		return nil, err
	}
	return manifest.Parse(data)
}

// releaseManifest reads werft.yml from an unpacked release.
func (w *Werft) releaseManifest(name, version string) (*manifest.Manifest, error) {
	return manifest.Load(filepath.Join(w.Layout.Dir(name, version), manifest.FileName))
}

func problemsErr(ps []manifest.Problem) error {
	if len(ps) == 0 {
		return nil
	}
	lines := make([]string, len(ps))
	for i, p := range ps {
		lines[i] = "  " + p.String()
	}
	return fmt.Errorf("werft.yml has mistakes:\n%s", strings.Join(lines, "\n"))
}

func (w *Werft) rtContext(name string, m *manifest.Manifest, data map[string]string) rt.Context {
	return rt.Context{
		App:      name,
		Current:  w.Layout.Current(name),
		Shared:   w.Layout.Shared(name),
		Manifest: m,
		Data:     data,
		EnvFile:  m.Config.EnvFile,
	}
}

// writeConfig writes the .env file and templates for a release.
func (w *Werft) writeConfig(name, version string, m *manifest.Manifest, answers map[string]string) error {
	group := ""
	if m.Runtime.Type == "systemd" && m.Runtime.Systemd.User != "" {
		// The group has to exist before it may read the .env file.
		group = m.Runtime.Systemd.User
		if err := rt.EnsureUser(group); err != nil {
			return err
		}
	}
	if m.Config.EnvFile != "" {
		fixed, err := w.fixedEnv(name, version, m, answers)
		if err != nil {
			return err
		}
		if err := cfgfiles.WriteEnv(m.Config.EnvFile, name, m, answers, fixed, group); err != nil {
			return err
		}
	}
	_, err := cfgfiles.RenderTemplates(m, w.Layout.Dir(name, version), w.data(name, version, answers), group)
	return err
}

// build unpacks a release and runs the build steps. None of this touches
// the running version.
func (w *Werft) build(name, version, commit string, repo *gitx.Repo, m *manifest.Manifest, r *runner.Runner, install bool) error {
	dir := w.Layout.Dir(name, version)
	if cur, _ := w.Layout.CurrentVersion(name); cur == release.DirName(version) {
		return fmt.Errorf("%s is running, its release will not be rebuilt", version)
	}
	os.RemoveAll(dir)

	st := w.UI.Step("release", "releases/"+release.DirName(version))
	if err := repo.Export(commit, dir); err != nil {
		return w.fail(st, err)
	}
	if err := w.Layout.LinkShared(name, version, m.Shared); err != nil {
		return w.fail(st, err)
	}
	st.OK("")

	hook := m.Hooks.PreUpdate
	if install {
		hook = m.Hooks.PreInstall
	}
	if hook != "" {
		st := w.UI.Step("hook", hook)
		if err := r.Script(dir, hook); err != nil {
			return w.fail(st, err)
		}
		st.OK("")
	}

	steps := m.Install
	if !install {
		steps = append(slices.Clone(steps), m.Update.Steps...)
	}
	for _, s := range steps {
		st := w.UI.Step("step", s.Label())
		sdir := dir
		if s.Dir != "" {
			sdir = filepath.Join(dir, filepath.FromSlash(s.Dir))
		}
		if err := r.Run(sdir, s.Run); err != nil {
			return w.fail(st, err)
		}
		st.OK("")
	}
	return nil
}

// migrations lists the migration files of a release, sorted.
func (w *Werft) migrations(name, version string, m *manifest.Manifest) ([]string, error) {
	if m.Update.Migrations == "" {
		return nil, nil
	}
	dir := filepath.Join(w.Layout.Dir(name, version), filepath.FromSlash(m.Update.Migrations))
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// migrate runs every migration that has not run yet.
func (w *Werft) migrate(name, version string, m *manifest.Manifest, r *runner.Runner, applied []string) ([]string, error) {
	files, err := w.migrations(name, version, m)
	if err != nil {
		return applied, err
	}
	for _, f := range files {
		if slices.Contains(applied, f) {
			continue
		}
		st := w.UI.Step("migration", f)
		if err := r.Script(w.Layout.Dir(name, version), m.Update.Migrations+"/"+f); err != nil {
			return applied, w.fail(st, err)
		}
		applied = append(applied, f)
		st.OK("")
	}
	return applied, nil
}

// start switches current, applies the runtime, restarts and checks health.
func (w *Werft) start(name, version string, m *manifest.Manifest, answers map[string]string, restart bool, r *runner.Runner) error {
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
	if m.Runtime.Type != "none" && restart {
		st := w.UI.Step("restart", runtimeLabel(name, m))
		if err := runtime.Restart(c); err != nil {
			return w.fail(st, err)
		}
		st.OK("")
	}
	if m.Health != nil {
		return w.checkHealth(name, version, m, answers, r)
	}
	return nil
}

func (w *Werft) checkHealth(name, version string, m *manifest.Manifest, answers map[string]string, r *runner.Runner) error {
	data := w.data(name, version, answers)
	label := m.Health.HTTP
	if label == "" {
		label = m.Health.Cmd
	}
	if rendered, err := manifest.Render(label, data); err == nil {
		label = strings.TrimPrefix(strings.TrimPrefix(rendered, "http://localhost"), "http://127.0.0.1")
	}
	st := w.UI.Step("health", label)
	result, err := health.Wait(m.Health, data, func(cmd string) error {
		return r.Run(w.Layout.Current(name), cmd)
	})
	if err != nil {
		return w.fail(st, err)
	}
	st.OK(result)
	return nil
}

func runtimeLabel(name string, m *manifest.Manifest) string {
	switch m.Runtime.Type {
	case "systemd":
		u := m.Runtime.Systemd.Unit
		if u == "" {
			u = name
		}
		return u + ".service"
	case "compose":
		return m.Runtime.Compose.File
	}
	return m.Runtime.Type
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

func since(t time.Time) string {
	d := time.Since(t).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("in %ds", int(d.Seconds()))
	}
	return "in " + d.String()
}
