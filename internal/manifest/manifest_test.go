package manifest

import (
	"strings"
	"testing"
)

const example = `
schema: 1
name: phone
questions:
  - id: web_ui
    prompt: Web UI?
    type: bool
    default: true
  - id: web_port
    prompt: Port
    type: port
    default: 8080
    when: web_ui
  - id: api_key
    type: password
    generate: random(32)
install:
  - python3 -m venv .venv
  - name: Packages
    run: .venv/bin/pip install -r requirements.txt
runtime:
  type: systemd
  systemd:
    exec: .venv/bin/python -m phone
health:
  http: http://localhost:{{ .web_port }}/health
`

func TestParseAndDefaults(t *testing.T) {
	m, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	if ps := m.Validate(); len(ps) > 0 {
		t.Fatalf("unexpected problems: %v", ps)
	}
	if m.Source.Channel != "tags" || m.Update.KeepReleases != 3 || m.Health.Timeout != "30s" {
		t.Errorf("defaults missing: %+v", m)
	}
	if m.Questions[0].Default != "true" || m.Questions[1].Default != "8080" {
		t.Errorf("default should be a string, got %q / %q", m.Questions[0].Default, m.Questions[1].Default)
	}
	if m.Install[0].Run != "python3 -m venv .venv" || m.Install[1].Label() != "Packages" {
		t.Errorf("steps read wrong: %+v", m.Install)
	}
	if !m.Questions[2].IsSecret() || m.Questions[1].EnvName() != "WEB_PORT" {
		t.Error("IsSecret/EnvName wrong")
	}
}

func TestUnknownField(t *testing.T) {
	_, err := Parse([]byte("schema: 1\nname: x\nruntme:\n  type: none\n"))
	if err == nil || !strings.Contains(err.Error(), "runtme") {
		t.Fatalf("a typo should be reported, error: %v", err)
	}
}

func TestValidateFindsMistakes(t *testing.T) {
	m, err := Parse([]byte(`
schema: 1
name: Bad Name
questions:
  - id: a
    prompt: A
    type: choice
  - id: b
    prompt: B
    when: c
  - id: b
    prompt: B2
    type: port
    default: 99999
runtime:
  type: systemd
health:
  http: x
  cmd: y
`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range m.Validate() {
		got[p.Field] = true
	}
	for _, f := range []string{"name", "questions[0].choices", "questions[1].when", "questions[2].id", "questions[2].default", "runtime.systemd.exec", "health"} {
		if !got[f] {
			t.Errorf("problem in %s not reported (reported: %v)", f, got)
		}
	}
}

func TestCheckAnswer(t *testing.T) {
	cases := []struct {
		q    Question
		in   string
		want string
		ok   bool
	}{
		{Question{Type: "bool"}, "yes", "true", true},
		{Question{Type: "bool"}, "N", "false", true},
		{Question{Type: "bool"}, "maybe", "", false},
		{Question{Type: "port"}, "70000", "", false},
		{Question{Type: "choice", Choices: []string{"hermes", "ollama"}}, "2", "ollama", true},
		{Question{Type: "choice", Choices: []string{"hermes"}}, "claude", "", false},
		{Question{Type: "url"}, "http://x.local:8080", "http://x.local:8080", true},
		{Question{Type: "url"}, "x.local", "", false},
		{Question{Type: "string", Validate: `^[a-z]+$`}, "abc", "abc", true},
		{Question{Type: "string", Validate: `^[a-z]+$`}, "ABC", "", false},
	}
	for _, c := range cases {
		got, err := CheckAnswer(c.q, c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("%s %q: got %q, %v", c.q.Type, c.in, got, err)
		}
	}
}

func TestEvalWhen(t *testing.T) {
	a := map[string]string{"web_ui": "true", "brain": "ollama"}
	cases := map[string]bool{
		"":                  true,
		"web_ui":            true,
		"!web_ui":           false,
		"brain == ollama":   true,
		`brain == "hermes"`: false,
		"brain != hermes":   true,
	}
	for expr, want := range cases {
		got, err := EvalWhen(expr, a)
		if err != nil || got != want {
			t.Errorf("%q: %v, %v", expr, got, err)
		}
	}
}

func TestRender(t *testing.T) {
	s, err := Render("http://localhost:{{ .web_port }}/", map[string]string{"web_port": "8080"})
	if err != nil || s != "http://localhost:8080/" {
		t.Fatalf("%q %v", s, err)
	}
	if _, err := Render("{{ .missing }}", map[string]string{}); err == nil {
		t.Fatal("a missing key must be an error")
	}
}
