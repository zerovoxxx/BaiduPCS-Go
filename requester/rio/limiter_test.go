package rio

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestP4LimiterSharesBudgetAndCancellation(t *testing.T) {
	limiter := NewLimiter(1 << 20)
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := limiter.Wait(context.Background(), 256<<10); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if time.Since(start) < 450*time.Millisecond {
		t.Fatal("workers each received the full budget")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start = time.Now()
	if err := NewLimiter(1).Wait(ctx, 32<<10); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored")
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("rate wait did not drain")
	}
}
