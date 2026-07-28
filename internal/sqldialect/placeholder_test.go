package sqldialect

import "testing"

func TestRebind(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT 1", "SELECT 1"},
		{"WHERE a = ?", "WHERE a = $1"},
		{"VALUES (?, ?, ?)", "VALUES ($1, $2, $3)"},
		{"a = ? AND b = ? LIMIT ?", "a = $1 AND b = $2 LIMIT $3"},
	}
	for _, c := range cases {
		if got := Rebind(c.in); got != c.want {
			t.Errorf("Rebind(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRebindLeavesQuotedQuestionMark(t *testing.T) {
	in := "SELECT '?' AS lit WHERE a = ?"
	want := "SELECT '?' AS lit WHERE a = $1"
	if got := Rebind(in); got != want {
		t.Errorf("Rebind(%q) = %q, want %q", in, got, want)
	}
}
