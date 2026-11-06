package rio

import (
	"context"
	"io"
	"sync"
	"time"
)

// Limiter budgets bytes across all workers in one transfer. Waiting is always
// cancellable; no process-wide timer, token pool or stopped flag is shared.
type Limiter struct {
	mu   sync.Mutex
	rate int64
	next time.Time
}

func NewLimiter(rate int64) *Limiter { return &Limiter{rate: rate} }
func (l *Limiter) Wait(ctx context.Context, n int) error {
	if l == nil || l.rate <= 0 {
		return ctx.Err()
	}
	l.mu.Lock()
	now := time.Now()
	if l.next.Before(now) {
		l.next = now
	}
	l.next = l.next.Add(time.Duration(float64(n) / float64(l.rate) * float64(time.Second)))
	until := l.next
	l.mu.Unlock()
	timer := time.NewTimer(time.Until(until))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type LimitedReader struct {
	Reader  io.Reader
	Limiter *Limiter
	Context context.Context
}

func (r LimitedReader) Read(b []byte) (int, error) {
	if len(b) > 32<<10 {
		b = b[:32<<10]
	}
	n, err := r.Reader.Read(b)
	if n > 0 {
		if e := r.Limiter.Wait(r.Context, n); e != nil {
			return 0, e
		}
	}
	return n, err
}
