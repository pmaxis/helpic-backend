// Package i18n speaks the user's language: English or Ukrainian. Texts are
// written in English in the code, and that English is the key to their
// Ukrainian translation (uk.go), so a missing translation falls back to
// readable English. Formatted texts keep their verbs in both languages:
//
//	i18n.T(locale, "Too many attempts. Try again in %s.", i18n.Duration(locale, d))
//
// A request's language comes from Accept-Language (the frontend sends the
// language its interface is in); a user's is stored on their account and
// used for what reaches them outside a request: email, Telegram.
package i18n

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// The languages Helpic speaks.
const (
	EN = "en"
	UK = "uk"
)

// Default is the language when nothing says otherwise.
const Default = EN

// Locales are all the languages Helpic speaks.
var Locales = []string{EN, UK}

// Normalize maps a language tag ("uk", "uk-UA", "en-GB") to a supported
// locale; anything else is the default.
func Normalize(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if base, _, _ := strings.Cut(tag, "-"); base == UK {
		return UK
	}
	return Default
}

// Valid reports whether locale is one Helpic speaks.
func Valid(locale string) bool {
	return locale == EN || locale == UK
}

// FromHeader picks the first supported language in an Accept-Language
// header, ignoring weights' order beyond the header's own.
func FromHeader(header string) string {
	for _, part := range strings.Split(header, ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		base, _, _ := strings.Cut(strings.ToLower(tag), "-")
		if base == UK || base == EN {
			return base
		}
	}
	return Default
}

type contextKey struct{}

// WithLocale returns ctx carrying the locale.
func WithLocale(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, contextKey{}, locale)
}

// FromContext returns the locale ctx carries, or the default.
func FromContext(ctx context.Context) string {
	if l, ok := ctx.Value(contextKey{}).(string); ok && Valid(l) {
		return l
	}
	return Default
}

// T translates English text into the locale, then fills in args as
// fmt.Sprintf does.
func T(locale, text string, args ...any) string {
	if locale == UK {
		if tr, ok := uk[text]; ok {
			text = tr
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(text, args...)
	}
	return text
}

// Has reports whether text has a Ukrainian translation; for tests.
func Has(text string) bool {
	_, ok := uk[text]
	return ok
}

// units are counted nouns: English singular and plural, Ukrainian one, few
// and many ("1 хвилина", "3 хвилини", "5 хвилин").
var units = map[string][5]string{
	"second": {"second", "seconds", "секунда", "секунди", "секунд"},
	"minute": {"minute", "minutes", "хвилина", "хвилини", "хвилин"},
	"hour":   {"hour", "hours", "година", "години", "годин"},
	"day":    {"day", "days", "день", "дні", "днів"},
	"update": {"update", "updates", "оновлення", "оновлення", "оновлень"},
	"task":   {"task", "tasks", "задача", "задачі", "задач"},
}

// Count says "3 minutes" or "3 хвилини": n of a unit (see units).
func Count(locale string, n int, unit string) string {
	forms, ok := units[unit]
	if !ok {
		return strconv.Itoa(n) + " " + unit
	}
	if locale != UK {
		if n == 1 {
			return "1 " + forms[0]
		}
		return strconv.Itoa(n) + " " + forms[1]
	}
	return strconv.Itoa(n) + " " + forms[2+ukForm(n)]
}

// ukForm is the Ukrainian plural form of n: 0 one, 1 few, 2 many.
func ukForm(n int) int {
	n %= 100
	switch {
	case n >= 11 && n <= 14:
		return 2
	case n%10 == 1:
		return 0
	case n%10 >= 2 && n%10 <= 4:
		return 1
	default:
		return 2
	}
}

// Duration says how long to wait, roughly: "40 seconds", "3 хвилини".
func Duration(locale string, d time.Duration) string {
	seconds := int(math.Ceil(d.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	if seconds < 60 {
		return Count(locale, seconds, "second")
	}
	return Count(locale, int(math.Ceil(float64(seconds)/60)), "minute")
}

var (
	// ukMonthsOf are month names in a date ("17 жовтня 2026").
	ukMonthsOf = [12]string{"січня", "лютого", "березня", "квітня", "травня", "червня", "липня", "серпня", "вересня", "жовтня", "листопада", "грудня"}
	// ukMonths are month names on their own ("Жовтень 2026").
	ukMonths = [12]string{"Січень", "Лютий", "Березень", "Квітень", "Травень", "Червень", "Липень", "Серпень", "Вересень", "Жовтень", "Листопад", "Грудень"}
)

// Date shows a day: "17 Oct 2026", "17 жовтня 2026".
func Date(locale string, t time.Time) string {
	if locale == UK {
		return fmt.Sprintf("%d %s %d", t.Day(), ukMonthsOf[t.Month()-1], t.Year())
	}
	return t.Format("2 Jan 2006")
}

// DayMonth shows a day without the year: "17 Oct", "17 жовтня".
func DayMonth(locale string, t time.Time) string {
	if locale == UK {
		return fmt.Sprintf("%d %s", t.Day(), ukMonthsOf[t.Month()-1])
	}
	return t.Format("2 Jan")
}

// Month shows a month: "October 2026", "Жовтень 2026".
func Month(locale string, t time.Time) string {
	if locale == UK {
		return fmt.Sprintf("%s %d", ukMonths[t.Month()-1], t.Year())
	}
	return t.Format("January 2006")
}
