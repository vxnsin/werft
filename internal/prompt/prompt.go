// Package prompt asks the questions from werft.yml.
package prompt

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"

	"github.com/vxnsin/werft/internal/manifest"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

type Asker struct {
	In          *bufio.Reader
	Out         io.Writer
	Interactive bool              // false: never ask, only presets and defaults
	Preset      map[string]string // from --answers
	// Reconfigure asks known answers again, with the old one as the suggestion.
	Reconfigure bool
	ReadSecret  func() (string, error)
	// Claim answers a question marked warden: true from the outside, e.g. with
	// a port from warden. false means: ask as usual.
	Claim func(q manifest.Question, preferred string) (string, bool)
}

func New(in io.Reader, out io.Writer, interactive bool) *Asker {
	br, ok := in.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(in)
	}
	a := &Asker{In: br, Out: out, Interactive: interactive}
	a.ReadSecret = func() (string, error) {
		if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
			b, err := term.ReadPassword(int(f.Fd()))
			fmt.Fprintln(out)
			return string(b), err
		}
		return a.readLine()
	}
	return a
}

// LoadAnswers reads an answers file (id: value) for unattended installs.
func LoadAnswers(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = fmt.Sprint(v)
	}
	return out, nil
}

func (a *Asker) preset(q manifest.Question) (string, bool) {
	if v, ok := a.Preset[q.ID]; ok {
		return v, true
	}
	return os.LookupEnv("WERFT_ANSWER_" + strings.ToUpper(q.ID))
}

// Ask returns answers for all questions. Whatever is in known is kept
// (unless Reconfigure is set). known itself is not changed.
func (a *Asker) Ask(qs []manifest.Question, known map[string]string) (map[string]string, error) {
	result := map[string]string{}
	for _, q := range qs {
		active, err := manifest.EvalWhen(q.When, result)
		if err != nil {
			return nil, err
		}
		old, haveOld := known[q.ID]
		if !active {
			// Inactive questions get their default so templates still find the key.
			if haveOld {
				result[q.ID] = old
			} else {
				result[q.ID] = q.Default
			}
			continue
		}
		if haveOld && old != "" && !a.Reconfigure {
			result[q.ID] = old
			continue
		}
		if v, ok := a.preset(q); ok {
			nv, err := manifest.CheckAnswer(q, v)
			if err != nil {
				return nil, fmt.Errorf("answer for %s: %w", q.ID, err)
			}
			result[q.ID] = nv
			continue
		}
		if q.Warden && a.Claim != nil {
			pref := q.Default
			if haveOld && old != "" {
				pref = old
			}
			if v, ok := a.Claim(q, pref); ok {
				result[q.ID] = v
				continue
			}
		}
		if q.Generate != "" && !(a.Reconfigure && haveOld) {
			n, _ := manifest.GenerateLen(q.Generate)
			s, err := randomString(n)
			if err != nil {
				return nil, err
			}
			result[q.ID] = s
			continue
		}
		if q.Generate != "" {
			result[q.ID] = old
			continue
		}
		def := q.Default
		if haveOld && old != "" {
			def = old
		}
		if !a.Interactive {
			if def == "" && q.Optional {
				result[q.ID] = ""
				continue
			}
			if def == "" {
				return nil, fmt.Errorf("question %q needs an answer: --answers file.yml or WERFT_ANSWER_%s",
					q.ID, strings.ToUpper(q.ID))
			}
			nv, err := manifest.CheckAnswer(q, def)
			if err != nil {
				return nil, fmt.Errorf("default for %s: %w", q.ID, err)
			}
			result[q.ID] = nv
			continue
		}
		v, err := a.askOne(q, def, haveOld && q.IsSecret())
		if err != nil {
			return nil, err
		}
		result[q.ID] = v
	}
	return result, nil
}

func (a *Asker) askOne(q manifest.Question, def string, keepSecret bool) (string, error) {
	if q.Help != "" {
		fmt.Fprintf(a.Out, "  %s\n", q.Help)
	}
	if q.Type == "choice" {
		for i, c := range q.Choices {
			fmt.Fprintf(a.Out, "    %d) %s\n", i+1, c)
		}
	}
	for {
		fmt.Fprintf(a.Out, "  %s%s: ", q.Prompt, hint(q, def, keepSecret))
		var (
			line string
			err  error
		)
		if q.IsSecret() {
			line, err = a.ReadSecret()
		} else {
			line, err = a.readLine()
		}
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(line) == "" {
			if def == "" && q.Optional {
				return "", nil
			}
			if def == "" {
				fmt.Fprintln(a.Out, "    please enter something")
				continue
			}
			line = def
		}
		v, err := manifest.CheckAnswer(q, line)
		if err != nil {
			fmt.Fprintf(a.Out, "    %v\n", err)
			continue
		}
		return v, nil
	}
}

func hint(q manifest.Question, def string, keepSecret bool) string {
	switch {
	case keepSecret:
		return " [unchanged]"
	case q.Optional && def == "":
		return " (optional)"
	case q.IsSecret() || def == "":
		return ""
	case q.Type == "bool" && def == "true":
		return " [Y/n]"
	case q.Type == "bool":
		return " [y/N]"
	default:
		return " [" + def + "]"
	}
}

func (a *Asker) readLine() (string, error) {
	line, err := a.In.ReadString(10)
	if err != nil && (err != io.EOF || line == "") {
		if err == io.EOF {
			return "", fmt.Errorf("input ended before all questions were answered")
		}
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomString(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = alphabet[k.Int64()]
	}
	return string(b), nil
}
