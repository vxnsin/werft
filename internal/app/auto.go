package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vxnsin/werft/internal/manifest"
	"github.com/vxnsin/werft/internal/rt"
)

// Automatic updates run through one systemd timer per app, which starts
// "werft update <name> --auto". The output goes to the journal.

func autoUnit(name string) string { return "werft-auto-" + name }

func (w *Werft) setupAuto(name string, m *manifest.Manifest) error {
	if !m.AutoUpdate.Enabled {
		return w.removeAuto(name)
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("automatic updates need systemd")
	}
	service := fmt.Sprintf(`# Written by werft.
[Unit]
Description=werft: update %s
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=%s update %s --auto
`, name, w.Self, name)
	timer := fmt.Sprintf(`# Written by werft. Schedule: auto_update.schedule in werft.yml.
[Unit]
Description=werft: update schedule for %s

[Timer]
OnCalendar=%s
RandomizedDelaySec=15m
Persistent=true

[Install]
WantedBy=timers.target
`, name, m.AutoUpdate.Schedule)

	base := filepath.Join(rt.UnitDir, autoUnit(name))
	if err := os.WriteFile(base+".service", []byte(service), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(base+".timer", []byte(timer), 0o644); err != nil {
		return err
	}
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	return systemctl("enable", "--now", "--quiet", autoUnit(name)+".timer")
}

func (w *Werft) removeAuto(name string) error {
	base := filepath.Join(rt.UnitDir, autoUnit(name))
	if _, err := os.Stat(base + ".timer"); err != nil {
		return nil
	}
	systemctl("disable", "--now", "--quiet", autoUnit(name)+".timer")
	os.Remove(base + ".timer")
	os.Remove(base + ".service")
	return systemctl("daemon-reload")
}

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}
