package app

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vxnsin/werft/internal/release"
	"github.com/vxnsin/werft/internal/state"
	"github.com/vxnsin/werft/internal/ui"
)

// A whole lifecycle with runtime: none: install, update,
// automatic rollback, manual rollback, protection against changed commands.
func TestLifecycle(t *testing.T) {
	for _, tool := range []string{"git", "sh"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is missing")
		}
	}
	base := t.TempDir()
	etc := slashAbs(filepath.Join(base, "etc"))
	src := filepath.Join(base, "src")
	git := gitRepo(t, src)
	release := func(tag, yml string) {
		t.Helper()
		write(t, src, "werft.yml", strings.ReplaceAll(yml, "ETC", etc))
		git("add", "-A")
		git("commit", "-q", "-m", tag)
		git("tag", tag)
	}

	write(t, src, "data/.keep", "")
	write(t, src, ".werft/greeting.tmpl", "Greeting: {{ .greeting }} from {{ .werft_version }}")
	write(t, src, ".werft/migrations/001_start.sh", `echo 001 >> "$WERFT_SHARED_DIR/data/migrations-ran"`)
	release("v1.0.0", yml("", ""))

	w, out := testWerft(base)
	err := w.Install(InstallOptions{Source: src, Answers: map[string]string{"greeting": "hello", "pw": "secret"}})
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	expectCurrent(t, w, "v1.0.0")
	expectFile(t, filepath.Join(w.Layout.Current("demo"), "built.txt"), "hello\n")
	expectFile(t, filepath.Join(base, "etc", "greeting.txt"), "Greeting: hello from v1.0.0")
	env, _ := os.ReadFile(filepath.Join(base, "etc", "demo.env"))
	if !strings.Contains(string(env), "GREETING=hello\nPW=secret\nRELEASE=v1.0.0\n") {
		t.Errorf(".env: %q", env)
	}
	a := load(t, w)
	if a.Answers["pw"] != "" || a.Secrets["pw"] != "secret" {
		t.Errorf("the password belongs in secrets.json: %+v / %+v", a.Answers, a.Secrets)
	}
	if len(a.Migrations) != 1 {
		t.Errorf("existing migrations count as done after install: %v", a.Migrations)
	}
	if _, err := os.Stat(filepath.Join(w.Layout.Shared("demo"), "data", "migrations-ran")); err == nil {
		t.Error("migration 001 must not run on install")
	}

	// v1.1.0: a new migration, a new question with a default, a backup.
	write(t, src, ".werft/migrations/002_more.sh", `echo 002 >> "$WERFT_SHARED_DIR/data/migrations-ran"`)
	release("v1.1.0", yml(color, ""))
	if err := w.Update("demo", UpdateOptions{}); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	expectCurrent(t, w, "v1.1.0")
	expectFile(t, filepath.Join(w.Layout.Shared("demo"), "data", "migrations-ran"), "002\n")
	expectFile(t, filepath.Join(base, "etc", "greeting.txt"), "Greeting: hello from v1.1.0")
	a = load(t, w)
	if a.Answers["color"] != "blue" || a.Previous[0].Version != "v1.0.0" {
		t.Errorf("state after update: %+v", a)
	}
	if files, _ := w.Backups("demo"); len(files) != 1 {
		t.Errorf("expected one backup: %v", files)
	}

	// v1.2.0: the health check fails, werft goes back to v1.1.0.
	release("v1.2.0", yml(color, broken))
	err = w.Update("demo", UpdateOptions{})
	if err == nil || !strings.Contains(err.Error(), "v1.1.0 is running again") {
		t.Fatalf("expected a rollback, got: %v\n%s", err, out)
	}
	expectCurrent(t, w, "v1.1.0")
	expectFile(t, filepath.Join(base, "etc", "greeting.txt"), "Greeting: hello from v1.1.0")
	if load(t, w).Version != "v1.1.0" {
		t.Error("state.json must keep v1.1.0")
	}

	// Manual rollback to v1.0.0.
	if err := w.Rollback("demo", ""); err != nil {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	expectCurrent(t, w, "v1.0.0")
	if a := load(t, w); a.Previous[0].Version != "v1.1.0" {
		t.Errorf("v1.1.0 should now be the previous version: %+v", a.Previous)
	}

	// v1.3.0 changes a build step: that does not go through automatically.
	release("v1.3.0", strings.Replace(yml(color, ""), "> built.txt", "> built.txt && echo new > new.txt", 1))
	err = w.Update("demo", UpdateOptions{Auto: true, Policy: "major"})
	if !errors.Is(err, ErrAutoSkipped) {
		t.Fatalf("changed commands must stop the automatic update: %v", err)
	}
	expectCurrent(t, w, "v1.0.0")

	// By hand with --yes it does.
	w.Yes = true
	if err := w.Update("demo", UpdateOptions{}); err != nil {
		t.Fatalf("update with --yes: %v\n%s", err, out)
	}
	expectCurrent(t, w, "v1.3.0")
	if rels, _ := w.Layout.List("demo"); len(rels) > 3 {
		t.Errorf("keep_releases 3, gefunden %v", rels)
	}

	if err := w.Uninstall("demo", false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	for _, p := range []string{w.Layout.AppDir("demo"), w.Store.AppDir("demo"), filepath.Join(base, "etc", "demo.env")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s should be gone", p)
		}
	}
}

const v1 = `schema: 1
name: demo
description: Test project
questions:
  - id: greeting
    prompt: Greeting
  - id: pw
    prompt: Password
    type: password
#FRAGEN
config:
  env_file: ETC/demo.env
  env:
    RELEASE: "{{ .werft_version }}"
  templates:
    - src: .werft/greeting.tmpl
      dest: ETC/greeting.txt
shared: [data]
install:
  - echo "$GREETING" > built.txt
update:
  migrations: .werft/migrations
  backup:
    paths: [ETC]
runtime:
  type: none
`

// yml builds the werft.yml of a version: extra questions and extra fields.
func yml(questions, extra string) string {
	return strings.Replace(v1, "#FRAGEN\n", questions, 1) + extra
}

const color = "  - id: color\n    prompt: Color\n    default: blue\n"

const broken = "health:\n  cmd: exit 1\n  timeout: 1s\n"

func testWerft(base string) (*Werft, *bytes.Buffer) {
	var out bytes.Buffer
	return &Werft{
		Store:  state.Store{Home: filepath.Join(base, "home")},
		Layout: release.Layout{Root: filepath.Join(base, "apps")},
		UI:     &ui.UI{Out: &out, Err: &out},
		In:     bufio.NewReader(strings.NewReader("")),
	}, &out
}

func gitRepo(t *testing.T, dir string) func(args ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.org", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init", "-q", "-b", "main")
	return run
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func expectCurrent(t *testing.T, w *Werft, want string) {
	t.Helper()
	got, err := w.Layout.CurrentVersion("demo")
	if err != nil || got != want {
		t.Fatalf("current points to %q (%v), want %s", got, err, want)
	}
}

func expectFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if strings.ReplaceAll(string(got), "\r\n", "\n") != want {
		t.Errorf("%s: %q, want %q", path, got, want)
	}
}

func load(t *testing.T, w *Werft) *state.App {
	t.Helper()
	a, err := w.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// slashAbs turns C:\x\y into /x/y on Windows so it passes the werft.yml
// check as an absolute path in the Linux sense.
func slashAbs(p string) string {
	if runtime.GOOS == "windows" {
		return filepath.ToSlash(strings.TrimPrefix(p, filepath.VolumeName(p)))
	}
	return p
}
