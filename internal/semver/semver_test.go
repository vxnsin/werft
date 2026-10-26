package semver

import "testing"

func TestLatest(t *testing.T) {
	tags := []string{"v1.4.2", "v1.4.3", "v1.5.0", "v2.0.0", "v2.1.0-rc.1", "nightly", "v1.4.10"}
	cases := []struct {
		current, policy, want string
	}{
		{"v1.4.2", "patch", "v1.4.10"},
		{"v1.4.2", "minor", "v1.5.0"},
		{"v1.4.2", "major", "v2.0.0"},
		{"", "patch", "v2.0.0"},
		{"v2.0.0", "major", ""},
	}
	for _, c := range cases {
		got, _ := Latest(tags, c.current, c.policy, false)
		if got != c.want {
			t.Errorf("%s/%s: %q, want %q", c.current, c.policy, got, c.want)
		}
	}
	if got, _ := Latest(tags, "v2.0.0", "major", true); got != "v2.1.0-rc.1" {
		t.Errorf("with pre-releases: %q", got)
	}
}

func TestParse(t *testing.T) {
	for _, s := range []string{"1.2", "v1.2.x", "v01.2.3", "release-1"} {
		if _, ok := Parse(s); ok {
			t.Errorf("%q should not be a version", s)
		}
	}
	a, _ := Parse("v1.0.0-rc.1")
	b, _ := Parse("v1.0.0")
	if Compare(a, b) != -1 {
		t.Error("rc must sort before the release")
	}
}

func TestSort(t *testing.T) {
	got := Sort([]string{"v1.2.0", "x", "v1.10.0", "v1.9.9"})
	want := []string{"v1.10.0", "v1.9.9", "v1.2.0"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
}
