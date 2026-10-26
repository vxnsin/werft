// Package semver compares release tags like v1.4.2.
package semver

import (
	"slices"
	"strconv"
	"strings"
)

type Version struct {
	Major, Minor, Patch int
	Pre                 string
	Raw                 string
}

// Parse accepts v1.2.3, 1.2.3 and v1.2.3-rc.1. Anything else is not a release.
func Parse(s string) (Version, bool) {
	v := Version{Raw: s}
	rest := strings.TrimPrefix(s, "v")
	rest, v.Pre, _ = strings.Cut(rest, "-")
	if i := strings.IndexByte(rest, '+'); i >= 0 {
		rest = rest[:i]
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (len(p) > 1 && p[0] == '0') {
			return Version{}, false
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, true
}

// Compare returns -1, 0 or 1. Pre-releases sort before the final version.
func Compare(a, b Version) int {
	for _, d := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.Pre == b.Pre:
		return 0
	case a.Pre == "":
		return 1
	case b.Pre == "":
		return -1
	case a.Pre < b.Pre:
		return -1
	default:
		return 1
	}
}

// Allowed reports whether policy permits an update from one version to another.
func Allowed(policy string, from, to Version) bool {
	switch policy {
	case "patch":
		return to.Major == from.Major && to.Minor == from.Minor
	case "minor":
		return to.Major == from.Major
	default:
		return true
	}
}

// Latest finds the highest tag that is newer than current
// and fits the policy. Pre-releases only count with pre=true.
func Latest(tags []string, current string, policy string, pre bool) (string, bool) {
	cur, haveCur := Parse(current)
	var best Version
	found := false
	for _, t := range tags {
		v, ok := Parse(t)
		if !ok || (v.Pre != "" && !pre) {
			continue
		}
		if haveCur && (Compare(v, cur) <= 0 || !Allowed(policy, cur, v)) {
			continue
		}
		if !found || Compare(v, best) > 0 {
			best, found = v, true
		}
	}
	return best.Raw, found
}

// Sort sorts tags in descending order and drops anything that is not a version.
func Sort(tags []string) []string {
	var vs []Version
	for _, t := range tags {
		if v, ok := Parse(t); ok {
			vs = append(vs, v)
		}
	}
	slices.SortFunc(vs, func(a, b Version) int { return Compare(b, a) })
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Raw
	}
	return out
}
