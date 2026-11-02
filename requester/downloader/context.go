package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// ExecuteContext is the isolated single-connection execution path. The caller
// owns the checkpoint and must durably flush its writer before acknowledging it.
// It does not use the legacy monitor, global cache sizing, or asynchronous events.
// Execute and the existing CLI retain their current behavior.
func (der *Downloader) ExecuteContext(ctx context.Context, offset int64, checkpoint func(int64) error) error {
	if der.client == nil || der.writer == nil || der.firstInfo == nil || der.firstInfo.ContentLength < 0 || offset < 0 || offset > der.firstInfo.ContentLength || der.config == nil || der.config.MaxParallel != 1 || der.config.MaxRate != 0 || der.config.IsTest || checkpoint == nil {
		return errors.New("context download requires an explicit client, writer, length, checkpoint and parallel=1")
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
	buffer := make([]byte, 64<<10)
	for offset < total {
		if err = ctx.Err(); err != nil {
			return err
		}
		n, readErr := resp.Body.Read(buffer[:min(int64(len(buffer)), total-offset)])
		if n > 0 {
			written, writeErr := der.writer.WriteAt(buffer[:n], offset)
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
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
