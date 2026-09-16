package update

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.0.3", "v0.0.2", true},
		{"v0.1.0", "v0.0.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.0.2", "v0.0.2", false},
		{"v0.0.1", "v0.0.2", false},
		{"v0.0.3", "dev", false},
		{"v0.0.3", "v0.0.3-rc1", false},
		{"0.0.4", "v0.0.3", true},
		{"garbage", "v0.0.3", false},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}
