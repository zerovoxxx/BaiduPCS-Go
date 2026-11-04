package baidu

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/downloader"
)

func TestP2DownloadRejectsInvalidRangesAndTruncatedStreams(t *testing.T) {
	for _, kind := range []string{"ignored", "wrong-range", "wrong-length", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Range") != "bytes=2-5" {
					t.Error("missing confirmed range")
				}
				w.Header().Set("Content-Length", "4")
				w.Header().Set("Content-Range", "bytes 2-5/6")
				switch kind {
				case "ignored":
					w.WriteHeader(200)
				case "wrong-range":
					w.Header().Set("Content-Range", "bytes 1-4/6")
					w.WriteHeader(206)
				case "wrong-length":
					w.Header().Set("Content-Length", "3")
					w.WriteHeader(206)
				case "truncated":
					w.WriteHeader(206)
					io.WriteString(w, "c")
					return
				}
				io.WriteString(w, "cdef")
			}))
			defer server.Close()
			file, err := os.CreateTemp(t.TempDir(), "partial")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			file.Write([]byte("ab"))
			client := requester.NewHTTPClientWithTransport(server.Client().Transport.(*http.Transport), nil, "test")
			engine := downloader.NewDownloader(server.URL, file, &downloader.Config{MaxParallel: 1})
			engine.SetClient(client)
			engine.SetFileContentLength(6)
			var offset int64
			if err = engine.ExecuteContext(context.Background(), 2, func(n int64) error { offset = n; return nil }); err == nil {
				t.Fatal("invalid response accepted")
			}
			if kind != "truncated" {
				got, _ := os.ReadFile(file.Name())
				if !bytes.Equal(got, []byte("ab")) || offset != 0 {
					t.Fatal("invalid range appended bytes")
				}
			}
		})
	}
}
func TestP2SlowDownloadCancellationIsBounded(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(exited)
		w.Header().Set("Content-Length", "6")
		io.WriteString(w, "a")
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	file, err := os.CreateTemp(t.TempDir(), "partial")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	client := requester.NewHTTPClientWithTransport(server.Client().Transport.(*http.Transport), nil, "test")
	engine := downloader.NewDownloader(server.URL, file, &downloader.Config{MaxParallel: 1})
	engine.SetClient(client)
	engine.SetFileContentLength(6)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- engine.ExecuteContext(ctx, 0, func(int64) error { return nil }) }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancel did not reach reader", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow body ignored cancel")
	}
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream connection not released")
	}
}
