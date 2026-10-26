package manifest

import (
	"fmt"
	"strings"
)

// Conditions for when: "id", "!id", "id == value", "id != value".

type cond struct {
	id    string
	op    string // "", "!", "==", "!="
	value string
}

func parseWhen(expr string) (cond, error) {
	expr = strings.TrimSpace(expr)
	for _, op := range []string{"==", "!="} {
		if l, r, ok := strings.Cut(expr, op); ok {
			id := strings.TrimSpace(l)
			val := strings.Trim(strings.TrimSpace(r), "\x22\x27")
			if !idRe.MatchString(id) {
				return cond{}, fmt.Errorf("when %q: the left side must be a question id", expr)
			}
			return cond{id: id, op: op, value: val}, nil
		}
	}
	c := cond{id: expr}
	if strings.HasPrefix(expr, "!") {
		c = cond{id: strings.TrimSpace(expr[1:]), op: "!"}
	}
	if !idRe.MatchString(c.id) {
		return cond{}, fmt.Errorf("when %q: expected id, !id, id == value or id != value", expr)
	}
	return c, nil
}

// WhenRefs returns the questions a condition refers to.
func WhenRefs(expr string) ([]string, error) {
	c, err := parseWhen(expr)
	if err != nil {
		return nil, err
	}
	return []string{c.id}, nil
}

// EvalWhen evaluates a condition against the answers so far. Empty means always.
func EvalWhen(expr string, answers map[string]string) (bool, error) {
	if strings.TrimSpace(expr) == "" {
		return true, nil
	}
	c, err := parseWhen(expr)
	if err != nil {
		return false, err
	}
	v := answers[c.id]
	switch c.op {
	case "":
		return v == "true", nil
	case "!":
		return v != "true", nil
	case "==":
		return v == c.value, nil
	default:
		return v != c.value, nil
	}
}
