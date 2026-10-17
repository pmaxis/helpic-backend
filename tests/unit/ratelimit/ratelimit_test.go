package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pmaxis/helpic/internal/ratelimit"
)

// clock is a time that moves only when told to.
type clock struct{ t time.Time }

func (c *clock) now() time.Time           { return c.t }
func (c *clock) advance(d time.Duration)  { c.t = c.t.Add(d) }
func newClock() *clock                    { return &clock{t: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)} }
func limiter(c *clock) *ratelimit.Limiter { return ratelimit.New(3, time.Minute).WithClock(c.now) }

func TestAllow_BurstThenRefill(t *testing.T) {
	c := newClock()
	l := limiter(c)
	for i := range 3 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d refused within the burst", i+1)
		}
	}
	ok, retry := l.Allow("a")
	if ok || retry != time.Minute {
		t.Fatalf("4th = %v, retry %v; want refused, retry 1m", ok, retry)
	}

	// Another key has its own bucket.
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("another key was refused")
	}

	c.advance(30 * time.Second)
	if ok, retry := l.Allow("a"); ok || retry != 30*time.Second {
		t.Fatalf("after 30s = %v, retry %v; want refused, retry 30s", ok, retry)
	}
	c.advance(30 * time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("refused after a token came back")
	}

	// A long rest fills the bucket to the burst, not past it.
	c.advance(time.Hour)
	for range 3 {
		l.Allow("a")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("the bucket held more than the burst")
	}
}

func TestCheckAndTake(t *testing.T) {
	c := newClock()
	l := limiter(c)
	for range 5 {
		if ok, _ := l.Check("a"); !ok {
			t.Fatal("Check took a token")
		}
	}
	for range 4 { // one more than there is: never below zero
		l.Take("a")
	}
	if ok, retry := l.Check("a"); ok || retry != time.Minute {
		t.Fatalf("Check after Take = %v, retry %v", ok, retry)
	}
	c.advance(time.Minute)
	if ok, _ := l.Check("a"); !ok {
		t.Fatal("no token after a minute")
	}
}

func TestSweep_KeepsBucketsInUse(t *testing.T) {
	c := newClock()
	l := limiter(c)
	l.Allow("used")
	l.Allow("used")
	l.Allow("used")
	l.Sweep()
	if ok, _ := l.Allow("used"); ok {
		t.Fatal("Sweep forgot a bucket that was empty")
	}
	c.advance(time.Hour)
	l.Sweep()
	if ok, _ := l.Allow("used"); !ok {
		t.Fatal("a full bucket refused")
	}
}

func TestMiddleware(t *testing.T) {
	l := ratelimit.New(1, time.Hour)
	h := ratelimit.Middleware(l, func(r *http.Request) string { return r.Header.Get("X-Key") })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	call := func(key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("X-Key", key)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := call("a"); rec.Code != http.StatusNoContent {
		t.Fatalf("first = %d", rec.Code)
	}
	rec := call("a")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "3600" {
		t.Fatalf("second = %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if body := rec.Body.String(); !strings.Contains(body, "RATE_LIMITED") || !strings.Contains(body, "60 minutes") {
		t.Fatalf("body = %s", body)
	}
	// An empty key is not limited.
	for range 3 {
		if rec := call(""); rec.Code != http.StatusNoContent {
			t.Fatalf("empty key = %d", rec.Code)
		}
	}
}
