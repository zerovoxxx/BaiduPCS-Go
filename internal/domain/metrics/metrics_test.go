package metrics

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBoundedLabelsAndConcurrentCounters(t *testing.T) {
	r := New([]string{"SYS07", "FIL01", "SYS07", "PRIVATE\"label"})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.HTTP("SYS07", 200, time.Millisecond)
				r.HTTP(fmt.Sprintf("PRIVATE_%d_%d", i, j), 599, time.Millisecond)
				r.Started("upload")
				r.Settled("upload", "succeeded", 3, 0)
				var b bytes.Buffer
				if err := r.Write(&b, 16, 2); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	wg.Wait()
	var b bytes.Buffer
	r.Write(&b, 16, 2)
	s := b.String()
	for _, want := range []string{`baidupcs_http_requests_total{operation="SYS07",class="2xx"} 1200`, `baidupcs_http_errors_total{operation="unmatched",class="5xx"} 1200`, `baidupcs_transfer_confirmed_bytes_total{direction="upload"} 3600`} {
		if !strings.Contains(s, want) {
			t.Fatal("counter missing", want)
		}
	}
	if strings.Contains(s, "PRIVATE") || len(r.ops) != 3 || len(r.index) != 3 {
		t.Fatal("unbounded label admitted")
	}
}
