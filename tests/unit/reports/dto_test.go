package reports_test

import (
	"testing"
	"time"

	"github.com/pmaxis/helpic/internal/reports"
)

func TestDefaultTitle(t *testing.T) {
	day := func(s string) time.Time { d, _ := time.Parse(time.DateOnly, s); return d }
	cases := []struct{ project, from, to, want string }{
		{"", "2026-10-01", "2026-10-31", "Work report — Oct 2026"},
		{"Website", "2026-02-01", "2026-02-28", "Website — Feb 2026"},
		{"", "2026-10-01", "2026-10-15", "Work report — 1 Oct – 15 Oct 2026"},
	}
	for _, c := range cases {
		if got := reports.DefaultTitle(c.project, day(c.from), day(c.to)); got != c.want {
			t.Errorf("reports.DefaultTitle(%q, %s, %s) = %q, want %q", c.project, c.from, c.to, got, c.want)
		}
	}
}
