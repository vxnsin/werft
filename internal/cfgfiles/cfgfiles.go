// Package cfgfiles writes the .env file and the templates from the answers.
package cfgfiles

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/vxnsin/werft/internal/manifest"
)

const envHeader = "# Written by werft. Values changed by hand are lost on the next update;\n# change them with: werft config %s --set id=value\n"

// EnvLines returns the lines of the .env file: answers in the order of the
// questions, then the fixed values from config.env. Empty answers are left
// out so the app falls back to its own default.
func EnvLines(m *manifest.Manifest, answers, fixed map[string]string) []string {
	var lines []string
	for _, q := range m.Questions {
		if v := answers[q.ID]; v != "" {
			lines = append(lines, q.EnvName()+"="+Quote(v))
		}
	}
	keys := make([]string, 0, len(fixed))
	for k := range fixed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lines = append(lines, k+"="+Quote(fixed[k]))
	}
	return lines
}

// Quote adds quotes only when needed. Both systemd (EnvironmentFile)
// and docker compose (env_file) understand this format.
func Quote(v string) string {
	safe := v != ""
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:/@+,=%", r)) {
			safe = false
			break
		}
	}
	if safe {
		return v
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "$", `\$`, "`", "\\`")
	return `"` + r.Replace(v) + `"`
}

// WriteEnv writes the .env file with 0640; group (the service user) may read it.
func WriteEnv(path, app string, m *manifest.Manifest, answers, fixed map[string]string, group string) error {
	content := fmt.Sprintf(envHeader, app) + strings.Join(EnvLines(m, answers, fixed), "\n") + "\n"
	return writeFile(path, []byte(content), 0o640, group)
}

// RenderTemplates writes all config.templates and returns the target paths.
func RenderTemplates(m *manifest.Manifest, releaseDir string, data map[string]string, group string) ([]string, error) {
	var written []string
	for _, t := range m.Config.Templates {
		src, err := os.ReadFile(filepath.Join(releaseDir, filepath.FromSlash(t.Src)))
		if err != nil {
			return written, fmt.Errorf("template %s: %w", t.Src, err)
		}
		out, err := manifest.Render(string(src), data)
		if err != nil {
			return written, fmt.Errorf("template %s: %w", t.Src, err)
		}
		mode := uint32(0o640)
		if t.Mode != "" {
			mode, _ = manifest.ParseMode(t.Mode)
		}
		if err := writeFile(t.Dest, []byte(out), os.FileMode(mode), group); err != nil {
			return written, err
		}
		written = append(written, t.Dest)
	}
	return written, nil
}

func writeFile(path string, data []byte, mode os.FileMode, group string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".werft-neu"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	if group != "" && runtime.GOOS != "windows" {
		g, err := user.LookupGroup(group)
		if err != nil {
			os.Remove(tmp)
			return fmt.Errorf("group %s: %w", group, err)
		}
		gid, _ := strconv.Atoi(g.Gid)
		if err := os.Chown(tmp, 0, gid); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	return os.Rename(tmp, path)
}
