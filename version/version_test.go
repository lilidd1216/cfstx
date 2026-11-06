package version

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current string
		latest  string
		want    bool
	}{
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.0", "v1.1.0", true},
		{"v1.0.0", "v2.0.0", true},
		{"v1.0.0", "v1.0.0", false},
		{"v1.0.0", "v0.9.0", false},
		{"v1.0.0", "v0.9.9", false},
		{"1.0.0", "1.0.1", true},
		{"v1.2.3", "v1.2.10", true},
		{"v2.0.0", "v1.9.9", false},
	}

	for _, c := range cases {
		if got := IsNewer(c.current, c.latest); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		input string
		want  [3]int
	}{
		{"v1.2.3", [3]int{1, 2, 3}},
		{"1.2.3", [3]int{1, 2, 3}},
		{"v1.0", [3]int{1, 0, 0}},
		{"v2", [3]int{2, 0, 0}},
	}

	for _, c := range cases {
		if got := parseVersion(c.input); got != c.want {
			t.Errorf("parseVersion(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}
