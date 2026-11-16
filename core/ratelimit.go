package core

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimiter caps throughput in bytes per second across everything that
// shares it, so the limit holds for the total of all upload threads rather
// than for each one. A rate of 0 means unlimited. The rate can change while
// transfers run; it applies from the next read.
type RateLimiter struct {
	mu    sync.Mutex
	rate  int64     // bytes per second, 0 = unlimited
	next  time.Time // when the next byte may go out
	sleep func(context.Context, time.Duration) error
	now   func() time.Time
}

// UploadLimiter limits upload traffic; DownloadLimiter limits downloads (the
// Live Photo repair). Both start unlimited.
var (
	UploadLimiter   = NewRateLimiter()
	DownloadLimiter = NewRateLimiter()
)

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{sleep: sleepContext, now: time.Now}
}

// SetRate sets the limit in bytes per second; 0 or less removes it.
func (l *RateLimiter) SetRate(bytesPerSecond int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if bytesPerSecond < 0 {
		bytesPerSecond = 0
	}
	l.rate = bytesPerSecond
	l.next = time.Time{}
}

// Rate returns the current limit in bytes per second (0 = unlimited).
func (l *RateLimiter) Rate() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rate
}

// rateBurst is how far ahead of schedule a transfer may run, so short pauses
// between reads do not waste the allowance.
const rateBurst = 500 * time.Millisecond

// Wait blocks until n more bytes fit under the limit, or ctx ends.
func (l *RateLimiter) Wait(ctx context.Context, n int) error {
	if n <= 0 {
		return nil
	}
	l.mu.Lock()
	if l.rate <= 0 {
		l.mu.Unlock()
		return nil
	}
	now := l.now()
	start := l.next
	if earliest := now.Add(-rateBurst); start.Before(earliest) {
		start = earliest
	}
	cost := time.Duration(float64(n) / float64(l.rate) * float64(time.Second))
	l.next = start.Add(cost)
	delay := l.next.Sub(now)
	l.mu.Unlock()

	if delay <= 0 {
		return nil
	}
	return l.sleep(ctx, delay)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// limitedChunk keeps single reads small so a limit takes effect smoothly.
const limitedChunk = 32 << 10

// limitedReader paces reads through a RateLimiter. With no limit set it
// passes reads straight through.
type limitedReader struct {
	ctx     context.Context
	reader  io.Reader
	limiter *RateLimiter
}

// NewLimitedReader wraps r so reading from it respects limiter.
func NewLimitedReader(ctx context.Context, r io.Reader, limiter *RateLimiter) io.Reader {
	if limiter == nil {
		return r
	}
	return &limitedReader{ctx: ctx, reader: r, limiter: limiter}
}

func (r *limitedReader) Read(p []byte) (int, error) {
	if r.limiter.Rate() > 0 && len(p) > limitedChunk {
		p = p[:limitedChunk]
	}
	n, err := r.reader.Read(p)
	if n > 0 {
		if waitErr := r.limiter.Wait(r.ctx, n); waitErr != nil {
			return n, waitErr
		}
	}
	return n, err
}

// ApplySpeedLimits applies the saved limits (KB/s, 0 = unlimited).
func ApplySpeedLimits(uploadKBps, downloadKBps int) {
	UploadLimiter.SetRate(int64(uploadKBps) * 1024)
	DownloadLimiter.SetRate(int64(downloadKBps) * 1024)
}

// ParseRate reads a speed such as "500K", "2M", "1.5MB" or "800" (KB/s when
// no unit is given) into bytes per second. "0" or "" means unlimited.
func ParseRate(text string) (int64, error) {
	value := strings.ToUpper(strings.TrimSpace(text))
	value = strings.TrimSuffix(strings.TrimSuffix(value, "/S"), "B")
	if value == "" || value == "0" {
		return 0, nil
	}
	multiplier := 1024.0 // a plain number is KB/s
	switch {
	case strings.HasSuffix(value, "K"):
		value = strings.TrimSuffix(value, "K")
	case strings.HasSuffix(value, "M"):
		multiplier = 1024 * 1024
		value = strings.TrimSuffix(value, "M")
	case strings.HasSuffix(value, "G"):
		multiplier = 1024 * 1024 * 1024
		value = strings.TrimSuffix(value, "G")
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number < 0 {
		return 0, fmt.Errorf("invalid speed %q: use a number with K or M, e.g. 500K or 2M", text)
	}
	return int64(number * multiplier), nil
}
