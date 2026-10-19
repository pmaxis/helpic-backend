package ratelimit_test

import (
	"context"
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

func allow(t *testing.T, l *ratelimit.Limiter, key string) (bool, time.Duration) {
	t.Helper()
	ok, retry, err := l.Allow(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return ok, retry
}

func TestAllow_BurstThenRefill(t *testing.T) {
	c := newClock()
	l := limiter(c)
	for i := range 3 {
		if ok, _ := allow(t, l, "a"); !ok {
			t.Fatalf("request %d refused within the burst", i+1)
		}
	}
	ok, retry := allow(t, l, "a")
	if ok || retry != time.Minute {
		t.Fatalf("4th = %v, retry %v; want refused, retry 1m", ok, retry)
	}

	// Another key has its own bucket.
	if ok, _ := allow(t, l, "b"); !ok {
		t.Fatal("another key was refused")
	}

	c.advance(30 * time.Second)
	if ok, retry := allow(t, l, "a"); ok || retry != 30*time.Second {
		t.Fatalf("after 30s = %v, retry %v; want refused, retry 30s", ok, retry)
	}
	c.advance(30 * time.Second)
	if ok, _ := allow(t, l, "a"); !ok {
		t.Fatal("refused after a token came back")
	}

	// A long rest fills the bucket to the burst, not past it.
	c.advance(time.Hour)
	for range 3 {
		allow(t, l, "a")
	}
	if ok, _ := allow(t, l, "a"); ok {
		t.Fatal("the bucket held more than the burst")
	}
}

func TestCheckAndTake(t *testing.T) {
	c := newClock()
	l := limiter(c)
	for range 5 {
		if ok, _, err := l.Check(context.Background(), "a"); err != nil || !ok {
			t.Fatal("Check took a token")
		}
	}
	for range 4 { // one more than there is: never below zero
		if err := l.Take(context.Background(), "a"); err != nil {
			t.Fatal(err)
		}
	}
	if ok, retry, err := l.Check(context.Background(), "a"); err != nil || ok || retry != time.Minute {
		t.Fatalf("Check after Take = %v, retry %v", ok, retry)
	}
	c.advance(time.Minute)
	if ok, _, err := l.Check(context.Background(), "a"); err != nil || !ok {
		t.Fatal("no token after a minute")
	}
}

func TestSweep_KeepsBucketsInUse(t *testing.T) {
	c := newClock()
	l := limiter(c)
	allow(t, l, "used")
	allow(t, l, "used")
	allow(t, l, "used")
	if err := l.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ok, _ := allow(t, l, "used"); ok {
		t.Fatal("Sweep forgot a bucket that was empty")
	}
	c.advance(time.Hour)
	if err := l.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ok, _ := allow(t, l, "used"); !ok {
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
