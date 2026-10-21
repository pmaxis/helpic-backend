package timelog_test

import (
	"testing"

	"github.com/pmaxis/helpic/internal/timelog"
)

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]int64{
		"1:30":        5400,
		" 0:05 ":      300,
		"1h 30m":      5400,
		"1h30m":       5400,
		"2h":          7200,
		"45m":         2700,
		"45 min":      2700,
		"1 год 30 хв": 5400,
		"1.5":         5400,
		"1,25":        4500,
		"0":           0,
	} {
		got, ok := timelog.ParseDuration(in)
		if !ok || got != want {
			t.Errorf("ParseDuration(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "soon", "1:75", "-1", "1x", "h", "NaN", "inf", "1.5h"} {
		if got, ok := timelog.ParseDuration(in); ok {
			t.Errorf("ParseDuration(%q) = %d, want an error", in, got)
		}
	}
}

func TestRequestValidate(t *testing.T) {
	p, fields := timelog.Request{SpentOn: "2026-10-20", Duration: "1:00", Note: "  Hero  "}.Validate()
	if len(fields) != 0 || p.Seconds != 3600 || p.Note != "Hero" || p.SpentOn.Format("2006-01-02") != "2026-10-20" {
		t.Fatalf("valid = %+v, %v", p, fields)
	}
	_, fields = timelog.Request{SpentOn: "yesterday", Duration: "0:00"}.Validate()
	if fields["spent_on"] == "" || fields["duration"] == "" {
		t.Fatalf("invalid = %v", fields)
	}
	if _, fields = (timelog.Request{SpentOn: "2026-10-20", Duration: "25h"}).Validate(); fields["duration"] == "" {
		t.Fatalf("over a day = %v", fields)
	}
}
