package rt

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// UnitDir can be changed in tests.
var UnitDir = "/etc/systemd/system"

type Systemd struct{}

func unitName(c Context) string {
	if u := c.Manifest.Runtime.Systemd.Unit; u != "" {
		return u
	}
	return c.App
}

// Unit builds the content of the unit file.
func Unit(c Context) (string, error) {
	s := c.Manifest.Runtime.Systemd
	exec, err := renderExec(s.Exec, c)
	if err != nil {
		return "", err
	}
	desc := c.Manifest.Description
	if desc == "" {
		desc = c.App
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Written by werft. Changes belong in the werft.yml of %s.\n", c.App)
	fmt.Fprintf(&b, "[Unit]\nDescription=%s\nAfter=network-online.target\nWants=network-online.target\n\n", desc)
	fmt.Fprintf(&b, "[Service]\nType=simple\nWorkingDirectory=%s\nExecStart=%s\n", c.Current, exec)
	if c.EnvFile != "" {
		fmt.Fprintf(&b, "EnvironmentFile=-%s\n", c.EnvFile)
	}
	if s.User != "" {
		fmt.Fprintf(&b, "User=%s\nGroup=%s\n", s.User, s.User)
	}
	fmt.Fprintf(&b, "Restart=%s\nRestartSec=%d\n\n[Install]\nWantedBy=multi-user.target\n", s.Restart, s.RestartSec)
	return b.String(), nil
}

// renderExec fills in answers and makes relative program paths absolute:
// .venv/bin/python -m app becomes /opt/werft/app/current/.venv/bin/python -m app.
func renderExec(exec string, c Context) (string, error) {
	out, err := render(exec, c)
	if err != nil {
		return "", fmt.Errorf("runtime.systemd.exec: %w", err)
	}
	fields := strings.Fields(out)
	if len(fields) > 0 && strings.Contains(fields[0], "/") && !strings.HasPrefix(fields[0], "/") {
		prog := strings.TrimPrefix(fields[0], "./")
		out = c.Current + "/" + prog + strings.TrimPrefix(out, fields[0])
	}
	return out, nil
}

func (Systemd) Apply(c Context) error {
	if u := c.Manifest.Runtime.Systemd.User; u != "" {
		if err := EnsureUser(u); err != nil {
			return err
		}
		if _, err := os.Stat(c.Shared); err == nil {
			if _, err := command("chown", "-R", u+":"+u, c.Shared); err != nil {
				return err
			}
		}
	}
	content, err := Unit(c)
	if err != nil {
		return err
	}
	path := filepath.Join(UnitDir, unitName(c)+".service")
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, []byte(content)) {
		return nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	if _, err := command("systemctl", "daemon-reload"); err != nil {
		return err
	}
	_, err = command("systemctl", "enable", "--quiet", unitName(c))
	return err
}

// EnsureUser creates a system user with a group of the same name if it does not exist.
func EnsureUser(name string) error {
	if _, err := command("id", "-u", name); err == nil {
		return nil
	}
	_, err := command("useradd", "--system", "--no-create-home", "--home-dir", "/nonexistent",
		"--shell", "/usr/sbin/nologin", name)
	return err
}

func (Systemd) Restart(c Context) error {
	_, err := command("systemctl", "restart", unitName(c))
	return err
}

func (Systemd) Start(c Context) error {
	_, err := command("systemctl", "start", unitName(c))
	return err
}

func (Systemd) Stop(c Context) error {
	_, err := command("systemctl", "stop", unitName(c))
	return err
}

func (Systemd) Status(c Context) string {
	out, _ := command("systemctl", "is-active", unitName(c))
	switch out {
	case "active":
		return "running"
	case "inactive":
		return "stopped"
	case "failed":
		return "crashed"
	case "activating":
		return "starting"
	}
	return out
}

func (Systemd) Logs(c Context, follow bool, lines int) error {
	args := []string{"-u", unitName(c), "-n", strconv.Itoa(lines), "--no-pager"}
	if follow {
		args = append(args, "-f")
	}
	return passthrough("journalctl", args...)
}

func (Systemd) Remove(c Context) error {
	command("systemctl", "disable", "--now", "--quiet", unitName(c))
	if err := os.Remove(filepath.Join(UnitDir, unitName(c)+".service")); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err := command("systemctl", "daemon-reload")
	return err
}
