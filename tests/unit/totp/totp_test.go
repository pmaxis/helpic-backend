package totp_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/pmaxis/helpic/internal/totp"
)

// rfcSecret is the SHA1 key of RFC 6238's test vectors, "12345678901234567890", in base32.
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestCode_RFC6238Vectors(t *testing.T) {
	// The RFC's eight-digit codes, cut to the last six.
	vectors := map[int64]string{
		59:         "287082",
		1111111109: "081804",
		1111111111: "050471",
		1234567890: "005924",
		2000000000: "279037",
	}
	for unix, want := range vectors {
		got, err := totp.Code(rfcSecret, totp.Step(time.Unix(unix, 0)))
		if err != nil || got != want {
			t.Errorf("Code at %d = %q, %v; want %q", unix, got, err, want)
		}
	}
}

func TestVerify_AllowsOneStepEitherSide(t *testing.T) {
	now := time.Unix(1111111111, 0)
	step := totp.Step(now)
	for offset := int64(-1); offset <= 1; offset++ {
		code, _ := totp.Code(rfcSecret, step+offset)
		got, ok, err := totp.Verify(rfcSecret, code, now)
		if err != nil || !ok || got != step+offset {
			t.Errorf("offset %d: step %d, ok %v, err %v", offset, got, ok, err)
		}
	}
	for _, offset := range []int64{-2, 2} {
		code, _ := totp.Code(rfcSecret, step+offset)
		if _, ok, _ := totp.Verify(rfcSecret, code, now); ok {
			t.Errorf("offset %d accepted", offset)
		}
	}
	for _, code := range []string{"", "12345", "1234567", "abcdef"} {
		if _, ok, _ := totp.Verify(rfcSecret, code, now); ok {
			t.Errorf("%q accepted", code)
		}
	}
}

func TestNewSecretAndURI(t *testing.T) {
	a, err := totp.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := totp.NewSecret()
	if len(a) != 32 || a == b {
		t.Fatalf("secrets %q, %q", a, b)
	}
	if _, err := totp.Code(a, 1); err != nil {
		t.Fatalf("a new secret doesn't make codes: %v", err)
	}

	u, err := url.Parse(totp.URI("Helpic", "olena@example.com", a))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme != "otpauth" || u.Host != "totp" || u.Path != "/Helpic:olena@example.com" ||
		q.Get("secret") != a || q.Get("issuer") != "Helpic" || q.Get("digits") != "6" || q.Get("period") != "30" {
		t.Fatalf("URI = %s", u)
	}
}
