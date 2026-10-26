package prompt

import (
	"strings"
	"testing"

	"github.com/vxnsin/werft/internal/manifest"
)

var questions = []manifest.Question{
	{ID: "server", Prompt: "SIP server", Type: "string"},
	{ID: "web_ui", Prompt: "Web UI?", Type: "bool", Default: "true"},
	{ID: "web_port", Prompt: "Port", Type: "port", Default: "8080", When: "web_ui"},
	{ID: "brain", Prompt: "Brain", Type: "choice", Choices: []string{"hermes", "ollama"}},
	{ID: "pw", Prompt: "Password", Type: "password"},
	{ID: "key", Type: "password", Generate: "random(24)"},
}

func TestAskInteractive(t *testing.T) {
	// The empty line for the server is rejected.
	in := strings.NewReader("\nsip.local\nn\n2\nsecret\n")
	var out strings.Builder
	a := New(in, &out, true)
	got, err := a.Ask(questions, nil)
	if err != nil {
		t.Fatal(err, out.String())
	}
	want := map[string]string{"server": "sip.local", "web_ui": "false", "web_port": "8080", "brain": "ollama", "pw": "secret"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got["key"]) != 24 {
		t.Errorf("generated key: %q", got["key"])
	}
	if !strings.Contains(out.String(), "please enter something") {
		t.Error("an empty answer should be rejected")
	}
}

func TestAskOnlyNewQuestions(t *testing.T) {
	known := map[string]string{"server": "a", "web_ui": "true", "web_port": "9000", "brain": "hermes", "pw": "x", "key": "k"}
	a := New(strings.NewReader("\n"), &strings.Builder{}, true)
	got, err := a.Ask(append(questions, manifest.Question{ID: "new", Prompt: "New", Default: "d"}), known)
	if err != nil {
		t.Fatal(err)
	}
	if got["web_port"] != "9000" || got["key"] != "k" || got["new"] != "d" {
		t.Errorf("known answers overwritten: %v", got)
	}
}

func TestAskWithoutTerminal(t *testing.T) {
	a := New(strings.NewReader(""), &strings.Builder{}, false)
	a.Preset = map[string]string{"server": "s", "brain": "1", "pw": "p"}
	got, err := a.Ask(questions, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["brain"] != "hermes" || got["web_port"] != "8080" {
		t.Errorf("%v", got)
	}
	a.Preset = map[string]string{"server": "s"}
	if _, err := a.Ask(questions, nil); err == nil || !strings.Contains(err.Error(), "WERFT_ANSWER_BRAIN") {
		t.Errorf("a missing answer must be reported: %v", err)
	}
}
