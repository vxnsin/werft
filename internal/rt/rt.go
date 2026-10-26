// Package rt starts and stops apps: as a systemd service, with docker compose
// or not at all (none). The name rt avoids a clash with the runtime package.
package rt

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/vxnsin/werft/internal/manifest"
)

type Context struct {
	App      string
	Current  string // /opt/werft/<name>/current
	Shared   string // /opt/werft/<name>/shared
	Manifest *manifest.Manifest
	Data     map[string]string
	EnvFile  string
}

type Runtime interface {
	// Apply brings the environment in line with werft.yml (unit file, images).
	Apply(c Context) error
	Restart(c Context) error
	Start(c Context) error
	Stop(c Context) error
	// Status is a short line like "running" or "2/3 running".
	Status(c Context) string
	Logs(c Context, follow bool, lines int) error
	Remove(c Context) error
}

func For(typ string) (Runtime, error) {
	switch typ {
	case "systemd":
		return Systemd{}, nil
	case "compose":
		return Compose{}, nil
	case "none", "":
		return None{}, nil
	}
	return nil, fmt.Errorf("unknown runtime %q", typ)
}

// command runs a program and puts its output into the error.
func command(name string, args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), text)
	}
	return text, nil
}

// passthrough sends the output straight to the terminal (for logs -f).
func passthrough(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func render(s string, c Context) (string, error) {
	return manifest.Render(s, c.Data)
}

type None struct{}

func (None) Apply(Context) error   { return nil }
func (None) Restart(Context) error { return nil }
func (None) Start(Context) error   { return nil }
func (None) Stop(Context) error    { return nil }
func (None) Status(Context) string { return "-" }
func (None) Remove(Context) error  { return nil }

func (None) Logs(Context, bool, int) error {
	return fmt.Errorf("runtime: none has no logs")
}
