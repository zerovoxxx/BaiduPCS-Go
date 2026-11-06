package httpapi

import (
	"encoding/json"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/application/transfers"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const sseQueueLimit = 128
const sseHeartbeat = 15 * time.Second

func eventID(job string, seq uint64) string { return "evt_" + job + "_" + uintString(seq) }
func eventCursor(job, value string) (uint64, error) {
	prefix := "evt_" + job + "_"
	if !strings.HasPrefix(value, prefix) {
		return 0, &httpError{400, "INVALID_EVENT_CURSOR", "事件游标格式无效或属于其他任务"}
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(value, prefix), 10, 64)
	if err != nil || eventID(job, n) != value {
		return 0, &httpError{400, "INVALID_EVENT_CURSOR", "事件游标格式无效"}
	}
	return n, nil
}
func (a *API) serveEvents(w http.ResponseWriter, r *http.Request) error {
	p, ctx, id := principal(r.Context()), r.Context(), r.PathValue("job_id")
	select {
	case a.sseSlots <- struct{}{}:
		defer func() { <-a.sseSlots }()
	default:
		return &httpError{429, "SSE_CONNECTION_LIMIT", "事件连接数已达上限"}
	}
	snapshot, head, err := a.deps.Transfers.EventSnapshot(ctx, p, id)
	if err != nil {
		return err
	}
	after := head
	initialHead := head
	values := r.Header.Values("Last-Event-ID")
	if len(values) > 1 {
		return invalid("Last-Event-ID不能重复")
	}
	replay := len(values) == 1
	if replay {
		if after, err = eventCursor(id, values[0]); err != nil {
			return err
		}
		batch, err := a.deps.Transfers.Events(ctx, p, id, after, sseQueueLimit)
		if err != nil {
			return err
		}
		initialHead = batch.Head
	}
	controller := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	write := func(seq uint64, kind string, payload any, occurred string) error {
		body, err := json.Marshal(map[string]any{"event_id": eventID(id, seq), "job_id": id, "occurred_at": occurred, "type": kind, "payload": payload})
		if err != nil {
			return err
		}
		_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
		defer controller.SetWriteDeadline(time.Time{})
		if _, err = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", eventID(id, seq), kind, body); err != nil {
			return err
		}
		return controller.Flush()
	}
	if !replay {
		if err = write(head, "job.snapshot", publicJob(snapshot, RequestID(ctx)), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return nil
		}
	}
	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	for {
		if a.deps.Draining != nil && a.deps.Draining() {
			return nil
		}
		batch, err := a.deps.Transfers.Events(ctx, p, id, after, sseQueueLimit)
		if err != nil {
			return nil
		}
		// Replay is bounded by the retained log. Once caught up, >128 pending frames
		// disconnects a slow consumer; the durable cursor lets it reconnect safely.
		if after >= initialHead && batch.Head-after > sseQueueLimit {
			return nil
		}
		for _, e := range batch.Events {
			var payload any
			if e.Record == nil {
				return nil
			}
			switch e.Type {
			case "job.created", "job.state_changed":
				payload = publicJob(*e.Record, RequestID(ctx))
			case "job.progress":
				payload = publicJob(*e.Record, RequestID(ctx)).(map[string]any)["progress"]
			case "job.finished":
				payload = publicResult(*e.Record)
			case "item.completed":
				item := e.Item
				if item == nil {
					v := transfers.TransferItem(*e.Record)
					item = &v
				}
				payload = publicItem(*item, RequestID(ctx))
			default:
				return nil
			}
			if err = write(e.Sequence, e.Type, payload, e.OccurredAt); err != nil {
				return nil
			}
			after = e.Sequence
		}
		if after < batch.Head {
			continue
		}
		current, currentHead, err := a.deps.Transfers.EventSnapshot(ctx, p, id)
		if err != nil {
			return nil
		}
		if transfer.Terminal(current.State) && after >= currentHead {
			return nil
		}
		if after < currentHead {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-poll.C:
		case <-heartbeat.C:
			_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
			_, err = w.Write([]byte(": heartbeat\n\n"))
			if err == nil {
				err = controller.Flush()
			}
			_ = controller.SetWriteDeadline(time.Time{})
			if err != nil {
				return nil
			}
		}
	}
}
