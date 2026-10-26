package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/vxnsin/werft/internal/rt"
	"github.com/vxnsin/werft/internal/state"
)

// Automatic updates are a decision of the server, not of the project: werft
// auto stores a schedule per app and sets up a systemd timer that runs
// "werft update <name> --auto". The output goes to the journal.

func autoUnit(name string) string { return "werft-auto-" + name }

var clockRe = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)

// ParseSchedule accepts a time like 02:00 (every day), hourly, daily, weekly,
// or any systemd OnCalendar expression such as "Mon..Fri 04:00".
func ParseSchedule(s string) (string, error) {
	s = strings.TrimSpace(s)
	if m := clockRe.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		if h > 23 || min > 59 {
			return "", fmt.Errorf("%q is not a time", s)
		}
		return fmt.Sprintf("*-*-* %02d:%02d", h, min), nil
	}
	if s == "" {
		return "", errors.New("the schedule is empty")
	}
	if path, err := exec.LookPath("systemd-analyze"); err == nil {
		if out, err := exec.Command(path, "calendar", s).CombinedOutput(); err != nil {
			return "", fmt.Errorf("%q is not a systemd calendar expression: %s", s, strings.TrimSpace(string(out)))
		}
	}
	return s, nil
}

// AutoSet turns on automatic updates for apps.
func (w *Werft) AutoSet(names []string, schedule, policy string) error {
	if policy == "" {
		policy = "patch"
	}
	var errs []error
	for _, name := range names {
		a, err := w.Store.Load(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		st := w.UI.Step("auto", name)
		a.Auto = &state.Auto{Schedule: schedule, Policy: policy}
		if err := w.Store.Save(a); err != nil {
			errs = append(errs, w.fail(st, err))
			continue
		}
		if err := w.writeTimer(name, schedule); err != nil {
			errs = append(errs, w.fail(st, err))
			continue
		}
		result := schedule
		if a.Channel == "tags" {
			result += " (" + policy + ")"
		}
		st.OK(result)
	}
	return errors.Join(errs...)
}

// AutoOff turns automatic updates off again.
func (w *Werft) AutoOff(names []string) error {
	var errs []error
	for _, name := range names {
		a, err := w.Store.Load(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		st := w.UI.Step("auto", name)
		a.Auto = nil
		if err := w.Store.Save(a); err != nil {
			errs = append(errs, w.fail(st, err))
			continue
		}
		if err := w.removeAuto(name); err != nil {
			errs = append(errs, w.fail(st, err))
			continue
		}
		st.OK("off")
	}
	return errors.Join(errs...)
}

// AutoList shows which apps update on their own and when.
func (w *Werft) AutoList() error {
	apps, err := w.Store.List()
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		w.UI.Printf("Nothing installed or tracked yet.\n")
		return nil
	}
	tw := tabwriter.NewWriter(w.UI.Out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSCHEDULE\tPOLICY\tNEXT RUN")
	for _, a := range apps {
		if a.Auto == nil {
			fmt.Fprintf(tw, "%s\toff\t\t\n", a.Name)
			continue
		}
		policy := a.Auto.Policy
		if a.Channel != "tags" {
			policy = "every commit"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.Name, a.Auto.Schedule, policy, nextRun(a.Name))
	}
	return tw.Flush()
}

// AutoInteractive asks which apps should update on their own and when.
func (w *Werft) AutoInteractive() error {
	apps, err := w.Store.List()
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		w.UI.Printf("Nothing installed or tracked yet.\n")
		return nil
	}
	w.UI.Printf("\n")
	for i, a := range apps {
		now := "off"
		if a.Auto != nil {
			now = a.Auto.Schedule
		}
		w.UI.Printf("  %2d) %-20s %s\n", i+1, a.Name, now)
	}
	w.UI.Printf("\n")

	line := w.ask("Which apps? Numbers like 1,3 or all", "")
	if line == "" {
		return errors.New("aborted")
	}
	var names []string
	if strings.EqualFold(line, "all") {
		for _, a := range apps {
			names = append(names, a.Name)
		}
	} else {
		for _, f := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' }) {
			n, err := strconv.Atoi(f)
			if err != nil || n < 1 || n > len(apps) {
				return fmt.Errorf("%q is not one of the numbers", f)
			}
			names = append(names, apps[n-1].Name)
		}
	}

	when := w.ask("When? A time like 02:00, a systemd calendar like Mon..Fri 04:00, or off", "02:00")
	if strings.EqualFold(when, "off") {
		w.UI.Printf("\n")
		return w.AutoOff(names)
	}
	schedule, err := ParseSchedule(when)
	if err != nil {
		return err
	}
	policy := "patch"
	for _, a := range apps {
		if a.Channel == "tags" && contains(names, a.Name) {
			policy = w.ask("How far may releases jump on their own? patch, minor or major", "patch")
			if policy != "patch" && policy != "minor" && policy != "major" {
				return fmt.Errorf("%q: use patch, minor or major", policy)
			}
			break
		}
	}
	w.UI.Printf("\n")
	return w.AutoSet(names, schedule, policy)
}

func (w *Werft) ask(question, def string) string {
	if def != "" {
		fmt.Fprintf(w.UI.Out, "  %s [%s]: ", question, def)
	} else {
		fmt.Fprintf(w.UI.Out, "  %s: ", question)
	}
	line, _ := w.In.ReadString(10)
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (w *Werft) writeTimer(name, schedule string) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("automatic updates need systemd")
	}
	service := fmt.Sprintf(`# Written by werft (werft auto).
[Unit]
Description=werft: update %s
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=%s update %s --auto
`, name, w.Self, name)
	timer := fmt.Sprintf(`# Written by werft. Change it with: werft auto set %s --at ...
[Unit]
Description=werft: update schedule for %s

[Timer]
OnCalendar=%s
RandomizedDelaySec=5m
Persistent=true

[Install]
WantedBy=timers.target
`, name, name, schedule)

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
	if err := systemctl("enable", "--quiet", autoUnit(name)+".timer"); err != nil {
		return err
	}
	return systemctl("restart", autoUnit(name)+".timer")
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

func nextRun(name string) string {
	out, err := exec.Command("systemctl", "show", autoUnit(name)+".timer", "--property=NextElapseUSecRealtime", "--value").Output()
	if err != nil {
		return "-"
	}
	if s := strings.TrimSpace(string(out)); s != "" && s != "n/a" {
		return s
	}
	return "-"
}

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}
