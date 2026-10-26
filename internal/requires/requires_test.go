package requires

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vxnsin/werft/internal/manifest"
)

func TestVersionOK(t *testing.T) {
	cases := []struct {
		have, op, want string
		ok             bool
	}{
		{"3.11.2", ">=", "3.11", true},
		{"3.9.18", ">=", "3.11", false},
		{"13", ">=", "12", true},
		{"12", ">", "12", false},
		{"12", "==", "12", true},
	}
	for _, c := range cases {
		if versionOK(c.have, c.op, c.want) != c.ok {
			t.Errorf("%s %s %s", c.have, c.op, c.want)
		}
	}
}

func TestOS(t *testing.T) {
	p := filepath.Join(t.TempDir(), "os-release")
	os.WriteFile(p, []byte("PRETTY_NAME=\"Debian GNU/Linux 13\"\nID=debian\nVERSION_ID=\"13\"\n"), 0o644)
	c := Checker{OSRelease: p}
	if err := c.checkOS([]string{"ubuntu>=22.04", "debian>=12"}); err != nil {
		t.Error(err)
	}
	if err := c.checkOS([]string{"debian>=14"}); err == nil || !strings.Contains(err.Error(), "debian 13") {
		t.Errorf("expected an error with the actual system: %v", err)
	}
}

func TestPortBelegt(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer l.Close()
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	err = Checker{}.Check(manifest.Requires{Ports: []string{port + "/tcp"}}, true)
	if err == nil {
		t.Skip("platform allows binding twice")
	}
	if !strings.Contains(err.Error(), "in use") {
		t.Error(err)
	}
}
