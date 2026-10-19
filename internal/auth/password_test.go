package auth

import "testing"

func TestPasswordIssue(t *testing.T) {
	cases := []struct{ password, want string }{
		{"", "Password is required"},
		{"short", "Password must be at least 8 characters"},
		{"longenough", "Password must include a letter and a number"},
		{"12345678", "Password must include a letter and a number"},
		{"supersecret1", ""},
		{"Пароль123", ""},
	}
	for _, tc := range cases {
		if got := passwordIssue(tc.password); got != tc.want {
			t.Errorf("%q = %q, want %q", tc.password, got, tc.want)
		}
	}
}
