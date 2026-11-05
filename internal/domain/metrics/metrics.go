// Package metrics keeps process-local, bounded counters. No caller-controlled
// value is ever added to the label sets after construction.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"sync/atomic"
	"time"
)

var kinds = [...]string{"upload", "download", "file_scan", "mkdir", "file_delete", "file_copy", "file_move", "file_rename", "share_transfer", "share_cancel", "offline_add", "offline_cancel", "offline_delete", "offline_clear", "recycle_restore", "recycle_delete", "recycle_clear", "checksum_fix", "local_checksum", "file_verification", "export", "local_encryption", "local_decryption", "other"}
var states = [...]string{"succeeded", "partially_succeeded", "failed", "cancelled", "paused", "interrupted", "other"}

type httpCounter struct {
	count [5]atomic.Uint64
	nanos atomic.Uint64
}

type Registry struct {
	ops           []string
	index         map[string]int // immutable after New
	http          []httpCounter
	started       [len(kinds)]atomic.Uint64
	settled       [len(kinds)][len(states)]atomic.Uint64
	bytes         [2]atomic.Uint64
	written       atomic.Uint64
	storageErrors atomic.Uint64
	since         time.Time
}

func validOperation(s string) bool {
	if len(s) != 5 {
		return false
	}
	for i := 0; i < 3; i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return s[3] >= '0' && s[3] <= '9' && s[4] >= '0' && s[4] <= '9'
}

func New(operations []string) *Registry {
	r := &Registry{index: map[string]int{}, since: time.Now()}
	for _, op := range operations {
		if !validOperation(op) || len(r.index) >= 128 {
			continue
		}
		if _, ok := r.index[op]; !ok {
			r.index[op] = 0
			r.ops = append(r.ops, op)
		}
	}
	sort.Strings(r.ops)
	r.ops = append(r.ops, "unmatched")
	for i, op := range r.ops {
		r.index[op] = i
	}
	r.http = make([]httpCounter, len(r.ops))
	return r
}

func (r *Registry) HTTP(op string, status int, elapsed time.Duration) {
	if r == nil {
		return
	}
	i, ok := r.index[op]
	if !ok {
		i = len(r.ops) - 1
	}
	c := status/100 - 1
	if c < 0 || c >= 5 {
		c = 4
	}
	r.http[i].count[c].Add(1)
	if elapsed > 0 {
		r.http[i].nanos.Add(uint64(elapsed))
	}
}
func kindIndex(kind string) int {
	for i, k := range kinds {
		if k == kind {
			return i
		}
	}
	return len(kinds) - 1
}
func (r *Registry) Started(kind string) {
	if r != nil {
		r.started[kindIndex(kind)].Add(1)
	}
}
func (r *Registry) Settled(kind, state string, transferred, written int64) {
	if r == nil {
		return
	}
	i := len(states) - 1
	for j, s := range states {
		if s == state {
			i = j
			break
		}
	}
	r.settled[kindIndex(kind)][i].Add(1)
	// Count checkpoint deltas once per execution; a resumed checkpoint is the
	// next execution's baseline. These are confirmed bytes, not wire traffic.
	if transferred > 0 && (kind == "upload" || kind == "download") {
		d := 0
		if kind == "download" {
			d = 1
		}
		r.bytes[d].Add(uint64(transferred))
	}
	if written > 0 {
		r.written.Add(uint64(written))
	}
}
func (r *Registry) StorageError() {
	if r != nil {
		r.storageErrors.Add(1)
	}
}

// Write reads atomics only. It never queries storage or takes executor locks.
// Queue values are supplied from the dispatcher's most recent bounded scan.
func (r *Registry) Write(w io.Writer, queued, executing int64) error {
	if r == nil {
		return fmt.Errorf("metrics registry unavailable")
	}
	var err error
	put := func(format string, a ...any) {
		if err == nil {
			_, err = fmt.Fprintf(w, format, a...)
		}
	}
	put("# HELP baidupcs_process_uptime_seconds Process lifetime; counters reset on restart.\n# TYPE baidupcs_process_uptime_seconds gauge\nbaidupcs_process_uptime_seconds %.3f\n", time.Since(r.since).Seconds())
	put("# HELP baidupcs_jobs_queued Durable queued rows at the last dispatch scan (including admission before worker claim).\n# TYPE baidupcs_jobs_queued gauge\nbaidupcs_jobs_queued %d\n", max(0, min(18, queued)))
	put("# TYPE baidupcs_jobs_executing gauge\nbaidupcs_jobs_executing %d\n# TYPE baidupcs_queue_capacity gauge\nbaidupcs_queue_capacity 16\n# TYPE baidupcs_executor_capacity gauge\nbaidupcs_executor_capacity 2\n", max(0, min(2, executing)))
	put("# TYPE baidupcs_http_requests_total counter\n# TYPE baidupcs_http_errors_total counter\n# TYPE baidupcs_http_request_duration_seconds_sum counter\n")
	for i, op := range r.ops {
		for c := 0; c < 5; c++ {
			n := r.http[i].count[c].Load()
			put("baidupcs_http_requests_total{operation=\"%s\",class=\"%dxx\"} %d\n", op, c+1, n)
			if c >= 3 {
				put("baidupcs_http_errors_total{operation=\"%s\",class=\"%dxx\"} %d\n", op, c+1, n)
			}
		}
		put("baidupcs_http_request_duration_seconds_sum{operation=\"%s\"} %.9f\n", op, float64(r.http[i].nanos.Load())/1e9)
	}
	put("# TYPE baidupcs_job_executions_started_total counter\n# TYPE baidupcs_job_executions_settled_total counter\n")
	for i, kind := range kinds {
		put("baidupcs_job_executions_started_total{kind=\"%s\"} %d\n", kind, r.started[i].Load())
		for j, state := range states {
			put("baidupcs_job_executions_settled_total{kind=\"%s\",state=\"%s\"} %d\n", kind, state, r.settled[i][j].Load())
		}
	}
	put("# HELP baidupcs_transfer_confirmed_bytes_total Confirmed checkpoint deltas settled during this process; excludes historical data.\n# TYPE baidupcs_transfer_confirmed_bytes_total counter\nbaidupcs_transfer_confirmed_bytes_total{direction=\"upload\"} %d\nbaidupcs_transfer_confirmed_bytes_total{direction=\"download\"} %d\n", r.bytes[0].Load(), r.bytes[1].Load())
	put("# TYPE baidupcs_local_written_bytes_total counter\nbaidupcs_local_written_bytes_total %d\n# TYPE baidupcs_storage_errors_total counter\nbaidupcs_storage_errors_total %d\n", r.written.Load(), r.storageErrors.Load())
	return err
}
