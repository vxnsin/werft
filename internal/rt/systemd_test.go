package rt

import (
	"strings"
	"testing"

	"github.com/vxnsin/werft/internal/manifest"
)

func TestUnit(t *testing.T) {
	m, err := manifest.Parse([]byte(`
schema: 1
name: fas-telefon
description: Phone service
runtime:
  type: systemd
  systemd:
    exec: .venv/bin/python -m telefon --port {{ .web_port }}
    user: fas-telefon
    restart: always
`))
	if err != nil {
		t.Fatal(err)
	}
	u, err := Unit(Context{
		App:      "fas-telefon",
		Current:  "/opt/werft/fas-telefon/current",
		Manifest: m,
		Data:     map[string]string{"web_port": "8765"},
		EnvFile:  "/etc/fas-telefon/.env",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Description=Phone service",
		"WorkingDirectory=/opt/werft/fas-telefon/current\n",
		"ExecStart=/opt/werft/fas-telefon/current/.venv/bin/python -m telefon --port 8765\n",
		"EnvironmentFile=-/etc/fas-telefon/.env",
		"User=fas-telefon",
		"Restart=always\nRestartSec=5",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit does not contain %q:\n%s", want, u)
		}
	}
}

func TestExecMitPATH(t *testing.T) {
	m := &manifest.Manifest{}
	got, _ := renderExec("node server.js", Context{Current: "/c", Manifest: m})
	if got != "node server.js" {
		t.Errorf("a program without / stays as it is: %q", got)
	}
	got, _ = renderExec("./bin/app serve", Context{Current: "/c", Manifest: m})
	if got != "/c/bin/app serve" {
		t.Errorf("./ becomes absolute: %q", got)
	}
}
