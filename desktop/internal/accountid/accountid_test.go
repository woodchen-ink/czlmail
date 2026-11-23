package accountid

import "testing"

func TestJoinSplit(t *testing.T) {
	cases := []struct {
		prefix, server, local string
	}{
		{Primary, "c", "c"},
		{Primary, "", ""},
		{"s2", "c", "s2~c"},
		{"s10", "a1-b_2", "s10~a1-b_2"},
	}
	for _, c := range cases {
		if got := Join(c.prefix, c.server); got != c.local {
			t.Errorf("Join(%q, %q) = %q, want %q", c.prefix, c.server, got, c.local)
		}
		p, s := Split(c.local)
		if p != c.prefix || s != c.server {
			t.Errorf("Split(%q) = %q, %q, want %q, %q", c.local, p, s, c.prefix, c.server)
		}
	}
}
