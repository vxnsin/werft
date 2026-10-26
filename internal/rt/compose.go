package rt

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

type Compose struct{}

func composeArgs(c Context, args ...string) []string {
	file := filepath.Join(c.Current, filepath.FromSlash(c.Manifest.Runtime.Compose.File))
	base := []string{"compose", "-p", c.App, "-f", file}
	if c.EnvFile != "" {
		base = append(base, "--env-file", c.EnvFile)
	}
	return append(base, args...)
}

func (Compose) Apply(c Context) error {
	if !c.Manifest.Runtime.Compose.Pull {
		return nil
	}
	_, err := command("docker", composeArgs(c, "pull", "--quiet")...)
	return err
}

func (Compose) Restart(c Context) error {
	args := []string{"up", "-d", "--remove-orphans"}
	if c.Manifest.Runtime.Compose.Build {
		args = append(args, "--build")
	}
	_, err := command("docker", composeArgs(c, args...)...)
	return err
}

func (Compose) Start(c Context) error {
	_, err := command("docker", composeArgs(c, "up", "-d")...)
	return err
}

func (Compose) Stop(c Context) error {
	_, err := command("docker", composeArgs(c, "stop")...)
	return err
}

func (Compose) Status(c Context) string {
	all, err := command("docker", composeArgs(c, "config", "--services")...)
	if err != nil {
		return "unknown"
	}
	running, _ := command("docker", composeArgs(c, "ps", "--services", "--status", "running")...)
	total, up := len(lines(all)), len(lines(running))
	if up == total {
		return "running"
	}
	return fmt.Sprintf("%d/%d running", up, total)
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func (Compose) Logs(c Context, follow bool, n int) error {
	args := []string{"logs", "--tail", strconv.Itoa(n)}
	if follow {
		args = append(args, "-f")
	}
	return passthrough("docker", composeArgs(c, args...)...)
}

func (Compose) Remove(c Context) error {
	_, err := command("docker", composeArgs(c, "down", "--remove-orphans")...)
	return err
}
