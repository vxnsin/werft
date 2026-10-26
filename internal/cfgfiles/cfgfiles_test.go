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
		{ID: "sip_server"}, {ID: "pw", Env: "SIP_PASSWORD"}, {ID: "missing"}, {ID: "empty"},
	}}
	answers := map[string]string{"sip_server": "a", "pw": "b c", "empty": ""}
	fixed := map[string]string{"NODE_ENV": "production", "A_FIRST": "1"}
	got := strings.Join(EnvLines(m, answers, fixed), "\n")
	if got != "SIP_SERVER=a\nSIP_PASSWORD=\"b c\"\nA_FIRST=1\nNODE_ENV=production" {
		t.Errorf("%q", got)
	}
}
