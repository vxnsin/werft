package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vxnsin/werft/internal/audit"
	"github.com/vxnsin/werft/internal/gitx"
	"github.com/vxnsin/werft/internal/manifest"
)

// Readers for the audit: a directory on disk, or a ref in the git mirror.

type dirReader struct{ dir string }

func (d dirReader) ReadFile(p string) ([]byte, error) {
	return os.ReadFile(filepath.Join(d.dir, filepath.FromSlash(p)))
}

func (d dirReader) ReadDir(p string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(d.dir, filepath.FromSlash(p)))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

type repoReader struct {
	repo *gitx.Repo
	ref  string
}

func (r repoReader) ReadFile(p string) ([]byte, error)  { return r.repo.ShowFile(r.ref, p) }
func (r repoReader) ReadDir(p string) ([]string, error) { return r.repo.Files(r.ref, p) }

// review runs the audit and prints the findings. The result says whether
// anything critical came up.
func (w *Werft) review(m *manifest.Manifest, r audit.Reader) int {
	findings := audit.Scan(audit.Collect(m, r))
	return w.printFindings(findings)
}

func (w *Werft) printFindings(findings []audit.Finding) int {
	critical := audit.Count(findings, audit.Critical)
	st := w.UI.Step("audit", "commands, hooks, migrations")
	switch {
	case len(findings) == 0:
		st.OK("clean")
	case critical > 0:
		st.Fail(nil)
	default:
		st.Skip(plural(len(findings), "note"))
	}
	for _, f := range findings {
		w.UI.Printf("    %s\n", f)
		w.UI.Output(f.Text, 1)
	}
	return critical
}

// gate stops when the audit found something critical: automatic updates
// skip, otherwise the user decides.
func (w *Werft) gate(critical int, auto bool, what string) error {
	if critical == 0 {
		return nil
	}
	if auto {
		return fmt.Errorf("%w: the audit found %s, look at it by hand: werft %s", ErrAutoSkipped, plural(critical, "critical finding"), what)
	}
	if !w.confirm(fmt.Sprintf("%s found. Continue anyway?", plural(critical, "critical finding"))) {
		return errors.New("aborted")
	}
	return nil
}

// Audit checks a project directory or an installed app and returns how many
// critical findings there are.
func (w *Werft) Audit(target string) (int, error) {
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		m, err := manifest.Load(filepath.Join(target, manifest.FileName))
		if err != nil {
			return 0, err
		}
		if err := problemsErr(m.Validate()); err != nil {
			return 0, err
		}
		return w.review(m, dirReader{target}), nil
	}
	a, err := w.Store.Load(target)
	if err != nil {
		return 0, err
	}
	if a.Kind == "git" {
		var src []audit.Source
		if a.After != "" {
			src = append(src, audit.Source{Where: "after command", Text: a.After})
		}
		if b, err := os.ReadFile(filepath.Join(a.Dir, "package.json")); err == nil {
			_ = b
			m := &manifest.Manifest{}
			src = append(src, audit.Collect(m, dirReader{a.Dir})...)
		}
		return w.printFindings(audit.Scan(src)), nil
	}
	m, err := w.releaseManifest(a.Name, a.Version)
	if err != nil {
		return 0, err
	}
	return w.review(m, dirReader{w.Layout.Dir(a.Name, a.Version)}), nil
}

// auditAfter checks the after command of a tracked repository.
func (w *Werft) auditAfter(after string) {
	if strings.TrimSpace(after) == "" {
		return
	}
	if fs := audit.Scan([]audit.Source{{Where: "after command", Text: after}}); len(fs) > 0 {
		w.printFindings(fs)
	}
}
