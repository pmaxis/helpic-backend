package money_test

import (
	"testing"

	"github.com/pmaxis/helpic/internal/money"
)

func TestParsePrice(t *testing.T) {
	ok := map[string]int64{
		"0": 0, "1200": 120000, "1 200.5": 120050, "99,90": 9990, " 12.05 ": 1205, "1000000000": money.MaxCents,
	}
	for in, want := range ok {
		if got, err := money.ParsePrice(in); err != nil || got != want {
			t.Errorf("ParsePrice(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "-5", "1.234", "1.", ".5", "1,2.3", "1000000000.01", "99999999999999999999"} {
		if _, err := money.ParsePrice(in); err == nil {
			t.Errorf("ParsePrice(%q) accepted", in)
		}
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		cents    int64
		currency string
		want     string
	}{
		{0, "USD", "$0.00"},
		{5, "EUR", "€0.05"},
		{123450, "UAH", "₴1,234.50"},
		{100000000, "GBP", "£1,000,000.00"},
		{9990, "XYZ", "99.90 XYZ"},
	}
	for _, c := range cases {
		if got := money.Format(c.cents, c.currency); got != c.want {
			t.Errorf("Format(%d, %s) = %q, want %q", c.cents, c.currency, got, c.want)
		}
	}
}
