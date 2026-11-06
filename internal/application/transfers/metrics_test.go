package transfers

import (
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"testing"
	"time"
)

func TestMetricsSnapshotDoesNotWaitForExecutorMutex(t *testing.T) {
	s := &Service{workers: make(chan struct{}, 2)}
	s.queued.Store(16)
	s.workers <- struct{}{}
	s.mu.Lock()
	defer s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		q, n := s.MetricsSnapshot()
		if q != 16 || n != 1 {
			t.Error(q, n)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scrape took executor lock")
	}
}

func TestConfirmedMetricDeltasUseResumeCheckpoint(t *testing.T) {
	r := transfer.Record{Request: transfer.Request{Kind: "upload"}, Checkpoint: json.RawMessage(`{"Upload":{"block_list":[{"Range":{"Begin":0,"End":4},"checksum":"ok"},{"Range":{"Begin":4,"End":8},"checksum":""}]}}`)}
	before, _ := confirmedBytes(r)
	r.Checkpoint = json.RawMessage(`{"Phase":"done","Size":8}`)
	after, _ := confirmedBytes(r)
	if before != 4 || after-before != 4 {
		t.Fatal(before, after)
	}
	r.Request.Kind = "download"
	r.Checkpoint = json.RawMessage(`{"Offset":11}`)
	x, w := confirmedBytes(r)
	if x != 11 || w != 11 {
		t.Fatal(x, w)
	}
}
