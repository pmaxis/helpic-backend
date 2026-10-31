// Package pwned asks Have I Been Pwned's Pwned Passwords whether a password
// is known from a data breach. The password never leaves the server: only the
// first five characters of its SHA-1 go out, and the service answers with
// every breached hash that starts with them (k-anonymity). Padding makes all
// answers about the same size, so their length tells nothing either.
package pwned

import (
	"bufio"
	"context"
	"crypto/sha1" // #nosec G505 -- the service's key, not a password hash
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// timeout bounds a call; it runs while someone waits to sign up.
const timeout = 3 * time.Second

// Client calls the Pwned Passwords range API.
type Client struct {
	base string
	http *http.Client
}

// NewClient creates a Client for the API at base, e.g.
// https://api.pwnedpasswords.com.
func NewClient(base string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: timeout}}
}

// Breached reports whether the password has turned up in a breach.
func (c *Client) Breached(ctx context.Context, password string) (bool, error) {
	sum := sha1.Sum([]byte(password)) // #nosec G401 -- the service's key, not a password hash
	hash := strings.ToUpper(hex.EncodeToString(sum[:]))
	prefix, suffix := hash[:5], hash[5:]

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/range/"+prefix, nil)
	if err != nil {
		return false, fmt.Errorf("pwned request: %w", err)
	}
	req.Header.Set("Add-Padding", "true")
	req.Header.Set("User-Agent", "Helpic")
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("pwned: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("pwned: status %d", resp.StatusCode)
	}

	// Lines are SUFFIX:COUNT; padding lines have a count of 0.
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		s, count, ok := strings.Cut(strings.TrimSpace(sc.Text()), ":")
		if ok && strings.EqualFold(s, suffix) {
			return count != "0", nil
		}
	}
	if err := sc.Err(); err != nil {
		return false, fmt.Errorf("pwned: read: %w", err)
	}
	return false, nil
}
