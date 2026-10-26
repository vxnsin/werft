package manifest

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	nameRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	idRe       = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	envRe      = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	generateRe = regexp.MustCompile(`^random\((\d+)\)$`)
)

var questionTypes = map[string]bool{
	"string": true, "int": true, "bool": true, "choice": true,
	"password": true, "path": true, "port": true, "url": true,
}

// Problem is a mistake in werft.yml together with the field it is in.
type Problem struct {
	Field string
	Msg   string
}

func (p Problem) String() string { return p.Field + ": " + p.Msg }

// GenerateLen returns N from generate: random(N).
func GenerateLen(spec string) (int, bool) {
	m := generateRe.FindStringSubmatch(spec)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// Validate checks everything that can be checked without a server.
func (m *Manifest) Validate() []Problem {
	var ps []Problem
	add := func(field, format string, args ...any) {
		ps = append(ps, Problem{field, fmt.Sprintf(format, args...)})
	}

	if m.Schema != 1 {
		add("schema", "must be 1, is %d", m.Schema)
	}
	if !nameRe.MatchString(m.Name) {
		add("name", "%q: only a-z, 0-9 and -, at most 63 characters", m.Name)
	}
	switch m.Source.Channel {
	case "tags", "branch":
	default:
		add("source.channel", "%q: use tags or branch", m.Source.Channel)
	}

	for i, spec := range m.Requires.Ports {
		if _, _, err := ParsePort(spec); err != nil {
			add(fmt.Sprintf("requires.ports[%d]", i), "%v", err)
		}
	}

	seen := map[string]bool{}
	for i, q := range m.Questions {
		f := fmt.Sprintf("questions[%d]", i)
		if !idRe.MatchString(q.ID) {
			add(f+".id", "%q: lowercase letters, digits and _, starting with a letter", q.ID)
		}
		if strings.HasPrefix(q.ID, "werft_") {
			add(f+".id", "%q: werft_ is reserved for werft itself", q.ID)
		}
		if seen[q.ID] {
			add(f+".id", "%q is used twice", q.ID)
		}
		if !questionTypes[q.Type] {
			add(f+".type", "unknown type %q", q.Type)
		}
		if q.Type == "choice" && len(q.Choices) == 0 {
			add(f+".choices", "type: choice needs choices")
		}
		if q.Warden && q.Type != "port" {
			add(f+".warden", "only for type: port")
		}
		if q.Type != "choice" && len(q.Choices) > 0 {
			add(f+".choices", "only for type: choice")
		}
		if q.Validate != "" {
			if _, err := regexp.Compile(q.Validate); err != nil {
				add(f+".validate", "not a valid regular expression: %v", err)
			}
		}
		if q.Env != "" && !envRe.MatchString(q.Env) {
			add(f+".env", "%q: uppercase letters, digits and _", q.Env)
		}
		if q.Generate != "" {
			if n, ok := GenerateLen(q.Generate); !ok {
				add(f+".generate", "%q: only random(N) for now", q.Generate)
			} else if n < 8 || n > 256 {
				add(f+".generate", "length between 8 and 256")
			}
		}
		if q.Prompt == "" && q.Generate == "" {
			add(f+".prompt", "missing")
		}
		if q.When != "" {
			refs, err := WhenRefs(q.When)
			if err != nil {
				add(f+".when", "%v", err)
			}
			for _, r := range refs {
				if !seen[r] {
					add(f+".when", "%q must be an earlier question", r)
				}
			}
		}
		if q.Default != "" && q.Generate == "" {
			if _, err := CheckAnswer(q, q.Default); err != nil {
				add(f+".default", "%v", err)
			}
		}
		seen[q.ID] = true
	}

	if m.Config.EnvFile != "" && !path.IsAbs(m.Config.EnvFile) {
		add("config.env_file", "must be an absolute path")
	}
	for k := range m.Config.Env {
		if !envRe.MatchString(k) {
			add("config.env."+k, "uppercase letters, digits and _")
		}
	}
	for i, t := range m.Config.Templates {
		f := fmt.Sprintf("config.templates[%d]", i)
		checkRel(add, f+".src", t.Src)
		if !path.IsAbs(t.Dest) {
			add(f+".dest", "must be an absolute path")
		}
		if t.Mode != "" {
			if _, err := ParseMode(t.Mode); err != nil {
				add(f+".mode", "%v", err)
			}
		}
	}
	for i, s := range m.Shared {
		checkRel(add, fmt.Sprintf("shared[%d]", i), s)
	}

	checkSteps(add, "install", m.Install)
	checkSteps(add, "update.steps", m.Update.Steps)
	for i, p := range m.Update.Backup.Paths {
		if !path.IsAbs(p) && !strings.HasPrefix(p, "{{") {
			add(fmt.Sprintf("update.backup.paths[%d]", i), "must be an absolute path")
		}
	}
	if m.Update.Migrations != "" {
		checkRel(add, "update.migrations", m.Update.Migrations)
	}
	switch m.Update.Restart {
	case "always", "never":
	default:
		add("update.restart", "%q: use always or never", m.Update.Restart)
	}
	if m.Update.KeepReleases < 2 {
		add("update.keep_releases", "at least 2, otherwise there is nothing to roll back to")
	}

	switch m.Runtime.Type {
	case "systemd":
		if m.Runtime.Systemd.Exec == "" {
			add("runtime.systemd.exec", "missing")
		}
		switch m.Runtime.Systemd.Restart {
		case "no", "always", "on-failure", "on-abnormal", "on-abort", "on-success", "on-watchdog":
		default:
			add("runtime.systemd.restart", "%q is not a systemd Restart= value", m.Runtime.Systemd.Restart)
		}
		if u := m.Runtime.Systemd.Unit; u != "" && !nameRe.MatchString(u) {
			add("runtime.systemd.unit", "%q: only a-z, 0-9 and -", u)
		}
		if u := m.Runtime.Systemd.User; u != "" && !nameRe.MatchString(u) {
			add("runtime.systemd.user", "%q: only a-z, 0-9 and -", u)
		}
	case "compose":
		checkRel(add, "runtime.compose.file", m.Runtime.Compose.File)
	case "none":
	default:
		add("runtime.type", "%q: use systemd, compose or none", m.Runtime.Type)
	}

	if h := m.Health; h != nil {
		if (h.HTTP == "") == (h.Cmd == "") {
			add("health", "set exactly one of http or cmd")
		}
		if _, err := time.ParseDuration(h.Timeout); err != nil {
			add("health.timeout", "%q: a duration like 30s or 2m", h.Timeout)
		}
	}

	hooks := []struct{ name, file string }{
		{"pre_install", m.Hooks.PreInstall}, {"post_install", m.Hooks.PostInstall},
		{"pre_update", m.Hooks.PreUpdate}, {"post_update", m.Hooks.PostUpdate},
		{"pre_uninstall", m.Hooks.PreUninstall},
	}
	for _, h := range hooks {
		if h.file != "" {
			checkRel(add, "hooks."+h.name, h.file)
		}
	}
	return ps
}

func checkRel(add func(string, string, ...any), field, p string) {
	switch {
	case p == "":
		add(field, "missing")
	case path.IsAbs(p):
		add(field, "must be relative to the project")
	case path.Clean(p) == ".." || strings.HasPrefix(path.Clean(p), "../"):
		add(field, "must not point outside the project")
	}
}

func checkSteps(add func(string, string, ...any), field string, steps []Step) {
	for i, s := range steps {
		if strings.TrimSpace(s.Run) == "" {
			add(fmt.Sprintf("%s[%d]", field, i), "run is missing")
		}
		if s.Dir != "" {
			checkRel(add, fmt.Sprintf("%s[%d].dir", field, i), s.Dir)
		}
	}
}

// ParsePort splits "5060/udp" into number and protocol (tcp by default).
func ParsePort(spec string) (int, string, error) {
	num, proto, found := strings.Cut(spec, "/")
	if !found {
		proto = "tcp"
	}
	if proto != "tcp" && proto != "udp" {
		return 0, "", fmt.Errorf("%q: protocol tcp or udp", spec)
	}
	n, err := strconv.Atoi(num)
	if err != nil || n < 1 || n > 65535 {
		return 0, "", fmt.Errorf("%q is not a port", spec)
	}
	return n, proto, nil
}

// ParseMode reads file permissions like "0640".
func ParseMode(s string) (uint32, error) {
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil || n > 0o777 {
		return 0, fmt.Errorf("%q: octal permissions like 0640", s)
	}
	return uint32(n), nil
}

// ValidName reports whether s can be the name of an app.
func ValidName(s string) bool { return nameRe.MatchString(s) }
