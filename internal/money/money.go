// Package money handles task prices: amounts are whole cents (int64), in the
// currency the freelancer picked in their profile.
package money

import (
	"errors"
	"slices"
	"strconv"
	"strings"
)

// DefaultCurrency is a new account's currency.
const DefaultCurrency = "USD"

// Currencies lists the currencies a freelancer can bill in.
var Currencies = []string{"USD", "EUR", "GBP", "UAH", "PLN", "CAD"}

// MaxCents caps a price at 1 000 000 000.00.
const MaxCents = 100_000_000_000

var ErrInvalidPrice = errors.New("invalid price")

// ValidCurrency reports whether the code is one of Currencies.
func ValidCurrency(code string) bool {
	return slices.Contains(Currencies, code)
}

// ParsePrice reads an amount like "1200", "1 200.5" or "99,90" into cents.
// Spaces separate thousands; "." or "," is the decimal mark, with at most two
// decimals.
func ParsePrice(s string) (int64, error) {
	s = strings.NewReplacer(" ", "", " ", "").Replace(strings.TrimSpace(s))
	whole, frac, hasFrac := strings.Cut(strings.ReplaceAll(s, ",", "."), ".")
	if whole == "" || !digits(whole) || (hasFrac && (frac == "" || len(frac) > 2 || !digits(frac))) {
		return 0, ErrInvalidPrice
	}
	frac += strings.Repeat("0", 2-len(frac))
	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || units > MaxCents/100 {
		return 0, ErrInvalidPrice
	}
	cents, _ := strconv.ParseInt(frac, 10, 64)
	total := units*100 + cents
	if total > MaxCents {
		return 0, ErrInvalidPrice
	}
	return total, nil
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
