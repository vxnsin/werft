package audit

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/vxnsin/werft/internal/manifest"
)

// Reader gives access to the files of a project, on disk or in git.
type Reader interface {
	ReadFile(path string) ([]byte, error)
	ReadDir(path string) ([]string, error)
}

// Collect gathers everything werft would run for a manifest.
func Collect(m *manifest.Manifest, r Reader) []Source {
	var out []Source
	add := func(where, text string) {
		if strings.TrimSpace(text) != "" {
			out = append(out, Source{where, text})
		}
	}
	for i, s := range m.Install {
		add(fmt.Sprintf("werft.yml install[%d]", i), s.Run)
	}
	for i, s := range m.Update.Steps {
		add(fmt.Sprintf("werft.yml update.steps[%d]", i), s.Run)
	}
	add("werft.yml runtime.systemd.exec", m.Runtime.Systemd.Exec)
	if m.Health != nil {
		add("werft.yml health.cmd", m.Health.Cmd)
	}
	for _, p := range []string{m.Hooks.PreInstall, m.Hooks.PostInstall, m.Hooks.PreUpdate, m.Hooks.PostUpdate, m.Hooks.PreUninstall} {
		if p == "" {
			continue
		}
		if b, err := r.ReadFile(p); err == nil {
			add(p, string(b))
		}
	}
	if m.Update.Migrations != "" {
		names, _ := r.ReadDir(m.Update.Migrations)
		sort.Strings(names)
		for _, n := range names {
			p := path.Join(m.Update.Migrations, n)
			if b, err := r.ReadFile(p); err == nil {
				add(p, string(b))
			}
		}
	}
	if b, err := r.ReadFile("package.json"); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(b, &pkg) == nil {
			for _, k := range []string{"preinstall", "install", "postinstall", "prepare", "prepublish", "preprepare", "postprepare"} {
				add("package.json scripts."+k, pkg.Scripts[k])
			}
		}
	}
	return out
}
