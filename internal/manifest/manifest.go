// Package manifest reads and checks the werft.yml of a project.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

const FileName = "werft.yml"

type Manifest struct {
	Schema      int        `yaml:"schema"`
	Name        string     `yaml:"name"`
	Description string     `yaml:"description"`
	Source      Source     `yaml:"source"`
	Requires    Requires   `yaml:"requires"`
	Questions   []Question `yaml:"questions"`
	Config      Config     `yaml:"config"`
	Shared      []string   `yaml:"shared"`
	Install     []Step     `yaml:"install"`
	Update      Update     `yaml:"update"`
	Runtime     Runtime    `yaml:"runtime"`
	Health      *Health    `yaml:"health"`
	Hooks       Hooks      `yaml:"hooks"`
}

type Source struct {
	Channel         string `yaml:"channel"`
	Branch          string `yaml:"branch"`
	VerifySignature bool   `yaml:"verify_signature"`
}

type Requires struct {
	OS       []string `yaml:"os"`
	Root     bool     `yaml:"root"`
	Commands []string `yaml:"commands"`
	Apt      []string `yaml:"apt"`
	Ports    []string `yaml:"ports"`
}

type Question struct {
	ID       string   `yaml:"id"`
	Prompt   string   `yaml:"prompt"`
	Help     string   `yaml:"help"`
	Type     string   `yaml:"type"`
	Default  string   `yaml:"default"`
	Choices  []string `yaml:"choices"`
	Validate string   `yaml:"validate"`
	Secret   bool     `yaml:"secret"`
	Optional bool     `yaml:"optional"`
	When     string   `yaml:"when"`
	Env      string   `yaml:"env"`
	Generate string   `yaml:"generate"`
}

// EnvName is the variable name in the .env file and in the environment of steps.
func (q Question) EnvName() string {
	if q.Env != "" {
		return q.Env
	}
	return strings.ToUpper(q.ID)
}

// IsSecret: passwords are always secret, even without secret: true.
func (q Question) IsSecret() bool {
	return q.Secret || q.Type == "password"
}

type Config struct {
	EnvFile   string            `yaml:"env_file"`
	Env       map[string]string `yaml:"env"`
	Templates []Template        `yaml:"templates"`
}

type Template struct {
	Src  string `yaml:"src"`
	Dest string `yaml:"dest"`
	Mode string `yaml:"mode"`
}

// Step is a shell command. A plain string is enough in YAML, the long form is {run, name, dir}.
type Step struct {
	Run  string `yaml:"run"`
	Name string `yaml:"name"`
	Dir  string `yaml:"dir"`
}

func (s *Step) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		s.Run = n.Value
		return nil
	}
	type plain Step
	return n.Decode((*plain)(s))
}

// Label is the short form shown in the output.
func (s Step) Label() string {
	if s.Name != "" {
		return s.Name
	}
	line := strings.TrimSpace(strings.SplitN(s.Run, "\n", 2)[0])
	if len(line) > 40 {
		line = line[:39] + "…"
	}
	return line
}

type Update struct {
	Steps        []Step `yaml:"steps"`
	Backup       Backup `yaml:"backup"`
	Migrations   string `yaml:"migrations"`
	Restart      string `yaml:"restart"`
	KeepReleases int    `yaml:"keep_releases"`
}

type Backup struct {
	Paths []string `yaml:"paths"`
	Keep  int      `yaml:"keep"`
}

type Runtime struct {
	Type    string  `yaml:"type"`
	Systemd Systemd `yaml:"systemd"`
	Compose Compose `yaml:"compose"`
}

type Systemd struct {
	Unit       string `yaml:"unit"`
	Exec       string `yaml:"exec"`
	User       string `yaml:"user"`
	Restart    string `yaml:"restart"`
	RestartSec int    `yaml:"restart_sec"`
}

type Compose struct {
	File  string `yaml:"file"`
	Pull  bool   `yaml:"pull"`
	Build bool   `yaml:"build"`
}

type Health struct {
	HTTP           string `yaml:"http"`
	Cmd            string `yaml:"cmd"`
	Timeout        string `yaml:"timeout"`
	RollbackOnFail *bool  `yaml:"rollback_on_fail"`
}

func (h *Health) Rollback() bool {
	return h == nil || h.RollbackOnFail == nil || *h.RollbackOnFail
}

type Hooks struct {
	PreInstall   string `yaml:"pre_install"`
	PostInstall  string `yaml:"post_install"`
	PreUpdate    string `yaml:"pre_update"`
	PostUpdate   string `yaml:"post_update"`
	PreUninstall string `yaml:"pre_uninstall"`
}

// Load reads a werft.yml from disk.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse is strict: unknown fields are an error, so typos do not slip through.
func Parse(data []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s is empty", FileName)
		}
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	m.applyDefaults()
	return &m, nil
}

func (m *Manifest) applyDefaults() {
	if m.Source.Channel == "" {
		m.Source.Channel = "tags"
	}
	if m.Source.Branch == "" {
		m.Source.Branch = "main"
	}
	for i := range m.Questions {
		if m.Questions[i].Type == "" {
			m.Questions[i].Type = "string"
		}
	}
	if m.Update.Restart == "" {
		m.Update.Restart = "always"
	}
	if m.Update.KeepReleases == 0 {
		m.Update.KeepReleases = 3
	}
	if m.Update.Backup.Keep == 0 {
		m.Update.Backup.Keep = 5
	}
	if m.Runtime.Type == "" {
		m.Runtime.Type = "none"
	}
	if m.Runtime.Systemd.Restart == "" {
		m.Runtime.Systemd.Restart = "on-failure"
	}
	if m.Runtime.Systemd.RestartSec == 0 {
		m.Runtime.Systemd.RestartSec = 5
	}
	if m.Runtime.Compose.File == "" {
		m.Runtime.Compose.File = "docker-compose.yml"
	}
	if m.Health != nil && m.Health.Timeout == "" {
		m.Health.Timeout = "30s"
	}
}

// Question looks up a question by ID.
func (m *Manifest) Question(id string) (Question, bool) {
	for _, q := range m.Questions {
		if q.ID == id {
			return q, true
		}
	}
	return Question{}, false
}

// Render replaces {{ .id }} with answers. Missing keys are an error.
func Render(text string, data map[string]string) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	t, err := template.New("").Option("missingkey=error").Parse(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}
