package ratelimit

// The API's limits, as their buckets are stored. What a bucket's key is
// depends on the scope: KeyOf tells.
const (
	ScopeSignIn             = "sign-in"
	ScopeSignUp             = "sign-up"
	ScopeRefresh            = "refresh"
	ScopeLoginFailures      = "login-failures"
	ScopeLoginFailuresEmail = "login-failures-email"
	ScopePasswordChecks     = "password-checks"
	ScopeTwoFactorCodes     = "two-factor-codes"
	ScopeReset              = "reset"
	ScopeResetEmail         = "reset-email"
	ScopeInvites            = "invites"
	ScopeShared             = "shared"
)

// What a bucket's key is.
const (
	// KeyIP is the client's address.
	KeyIP = "ip"
	// KeyEmail is an email.
	KeyEmail = "email"
	// KeyEmailIP is an email and the client's address, a space between.
	KeyEmailIP = "email_ip"
	// KeyUser is a user's ID.
	KeyUser = "user"
)

// KeyOf tells what the keys of scope are.
func KeyOf(scope string) string {
	switch scope {
	case ScopeLoginFailures, ScopeReset:
		return KeyEmailIP
	case ScopeLoginFailuresEmail, ScopeResetEmail:
		return KeyEmail
	case ScopePasswordChecks, ScopeTwoFactorCodes, ScopeInvites:
		return KeyUser
	}
	return KeyIP
}

// ScopesKeyedBy lists the scopes whose keys are kind.
func ScopesKeyedBy(kind string) []string {
	var out []string
	for _, s := range []string{ScopeSignIn, ScopeSignUp, ScopeRefresh, ScopeLoginFailures, ScopeLoginFailuresEmail, ScopePasswordChecks, ScopeTwoFactorCodes, ScopeReset, ScopeResetEmail, ScopeInvites, ScopeShared} {
		if KeyOf(s) == kind {
			out = append(out, s)
		}
	}
	return out
}
