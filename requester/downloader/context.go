package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
)

// ExecuteContext is the isolated single-connection execution path. The caller
// owns the checkpoint and must durably flush its writer before acknowledging it.
// It does not use the legacy monitor, global cache sizing, or asynchronous events.
// Execute and the existing CLI retain their current behavior.
func (der *Downloader) ExecuteContext(ctx context.Context, offset int64, checkpoint func(int64) error, observers ...func(int, int64, int64, string)) error {
	if der.client == nil || der.writer == nil || der.firstInfo == nil || der.firstInfo.ContentLength < 0 || offset < 0 || offset > der.firstInfo.ContentLength || der.config == nil || (der.config.MaxParallel < 1 || der.config.MaxParallel > 32) || der.config.MaxRate != 0 || der.config.IsTest || checkpoint == nil {
		return errors.New("context download requires an explicit client, writer, length, checkpoint and parallel=1")
	}
	if der.config.MaxParallel > 1 {
		return der.executeParallelContext(ctx, offset, checkpoint, observers...)
	}
	total := der.firstInfo.ContentLength
	if err := ctx.Err(); err != nil {
		return err
	}
	if offset == total {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, der.durl, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", der.client.UserAgent)
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, total-1))
	}
	resp, err := der.client.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if offset > 0 {
		expected := fmt.Sprintf("bytes %d-%d/%d", offset, total-1, total)
		if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Range") != expected {
			return errors.New("upstream did not honor the confirmed download range")
		}
	} else if resp.StatusCode != http.StatusOK {
		return errors.New("unexpected upstream download status " + strconv.Itoa(resp.StatusCode))
	}
	if resp.ContentLength != total-offset || resp.Header.Get("Content-Encoding") != "" {
		return errors.New("upstream download size or encoding changed")
	}
	// TLS reads can be a few KiB. Accumulate a bounded durable unit rather than
	// forcing a file and SQLite fsync for every network packet. A partial unit
	// is still flushed on failure; recovery only uses the persisted offset.
	buffer := make([]byte, 256<<10)
	for offset < total {
		if err = ctx.Err(); err != nil {
			return err
		}
		n, readErr := io.ReadFull(resp.Body, buffer[:min(int64(len(buffer)), total-offset)])
		if n > 0 {
			written, writeErr := der.writer.WriteAt(buffer[:n], offset)
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
			if len(observers) > 0 {
				observers[0](0, offset, int64(n), "running")
			}
			offset += int64(n)
			if err = checkpoint(offset); err != nil {
				return err
			}
		}
		if readErr != nil {
			if readErr == io.EOF && offset == total {
				return nil
			}
			return readErr
		}
	}
	return nil
}

// Parallel workers acknowledge only a contiguous, durable prefix. Holes after a
// crash are deliberately fetched again; an unconfirmed tail is never trusted.
func (der *Downloader) executeParallelContext(parent context.Context, offset int64, checkpoint func(int64) error, observers ...func(int, int64, int64, string)) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	total := der.firstInfo.ContentLength
	const chunk int64 = 4 << 20
	type piece struct{ begin, end int64 }
	queue := make(chan piece)
	var wg sync.WaitGroup
	var mu sync.Mutex
	completed := map[int64]int64{}
	confirmed := offset
	var first error
	fail := func(err error) {
		mu.Lock()
		if first == nil {
			first = err
			cancel()
		}
		mu.Unlock()
	}
	for i := 0; i < der.config.MaxParallel; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			buffer := make([]byte, 256<<10)
			for p := range queue {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, der.durl, nil)
				if err != nil {
					fail(err)
					return
				}
				req.Header.Set("User-Agent", der.client.UserAgent)
				req.Header.Set("Accept-Encoding", "identity")
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", p.begin, p.end-1))
				resp, err := der.client.Client.Do(req)
				if err != nil {
					fail(err)
					return
				}
				expected := fmt.Sprintf("bytes %d-%d/%d", p.begin, p.end-1, total)
				if resp.StatusCode != 206 || resp.Header.Get("Content-Range") != expected || resp.ContentLength != p.end-p.begin || resp.Header.Get("Content-Encoding") != "" {
					resp.Body.Close()
					fail(errors.New("upstream did not honor parallel range"))
					return
				}
				for at := p.begin; at < p.end; {
					n, e := io.ReadFull(resp.Body, buffer[:min(int64(len(buffer)), p.end-at)])
					if n > 0 {
						written, we := der.writer.WriteAt(buffer[:n], at)
						if we != nil {
							e = we
						} else if written != n {
							e = io.ErrShortWrite
						}
						if len(observers) > 0 {
							observers[0](workerID, at, int64(n), "running")
						}
						at += int64(n)
					}
					if e != nil {
						resp.Body.Close()
						fail(e)
						return
					}
				}
				resp.Body.Close()
				mu.Lock()
				completed[p.begin] = p.end
				next := confirmed
				for {
					end, ok := completed[next]
					if !ok {
						break
					}
					delete(completed, next)
					next = end
				}
				if next > confirmed {
					if err = checkpoint(next); err != nil {
						if first == nil {
							first = err
						}
						cancel()
					} else {
						confirmed = next
					}
				}
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}(i)
	}
dispatch:
	for begin := offset; begin < total; begin += chunk {
		select {
		case <-ctx.Done():
			break dispatch
		case queue <- piece{begin, min(begin+chunk, total)}:
		}
	}
	close(queue)
	wg.Wait()
	if first != nil {
		return first
	}
	return parent.Err()
}
