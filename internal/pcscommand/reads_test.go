package pcscommand

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

type readTransport func(*http.Request) (*http.Response, error)

func (f readTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func captureReads(t *testing.T, run func()) string {
	t.Helper()
	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = old }()
	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(reader); _ = reader.Close(); done <- string(b) }()
	run()
	_ = writer.Close()
	return <-done
}
func TestP3CLISharedReadsPreserveAggregationOrderingAndOutput(t *testing.T) {
	count, fail, repeat := 2001, false, false
	wantOrder, wantDesc := "size", "1"
	calls := 0
	searchRecursive := false
	client := requester.NewHTTPClientWithTransport(&http.Transport{}, nil, "")
	client.Client.Transport = readTransport(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		list := []any{}
		switch q.Get("method") {
		case "search":
			searchRecursive = q.Get("re") == "1"
			list = append(list, map[string]any{"fs_id": 9007199254740993, "path": "/data/find.txt", "server_filename": "find.txt", "size": 17, "md5": "d41d8cd98f00b204e9800998ecf8427e", "isdir": 0})
		case "meta":
			list = append(list, map[string]any{"fs_id": 9007199254740993, "path": "/data/file.txt", "server_filename": "file.txt", "size": 17, "isdir": 0})
		default:
			calls++
			if q.Get("order") != wantOrder || q.Get("desc") != wantDesc {
				t.Errorf("CLI sort changed: %s", q.Encode())
			}
			var page, size int
			fmt.Sscan(q.Get("page"), &page)
			fmt.Sscan(q.Get("num"), &size)
			if fail && page == 2 {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"errno":-1}`))}, nil
			}
			if repeat && page > 1 {
				page = 1
			}
			for i := (page - 1) * size; i < min(page*size, count); i++ {
				list = append(list, map[string]any{"fs_id": int64(9007199254740993) + int64(i), "path": fmt.Sprintf("/data/file%04d.txt", i), "server_filename": fmt.Sprintf("file%04d.txt", i), "size": 17, "isdir": 0})
			}
		}
		body, _ := json.Marshal(map[string]any{"list": list, "errno": 0})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	pcs := baidupcs.NewPCSWithClient(266719, client)
	pcs.SetHTTPS(true)
	order := &baidupcs.OrderOptions{By: baidupcs.OrderBySize, Order: baidupcs.OrderDesc}
	for _, n := range []int{1001, 2000, 2001} {
		count = n
		calls = 0
		files, err := pcs.FilesDirectoriesList("/data", order)
		if err != nil || len(files) != n || calls != n/1000+1 {
			t.Fatal("CLI aggregate pagination", n, len(files), calls, err)
		}
	}
	count = 3
	listed := captureReads(t, func() { runLs(pcs, "/data", &LsOptions{Total: true}, order) })
	for _, want := range []string{"当前目录: /data", "9007199254740993", "file0002.txt", "文件总数: 3"} {
		if !strings.Contains(listed, want) {
			t.Fatal("CLI listing output lost", want)
		}
	}
	search := captureReads(t, func() { runSearch(pcs, "/data", "find", &SearchOptions{Total: true, Recurse: true}) })
	if !searchRecursive || !strings.Contains(search, "/data/find.txt") {
		t.Fatal("CLI recursive search changed")
	}
	meta := captureReads(t, func() { runGetMeta(pcs, "/data/file.txt") })
	if !strings.Contains(meta, "9007199254740993") || !strings.Contains(meta, "file.txt") {
		t.Fatal("CLI metadata output changed")
	}
	wantOrder, wantDesc = "name", "0"
	tree := captureReads(t, func() { getTreeFrom(pcs, "/data", 0, &TreeOptions{Depth: 0, ShowFsid: true}, false) })
	if !strings.Contains(tree, "file0002.txt: 9007199254740995") {
		t.Fatal("CLI tree fs_id output changed")
	}
	wantOrder, wantDesc = "size", "1"
	count = 2001
	repeat = true
	if _, err := pcs.FilesDirectoriesList("/data", order); err == nil {
		t.Fatal("repeated upstream page reported full success")
	}
	repeat = false
	fail = true
	if data, err := pcs.FilesDirectoriesList("/data", order); err == nil || len(data) != 1000 {
		t.Fatal("mid-page error lost evidence or claimed success")
	}
}
