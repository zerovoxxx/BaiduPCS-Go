package baidupcs

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestP5SharedTextPasswordPrecedenceAndLegacyRejection(t *testing.T) {
	cases := []struct{ input, password, feature, want string }{{"通过网盘分享 链接: https://pan.baidu.com/s/1example 提取码：a1B2", "", "1example", "a1B2"}, {"https://pan.baidu.com/s/1example?pwd=url1 密码txt2", "", "1example", "url1"}, {"https://pan.baidu.com/s/1example?pwd=url1 密码txt2", "exp3", "1example", "exp3"}, {"https://pan.baidu.com/share/init?surl=example 提取码 a1B2", "", "1example", "a1B2"}}
	for _, c := range cases {
		feature, password, err := ParseShareInput(c.input, c.password)
		if err != nil || feature != c.feature || password != c.want {
			t.Fatal("share parser lost precedence", err)
		}
	}
	for _, input := range []string{"bdpan://legacy", "https://example.com/s/1bad", ""} {
		if _, _, err := ParseShareInput(input, ""); err == nil {
			t.Fatal("invalid/legacy input accepted")
		}
	}
}

type sharePagesTransport struct {
	calls  int
	repeat bool
}

func (s *sharePagesTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.calls++
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if s.repeat {
		page = 1
	}
	list := []any{}
	for i := (page - 1) * 100; i < min(page*100, 205); i++ {
		list = append(list, map[string]any{"fs_id": 9007199254740993 + int64(i), "server_filename": fmt.Sprint(i)})
	}
	raw, _ := json.Marshal(map[string]any{"errno": 0, "list": list})
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw))), Request: r}, nil
}
func TestP5CLIShareExtractionReadsEveryPageAndRejectsRepeatedPages(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		transport := &sharePagesTransport{repeat: repeat}
		client := requester.NewHTTPClientWithTransport(&http.Transport{}, nil, "share-page-test")
		client.Transport = transport
		pcs := NewPCSWithClient(266719, client)
		got := pcs.ExtractShareInfo("https://pan.baidu.com/share/list?shorturl=fixture", "123", "456", "PRIVATE_TEST_TOKEN")
		if repeat {
			if got["ErrMsg"] == "success" || transport.calls != 2 {
				t.Fatal("repeated pages accepted")
			}
			continue
		}
		if got["ErrMsg"] != "success" || got["item_num"] != "205" || transport.calls != 3 || !strings.Contains(got["fs_id"], "9007199254741197") {
			t.Fatal("CLI silently truncated share list")
		}
	}
}
func TestP5CoreOfflineQueryRejectsMoreThan100IDsBeforeTransport(t *testing.T) {
	transport := &failingWriteTransport{}
	client := requester.NewHTTPClientWithTransport(&http.Transport{}, nil, "offline-bound-test")
	client.Transport = transport
	pcs := NewPCSWithClient(266719, client)
	ids := make([]int64, 101)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	if _, err := pcs.CloudDlQueryTask(ids); err == nil || transport.calls != 0 {
		t.Fatal("oversized core query was truncated or sent")
	}
}
