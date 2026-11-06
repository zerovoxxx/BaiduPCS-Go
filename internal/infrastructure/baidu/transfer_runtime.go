package baidu

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
)

type limiterKey struct{}
type pacedTransport struct {
	base    http.RoundTripper
	limiter *rio.Limiter
	ctx     context.Context
}
type pacedBody struct {
	io.Reader
	io.Closer
}

func (t pacedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil && r.URL.Query().Get("method") == "upload" {
		r = r.Clone(r.Context())
		r.Body = pacedBody{Reader: rio.LimitedReader{Reader: r.Body, Limiter: t.limiter, Context: t.ctx}, Closer: r.Body}
	}
	resp, err := t.base.RoundTrip(r)
	if err == nil && r.Method == "GET" && r.URL.Query().Get("method") != "meta" {
		resp.Body = pacedBody{Reader: rio.LimitedReader{Reader: resp.Body, Limiter: t.limiter, Context: t.ctx}, Closer: resp.Body}
	}
	return resp, err
}
func transferContext(ctx context.Context, r transfer.Record) context.Context {
	if ctx.Value(limiterKey{}) != nil {
		return ctx
	}
	rate := r.Config.MaxDownloadRate
	if r.Request.Kind == "upload" {
		rate = r.Config.MaxUploadRate
	}
	return context.WithValue(ctx, limiterKey{}, rio.NewLimiter(rate))
}
func executeAttempts(ctx context.Context, r transfer.Record, cp *fileCheckpoint, persist func() error, run func() error) error {
	var opts struct {
		Retries int `json:"max_retries"`
	}
	_ = json.Unmarshal(r.EffectiveOptions, &opts)
	for attempt := 0; ; attempt++ {
		err := run()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if cp.Phase == "committing" || cp.Phase == "done" {
			return err
		}
		var pcs pcserror.Error
		expired := errors.As(err, &pcs) && pcs.GetErrType() == pcserror.ErrTypeRemoteError && (pcs.GetRemoteErrCode() == 31363 || pcs.GetRemoteErrCode() == 31079)
		f := fault.As(upstreamFault(err))
		if attempt >= opts.Retries || !expired && !f.Retryable {
			return err
		}
		if expired {
			cp.Upload = nil
			cp.Phase = "preparing"
			if err = persist(); err != nil {
				return err
			}
		}
		timer := time.NewTimer(time.Duration(min(attempt+1, 5)) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
