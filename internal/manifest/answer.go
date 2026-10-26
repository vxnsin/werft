package manifest

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// CheckAnswer checks an answer against type and validate and returns it normalized
// (bool is always true/false, a choice may also be given as its number).
func CheckAnswer(q Question, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", fmt.Errorf("must not be empty")
	}
	switch q.Type {
	case "int":
		if _, err := strconv.Atoi(v); err != nil {
			return "", fmt.Errorf("%q is not a whole number", v)
		}
	case "port":
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("%q is not a port (1-65535)", v)
		}
	case "bool":
		switch strings.ToLower(v) {
		case "y", "yes", "true", "1":
			v = "true"
		case "n", "no", "false", "0":
			v = "false"
		default:
			return "", fmt.Errorf("%q: yes or no", v)
		}
	case "choice":
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(q.Choices) {
			v = q.Choices[n-1]
		}
		if !slices.Contains(q.Choices, v) {
			return "", fmt.Errorf("%q: allowed are %s", v, strings.Join(q.Choices, ", "))
		}
	case "url":
		u, err := url.Parse(v)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return "", fmt.Errorf("%q is not a complete URL", v)
		}
	case "path":
		if !strings.HasPrefix(v, "/") {
			return "", fmt.Errorf("%q: please use an absolute path", v)
		}
	}
	if q.Validate != "" {
		re, err := regexp.Compile(q.Validate)
		if err != nil {
			return "", err
		}
		if !re.MatchString(v) {
			return "", fmt.Errorf("%q does not match %s", v, q.Validate)
		}
	}
	return v, nil
}
