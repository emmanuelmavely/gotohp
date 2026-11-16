package core

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

// fakeClock drives a RateLimiter without real sleeping.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }
func (c *fakeClock) sleep(_ context.Context, d time.Duration) error {
	c.t = c.t.Add(d)
	return nil
}

func TestRateLimiterKeepsTotalUnderLimit(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	limiter := &RateLimiter{now: clock.now, sleep: clock.sleep}
	limiter.SetRate(100 * 1024) // 100 KB/s

	start := clock.t
	reader := NewLimitedReader(context.Background(), bytes.NewReader(make([]byte, 1024*1024)), limiter)
	n, err := io.Copy(io.Discard, reader)
	if err != nil || n != 1024*1024 {
		t.Fatalf("copied %d, err %v", n, err)
	}
	elapsed := clock.t.Sub(start)
	// 1 MB at 100 KB/s takes about 10 s; the burst allows half a second early.
	if elapsed < 9*time.Second || elapsed > 11*time.Second {
		t.Fatalf("1 MB at 100 KB/s took %s", elapsed)
	}
}

func TestRateLimiterUnlimitedAndChanges(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	limiter := &RateLimiter{now: clock.now, sleep: clock.sleep}

	start := clock.t
	if _, err := io.Copy(io.Discard, NewLimitedReader(context.Background(), bytes.NewReader(make([]byte, 4<<20)), limiter)); err != nil {
		t.Fatal(err)
	}
	if clock.t != start {
		t.Fatal("unlimited reader waited")
	}

	limiter.SetRate(1024 * 1024)
	if limiter.Rate() != 1024*1024 {
		t.Fatal("rate not set")
	}
	limiter.SetRate(-5)
	if limiter.Rate() != 0 {
		t.Fatal("negative rate should mean unlimited")
	}
}

func TestRateLimiterStopsOnCancel(t *testing.T) {
	limiter := NewRateLimiter()
	limiter.SetRate(1) // 1 byte per second
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := NewLimitedReader(ctx, bytes.NewReader(make([]byte, 64)), limiter)
	if _, err := io.Copy(io.Discard, reader); err == nil {
		t.Fatal("want an error after cancel")
	}
}

func TestParseRate(t *testing.T) {
	cases := map[string]int64{
		"":        0,
		"0":       0,
		"500K":    500 * 1024,
		"500kb":   500 * 1024,
		"2M":      2 * 1024 * 1024,
		"1.5MB/s": 1536 * 1024,
		"800":     800 * 1024,
	}
	for text, want := range cases {
		got, err := ParseRate(text)
		if err != nil || got != want {
			t.Errorf("ParseRate(%q) = %d, %v; want %d", text, got, err, want)
		}
	}
	for _, bad := range []string{"fast", "-1M", "2X"} {
		if _, err := ParseRate(bad); err == nil {
			t.Errorf("ParseRate(%q) should fail", bad)
		}
	}
}
