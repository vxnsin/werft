// Package audit looks for the usual tricks in everything werft is about to
// run: build steps, hooks, migrations, the start command and npm lifecycle
// scripts. It is a pattern check, not an antivirus: it catches downloads
// piped into a shell, reverse shells, hidden characters, writes to ssh keys
// or cron, and similar things. A carefully written backdoor gets past it.
package audit

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type Level int

const (
	Note Level = iota
	Warning
	Critical
)

func (l Level) String() string {
	switch l {
	case Critical:
		return "critical"
	case Warning:
		return "warning"
	}
	return "note"
}

// Mark is the one-character prefix used in the output.
func (l Level) Mark() string {
	switch l {
	case Critical:
		return "!"
	case Warning:
		return "~"
	}
	return "·"
}

type Finding struct {
	Level Level
	Where string // file or werft.yml field
	Line  int    // 1-based, 0 when the source is a single value
	Rule  string
	Text  string // the offending line, trimmed
}

func (f Finding) String() string {
	where := f.Where
	if f.Line > 0 {
		where += ":" + fmt.Sprint(f.Line)
	}
	return f.Level.Mark() + " " + where + ": " + f.Rule
}

// Source is one piece of text to check.
type Source struct {
	Where string
	Text  string
}

type rule struct {
	re    *regexp.Regexp
	level Level
	msg   string
}

var rules = []rule{
	{regexp.MustCompile(`\b(curl|wget)\b[^|;&\n]*\|\s*(sudo\s+)?(ba|z|da|k)?sh\b`), Critical, "downloads a script and runs it unseen"},
	{regexp.MustCompile(`base64\s+(-d|--decode)[^|\n]*\|\s*(sudo\s+)?(ba|z|da|k)?sh\b|\beval\b[^\n]*base64|\bxxd\s+-r[^\n]*\|\s*(ba)?sh`), Critical, "runs decoded, hidden commands"},
	{regexp.MustCompile(`\beval\s+["$]`), Warning, "eval of a computed string"},
	{regexp.MustCompile(`/dev/tcp/|\bnc\b[^\n]*\s-e\s|\bncat\b[^\n]*\s-e\s|\bsocat\b[^\n]*exec|bash\s+-i\s*>&`), Critical, "looks like a reverse shell"},
	{regexp.MustCompile(`\.ssh/|authorized_keys|/etc/sudoers|/etc/passwd|/etc/shadow|ld\.so\.preload|/etc/rc\.local`), Critical, "touches ssh keys, sudoers or system accounts"},
	{regexp.MustCompile(`/etc/cron|\bcrontab\b|/etc/systemd/|\bsystemctl\s+(enable|edit|link)\b`), Warning, "sets up something that keeps running outside werft"},
	{regexp.MustCompile(`\.bashrc|\.profile|\.zshrc|/etc/profile|/etc/environment`), Warning, "edits shell startup files"},
	{regexp.MustCompile(`\bchmod\s+(\+s|[ugo]*\+[rwx]*s|[2467][0-7]{3})\b|\bsetcap\b`), Critical, "sets setuid bits or capabilities"},
	{regexp.MustCompile(`\b(useradd|adduser|usermod|passwd|chpasswd)\b`), Warning, "changes user accounts"},
	{regexp.MustCompile(`\brm\s+-\w*[rf]\w*\s+(/\*?|~/?|\$HOME/?)(\s|$)|\bmkfs\b|\bdd\s+if=`), Critical, "destroys data outside the project"},
	{regexp.MustCompile(`\b(sudo|doas)\b|\bsu\s+-`), Note, "asks for more rights, but werft already runs as root"},
	{regexp.MustCompile(`\b(printenv|env)\s*(\||>)|/proc/self/environ|\bcat\s+[^\n]*\.(env|pem|key)\b[^\n]*\|`), Critical, "reads secrets and passes them on"},
	{regexp.MustCompile(`\bcurl\b[^\n]*(-d\s*@|--data-binary\s*@|-T\s|--upload-file)|\bscp\b|\brsync\b[^\n]*\s\S+:`), Warning, "uploads files somewhere"},
	{regexp.MustCompile(`\bnohup\b|\bsetsid\b|\bdisown\b|[^&]&\s*$`), Note, "starts something in the background"},
	{regexp.MustCompile(`\b(iptables|nft|ufw)\b`), Warning, "changes the firewall"},
}

// hiddenRune reports characters that do not show in an editor but change
// what runs: zero-width and bidirectional controls, and raw control codes.
func hiddenRune(r rune) bool {
	switch {
	case r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E,
		r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x2069, r == 0xFEFF:
		return true
	case r < 0x20 && r != '\t' && r != '\n' && r != '\r':
		return true
	}
	return false
}

// Scan checks all sources and returns the findings, worst first.
func Scan(sources []Source) []Finding {
	var out []Finding
	for _, s := range sources {
		lines := strings.Split(s.Text, "\n")
		multi := len(lines) > 1
		for i, line := range lines {
			n := 0
			if multi {
				n = i + 1
			}
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			for _, r := range line {
				if hiddenRune(r) {
					out = append(out, Finding{Critical, s.Where, n, "hidden or bidirectional characters", fmt.Sprintf("%q", trimmed)})
					break
				}
			}
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			for _, ru := range rules {
				if ru.re.MatchString(line) {
					out = append(out, Finding{ru.level, s.Where, n, ru.msg, trimmed})
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Level > out[j].Level })
	return out
}

// Count returns how many findings have at least the given level.
func Count(fs []Finding, min Level) int {
	n := 0
	for _, f := range fs {
		if f.Level >= min {
			n++
		}
	}
	return n
}
