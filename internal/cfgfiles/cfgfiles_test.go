package cfgfiles

import (
	"strings"
	"testing"

	"github.com/vxnsin/werft/internal/manifest"
)

func TestQuote(t *testing.T) {
	cases := map[string]string{
		"sip.local":       "sip.local",
		"http://x:8080/a": "http://x:8080/a",
		"with spaces":     `"with spaces"`,
		`say "hello"`:     `"say \"hello\""`,
		"price $5":        `"price \$5"`,
		"":                `""`,
		"two\nlines":      `"two\nlines"`,
		`C:\path`:         `"C:\\path"`,
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestEnvLines(t *testing.T) {
	m := &manifest.Manifest{Questions: []manifest.Question{
		{ID: "sip_server"}, {ID: "pw", Env: "SIP_PASSWORD"}, {ID: "missing"},
	}}
	got := strings.Join(EnvLines(m, map[string]string{"sip_server": "a", "pw": "b c"}), "\n")
	if got != "SIP_SERVER=a\nSIP_PASSWORD=\"b c\"" {
		t.Errorf("%q", got)
	}
}
