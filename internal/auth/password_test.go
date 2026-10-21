package auth

import (
	"strings"
	"testing"
)

func TestPasswordIssue(t *testing.T) {
	cases := []struct{ password, want string }{
		{"", "Password is required"},
		{"short", "Password must be at least 8 characters"},
		{"longenough", "Password must include a letter and a number"},
		{"12345678", "Password must include a letter and a number"},
		{"supersecret1", ""},
		{"Пароль123", ""},
		{strings.Repeat("a1", 36), ""},
		{strings.Repeat("a1", 36) + "b", "Password is too long"},
		// 36 Cyrillic letters are 72 bytes; one more is too many for bcrypt.
		{strings.Repeat("ї", 35) + "1", ""},
		{strings.Repeat("ї", 36) + "1", "Password is too long"},
	}
	for _, tc := range cases {
		if got := passwordIssue(tc.password); got != tc.want {
			t.Errorf("%q = %q, want %q", tc.password, got, tc.want)
		}
	}
}
