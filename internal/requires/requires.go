// Package requires checks whether the server has what the app needs.
package requires

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/vxnsin/werft/internal/manifest"
)

type Checker struct {
	// Confirm asks before apt-get installs anything. Nil means: do not install.
	Confirm func(question string) bool
	// OSRelease is the path to os-release (for tests).
	OSRelease string
}

// Check checks everything in requires. Ports only on install; during an update
// the running app holds them itself.
func (c Checker) Check(r manifest.Requires, install bool) error {
	var errs []error
	if r.Root && runtime.GOOS != "windows" && os.Geteuid() != 0 {
		errs = append(errs, errors.New("needs root: sudo werft ..."))
	}
	if len(r.OS) > 0 {
		if err := c.checkOS(r.OS); err != nil {
			errs = append(errs, err)
		}
	}
	for _, spec := range r.Commands {
		if err := checkCommand(spec); err != nil {
			errs = append(errs, err)
		}
	}
	if len(r.Apt) > 0 {
		if err := c.checkApt(r.Apt); err != nil {
			errs = append(errs, err)
		}
	}
	if install {
		for _, spec := range r.Ports {
			if err := checkPort(spec); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

var specRe = regexp.MustCompile(`^([a-zA-Z0-9._+-]+?)\s*(>=|==|>)?\s*([0-9][0-9.]*)?$`)

func splitSpec(spec string) (name, op, version string, err error) {
	m := specRe.FindStringSubmatch(strings.TrimSpace(spec))
	if m == nil {
		return "", "", "", fmt.Errorf("%q: expected name or name>=1.2", spec)
	}
	return m[1], m[2], m[3], nil
}

func (c Checker) checkOS(specs []string) error {
	path := c.OSRelease
	if path == "" {
		path = "/etc/os-release"
	}
	id, ver, err := readOSRelease(path)
	if err != nil {
		return fmt.Errorf("cannot detect the operating system: %w", err)
	}
	for _, s := range specs {
		name, op, want, err := splitSpec(s)
		if err != nil {
			return err
		}
		if name == id && (op == "" || versionOK(ver, op, want)) {
			return nil
		}
	}
	return fmt.Errorf("running %s %s, required is %s", id, ver, strings.Join(specs, " or "))
}

func readOSRelease(path string) (id, version string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, "\"")
		switch k {
		case "ID":
			id = v
		case "VERSION_ID":
			version = v
		}
	}
	return id, version, sc.Err()
}

var versionRe = regexp.MustCompile(`\d+(\.\d+)+|\d+`)

func checkCommand(spec string) error {
	name, op, want, err := splitSpec(spec)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("program %s is missing", name)
	}
	if op == "" {
		return nil
	}
	out, _ := exec.Command(name, "--version").CombinedOutput()
	have := versionRe.FindString(string(out))
	if have == "" {
		return fmt.Errorf("cannot read the version of %s", name)
	}
	if !versionOK(have, op, want) {
		return fmt.Errorf("%s is %s, required %s%s", name, have, op, want)
	}
	return nil
}

// versionOK compares versions part by part as numbers: 3.11 > 3.9.
func versionOK(have, op, want string) bool {
	h, w := strings.Split(have, "."), strings.Split(want, ".")
	cmp := 0
	for i := 0; i < len(w) && cmp == 0; i++ {
		var hv int
		if i < len(h) {
			hv, _ = strconv.Atoi(h[i])
		}
		wv, _ := strconv.Atoi(w[i])
		switch {
		case hv > wv:
			cmp = 1
		case hv < wv:
			cmp = -1
		}
	}
	switch op {
	case ">=":
		return cmp >= 0
	case ">":
		return cmp > 0
	default:
		return cmp == 0
	}
}

func (c Checker) checkApt(pkgs []string) error {
	if _, err := exec.LookPath("dpkg-query"); err != nil {
		return errors.New("requires.apt only works on Debian and Ubuntu")
	}
	var missing []string
	for _, p := range pkgs {
		out, _ := exec.Command("dpkg-query", "-W", "-f=${Status}", p).Output()
		if !strings.Contains(string(out), "install ok installed") {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	list := strings.Join(missing, " ")
	if c.Confirm == nil || !c.Confirm("Install missing packages: "+list+"?") {
		return fmt.Errorf("packages missing: %s (apt-get install %s)", list, list)
	}
	cmd := exec.Command("apt-get", append([]string{"install", "-y", "--no-install-recommends"}, missing...)...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apt-get install %s: %s", list, strings.TrimSpace(string(out)))
	}
	return nil
}

func checkPort(spec string) error {
	port, proto, err := manifest.ParsePort(spec)
	if err != nil {
		return err
	}
	addr := ":" + strconv.Itoa(port)
	if proto == "udp" {
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			return fmt.Errorf("port %s is in use", spec)
		}
		return pc.Close()
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %s is in use", spec)
	}
	return l.Close()
}
