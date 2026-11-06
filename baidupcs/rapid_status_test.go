package baidupcs

import (
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"io"
	"net/http"
	"strings"
	"testing"
)

type rapidStatusTransport struct{ body string }

func (t rapidStatusTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(t.body)), Request: r}, nil
}
func TestP6RapidRepairRequiresExplicitSuccessStatus(t *testing.T) {
	for _, tc := range []struct {
		body string
		kind pcserror.ErrType
		code int
	}{{`{"errno":0}`, pcserror.ErrorTypeNoError, 0}, {`{"error_code":0}`, pcserror.ErrorTypeNoError, 0}, {`{"errno":-6}`, pcserror.ErrTypeRemoteError, -6}, {`{"errno":2,"error_code":0}`, pcserror.ErrTypeRemoteError, 2}, {`{"errno":0,"error_code":31045}`, pcserror.ErrTypeRemoteError, 31045}, {`{}`, pcserror.ErrTypeJSONParseError, 0}, {`not-json`, pcserror.ErrTypeJSONParseError, 0}} {
		client := requester.NewHTTPClientWithTransport(&http.Transport{}, nil, "test")
		client.Transport = rapidStatusTransport{tc.body}
		pcs := NewPCSWithClient(266719, client)
		pcs.SetHTTPS(true)
		err := pcs.RapidUploadNoCheckDir("/fixture", "d41d8cd98f00b204e9800998ecf8427e", "d41d8cd98f00b204e9800998ecf8427e", "0", 0)
		if tc.kind == pcserror.ErrorTypeNoError {
			if err != nil {
				t.Fatal("success rejected", err)
			}
		} else if err == nil || err.GetErrType() != tc.kind || tc.code != 0 && err.GetRemoteErrCode() != tc.code {
			t.Fatal("response misreported", tc.body, err)
		}
	}
}
