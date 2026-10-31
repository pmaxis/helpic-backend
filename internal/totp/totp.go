// Package totp makes and checks the six-digit codes of authenticator apps
// (RFC 6238): HMAC-SHA1 over the number of 30-second steps since the Unix
// epoch, shared secret in base32. It is shared infrastructure, not a feature.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// Period is how long one code lasts.
	Period = 30 * time.Second
	// Digits is the length of a code.
	Digits = 6
	// skew is how many steps either side of now still count, for a phone
	// whose clock is a little off.
	skew = 1
	// secretBytes is 160 bits, the size RFC 4226 recommends.
	secretBytes = 20
)

var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a random secret, base32 as authenticator apps take it.
func NewSecret() (string, error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return encoding.EncodeToString(b), nil
}

// URI is the otpauth:// link a QR code carries: the app reads the secret
// from it and shows the account under the issuer's name.
func URI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(Digits))
	q.Set("period", fmt.Sprint(int(Period.Seconds())))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// Step is the step a moment falls in.
func Step(t time.Time) int64 {
	return t.Unix() / int64(Period.Seconds())
}

// Code is the code for a step.
func Code(secret string, step int64) (string, error) {
	key, err := encoding.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", fmt.Errorf("decode secret: %w", err)
	}
	if step < 0 {
		return "", fmt.Errorf("step before the Unix epoch: %d", step)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	// Dynamic truncation: four bytes from where the last nibble points.
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", Digits, n%1_000_000), nil
}

// Verify checks a code against the steps around now and returns the step it
// matched, so the caller can refuse it a second time. ok is false for a
// wrong code.
func Verify(secret, code string, now time.Time) (step int64, ok bool, err error) {
	if len(code) != Digits {
		return 0, false, nil
	}
	current := Step(now)
	for s := current - skew; s <= current+skew; s++ {
		want, err := Code(secret, s)
		if err != nil {
			return 0, false, err
		}
		if hmac.Equal([]byte(want), []byte(code)) {
			return s, true, nil
		}
	}
	return 0, false, nil
}
