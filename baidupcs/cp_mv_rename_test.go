package baidupcs

import (
	"errors"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"io"
	"net/http"
	"strings"
	"testing"
)

type failingWriteTransport struct {
	err   error
	calls int
}

func (t *failingWriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls++
	return nil, t.err
}
func TestCpMvPrepareErrorIsReturnedInsteadOfSuccess(t *testing.T) {
	for _, op := range []string{"copy", "move", "rename"} {
		t.Run(op, func(t *testing.T) {
			sentinel := errors.New("controlled prepare transport failure")
			transport := &failingWriteTransport{err: sentinel}
			client := requester.NewHTTPClientWithTransport(&http.Transport{}, nil, "prepare-test")
			client.Transport = transport
			pcs := NewPCSWithClient(266719, client)
			pcs.SetHTTPS(true)
			var err pcserror.Error
			switch op {
			case "copy":
				err = pcs.Copy(&CpMvJSON{From: "/from", To: "/to"})
			case "move":
				err = pcs.Move(&CpMvJSON{From: "/from", To: "/to"})
			case "rename":
				err = pcs.Rename("/from", "/to")
			}
			if err == nil || err.GetErrType() != pcserror.ErrTypeNetError || !errors.Is(err.GetError(), sentinel) || transport.calls != 1 {
				t.Fatalf("%s lost prepare failure: %v, requests=%d", op, err, transport.calls)
			}
		})
	}
}

type decimalAppTransport struct{ t *testing.T }

func (d decimalAppTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Query().Get("app_id") != "266719" {
		d.t.Error("app_id encoded as a Unicode codepoint")
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"errno":0}`)), Request: r}, nil
}
func TestPrepareBDStokenUsesDecimalAppID(t *testing.T) {
	client := requester.NewHTTPClientWithTransport(&http.Transport{}, nil, "prepare-test")
	client.Transport = decimalAppTransport{t}
	pcs := NewPCSWithClient(266719, client)
	body, err := pcs.PrepareBDStoken()
	if err != nil {
		t.Fatal(err)
	}
	body.Close()
}
