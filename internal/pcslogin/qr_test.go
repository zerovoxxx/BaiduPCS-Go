package pcslogin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

type pendingBody struct{ ctx context.Context }

func (b pendingBody) Read([]byte) (int, error) { <-b.ctx.Done(); return 0, b.ctx.Err() }
func (pendingBody) Close() error               { return nil }

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func qrImage(t *testing.T) []byte {
	t.Helper()
	matrix, err := qrcode.NewQRCodeWriter().Encode("https://wappass.baidu.com/wp/?qrlogin&sign=TEST-ONLY", gozxing.BarcodeFormat_QR_CODE, 240, 240, nil)
	if err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if err = png.Encode(&data, matrix); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func response(req *http.Request, code int, body string, cookies ...*http.Cookie) *http.Response {
	header := make(http.Header)
	for _, cookie := range cookies {
		header.Add("Set-Cookie", cookie.String())
	}
	return &http.Response{StatusCode: code, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

type fixture struct {
	t             *testing.T
	image         []byte
	polls         int
	missingCookie bool
	listBody      string
	identityBody  string
	before        func(*http.Request) (*http.Response, error, bool)
	requests      int
}

func (f *fixture) transport(req *http.Request) (*http.Response, error) {
	f.requests++
	if req.URL.Scheme != "https" {
		f.t.Fatal("credential request used HTTP")
	}
	if f.before != nil {
		if resp, err, ok := f.before(req); ok {
			return resp, err
		}
	}
	switch req.URL.Hostname() + req.URL.Path {
	case "passport.baidu.com/v2/api/getqrcode":
		return response(req, 200, `bd__cbs__qrcreate({"errno":0,"sign":"challenge-test","imgurl":"passport.baidu.com/v2/api/qrcode?sign=challenge-test"});`,
			&http.Cookie{Name: "BAIDUID", Value: "baiduid-test", Domain: ".baidu.com", Path: "/"}), nil
	case "passport.baidu.com/v2/api/qrcode":
		return response(req, 200, string(f.image)), nil
	case "passport.baidu.com/channel/unicast":
		f.polls++
		if f.polls == 1 {
			return response(req, 200, `{"errno":1}`), nil
		}
		if f.polls == 2 {
			return response(req, 200, `{"errno":0,"channel_v":"{\"status\":1}"}`), nil
		}
		return response(req, 200, `{"errno":0,"channel_v":"{\"status\":0,\"v\":\"temporary-test\"}"}`), nil
	case "passport.baidu.com/v3/login/main/qrbdusslogin":
		if req.URL.Query().Get("bduss") != "temporary-test" {
			f.t.Fatal("ticket exchange missing")
		}
		cookies := []*http.Cookie{{Name: "STOKEN", Value: "passport-stoken-test", Domain: ".passport.baidu.com", Path: "/", Secure: true}}
		if !f.missingCookie {
			cookies = append(cookies, &http.Cookie{Name: "BDUSS", Value: "bduss-test", Domain: ".baidu.com", Path: "/"})
		}
		return response(req, 200, `<html>callback({'errInfo':{'no':'0'}})</html>`, cookies...), nil
	case "pan.baidu.com/disk/main":
		return response(req, 200, "landing", &http.Cookie{Name: "STOKEN", Value: "pan-stoken-test", Domain: ".pan.baidu.com", Path: "/", Secure: true}), nil
	case "passport.baidu.com/v3/login/api/auth/":
		if cookie, err := req.Cookie("STOKEN"); err != nil || cookie.Value != "passport-stoken-test" {
			f.t.Fatal("passport received the wrong scoped STOKEN")
		}
		return response(req, 200, "synced"), nil
	case "pan.baidu.com/api/list":
		if req.URL.Query().Get("num") != "1" {
			f.t.Fatal("validation read more than one directory entry")
		}
		if cookie, err := req.Cookie("STOKEN"); err != nil || cookie.Value != "pan-stoken-test" {
			f.t.Fatal("pan received the wrong scoped STOKEN")
		}
		body := f.listBody
		if body == "" {
			body = `{"errno":0,"list":[]}`
		}
		return response(req, 200, body), nil
	case "pcs.baidu.com/rest/2.0/pcs/quota":
		if req.URL.Query().Get("app_id") != "266719" {
			f.t.Fatal("PCS validation ignored configured appid")
		}
		return response(req, 200, `{"quota":1000,"used":5}`), nil
	case "tieba.baidu.com/c/s/login":
		if req.Method != http.MethodPost {
			f.t.Fatal("identity request is not POST")
		}
		if err := req.ParseForm(); err != nil {
			f.t.Fatal(err)
		}
		if req.Form.Get("bdusstoken") != "bduss-test|null" || req.Form.Get("sign") == "" {
			f.t.Fatal("identity request not signed like legacy UID lookup")
		}
		if strings.Contains(req.Header.Get("Cookie"), "stoken-test") {
			f.t.Fatal("domain-scoped token leaked to identity endpoint")
		}
		body := f.identityBody
		if body == "" {
			body = `{"error_code":"0","user":{"id":"42","name":"test-user"}}`
		}
		return response(req, 200, body), nil
	}
	return nil, fmt.Errorf("unexpected test endpoint")
}

func testClient(t *testing.T, f *fixture, opts Options) *Client {
	t.Helper()
	f.t = t
	f.image = qrImage(t)
	if opts.OnQRCode == nil {
		opts.OnQRCode = func([]byte) error { return nil }
	}
	if opts.Timeout == 0 {
		opts.Timeout = time.Second
	}
	client, err := NewClient(&http.Client{Transport: roundTripFunc(f.transport)}, opts)
	if err != nil {
		t.Fatal(err)
	}
	client.pollInterval = time.Millisecond
	t.Cleanup(client.Close)
	return client
}

func TestOfficialQRFlowUsesExchangedCookiesAndLegacyUID(t *testing.T) {
	f := &fixture{}
	var states []State
	imageCalled := false
	client := testClient(t, f, Options{OnState: func(s State) { states = append(states, s) }, OnQRCode: func(data []byte) error { imageCalled = true; return nil }})
	session, err := client.Login(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !imageCalled || session.UID != 42 || session.Name != "test-user" {
		t.Fatal("missing verified identity or QR callback")
	}
	if len(states) < 5 || states[0] != Creating || states[len(states)-1] != Verifying {
		t.Fatalf("incomplete state transitions: %v", states)
	}
	for _, cookie := range session.Cookies {
		if cookie.Value == "temporary-test" {
			t.Fatal("temporary ticket persisted as a credential")
		}
	}
	if strings.Contains(fmt.Sprint(session), "bduss-test") {
		t.Fatal("session String exposes credentials")
	}
	if cookieValue(client.jar, panURL, "STOKEN") != "pan-stoken-test" {
		t.Fatal("netdisk STOKEN not preserved")
	}
}

func TestQRFlowRejectsIncompleteOrInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    fixture
		want error
	}{
		{"missing-session-cookie", fixture{missingCookie: true}, ErrCredentials},
		{"expired-account", fixture{listBody: `{"errno":-6}`}, nil},
		{"invalid-list-body", fixture{listBody: `{"errno":0,"list":null}`}, ErrProtocol},
		{"missing-identity", fixture{identityBody: `{"error_code":"0","user":{"id":"0","name":""}}`}, ErrProtocol},
		{"identity-failure", fixture{identityBody: `{"error_code":"4","user":{}}`}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, &tc.f, Options{})
			session, err := client.Login(context.Background())
			if err == nil || session != nil {
				t.Fatal("invalid session accepted")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestQRDeadlineAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			f := &fixture{before: func(req *http.Request) (*http.Response, error, bool) {
				if req.URL.Path == "/channel/unicast" {
					<-req.Context().Done()
					return nil, req.Context().Err(), true
				}
				return nil, nil, false
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := Options{Timeout: 20 * time.Millisecond}
			if cancelled {
				opts.OnState = func(state State) {
					if state == Waiting {
						cancel()
					}
				}
			}
			client := testClient(t, f, opts)
			_, err := client.Login(ctx)
			want := context.DeadlineExceeded
			if cancelled {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
		})
	}
}

func TestLongPollBodyTimeoutsDoNotConsumeNetworkFailureRetries(t *testing.T) {
	remaining := 3
	f := &fixture{before: func(req *http.Request) (*http.Response, error, bool) {
		if req.URL.Path == "/channel/unicast" && remaining > 0 {
			remaining--
			resp := response(req, 200, "")
			resp.Body = pendingBody{ctx: req.Context()}
			return resp, nil, true
		}
		return nil, nil, false
	}}
	client := testClient(t, f, Options{Timeout: 2 * time.Second})
	client.requestTimeout = 10 * time.Millisecond
	if session, err := client.Login(context.Background()); err != nil || session == nil {
		t.Fatal("normal long-poll body timeouts aborted the login", err)
	}
}

func TestQRRequestsDoNotExposeAuthenticationURLsInErrors(t *testing.T) {
	f := &fixture{before: func(req *http.Request) (*http.Response, error, bool) {
		return nil, &url.Error{Op: "Get", URL: "https://passport.baidu.com/?bduss=SECRET-TEST", Err: errors.New("raw-secret-response")}, true
	}}
	_, err := testClient(t, f, Options{}).Login(context.Background())
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "raw-secret") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestQRRedirectCannotDowngradeOrLeaveAllowedHosts(t *testing.T) {
	for _, target := range []string{"http://passport.baidu.com/?bduss=secret-test", "https://example.org/?bduss=secret-test"} {
		t.Run(target, func(t *testing.T) {
			f := &fixture{before: func(req *http.Request) (*http.Response, error, bool) {
				resp := response(req, 302, "")
				resp.Header.Set("Location", target)
				return resp, nil, true
			}}
			_, err := testClient(t, f, Options{}).Login(context.Background())
			if err == nil || strings.Contains(err.Error(), "secret-test") || f.requests != 1 {
				t.Fatalf("redirect was not safely blocked: %v", err)
			}
		})
	}
}

func TestUnknownQRStatusStopsWithoutGuessingOrSaving(t *testing.T) {
	f := &fixture{before: func(req *http.Request) (*http.Response, error, bool) {
		if req.URL.Path == "/channel/unicast" {
			return response(req, 200, `{"errno":0,"channel_v":{"status":987,"v":"must-not-exchange"}}`), nil, true
		}
		return nil, nil, false
	}}
	_, err := testClient(t, f, Options{}).Login(context.Background())
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != 987 || strings.Contains(err.Error(), "must-not-exchange") {
		t.Fatalf("unsafe unknown status: %v", err)
	}
}

func TestDecodeJSONPRejectsScriptsAndNeverEchoesBody(t *testing.T) {
	for _, input := range []string{`callback({"errno":0});alert("secret")`, `<html>secret</html>`, `callback({"errno":"not-a-number-secret"})`} {
		var out struct {
			Errno number `json:"errno"`
		}
		err := decodeJSON([]byte(input), &out)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe JSONP accepted")
		}
	}
	var out map[string]interface{}
	if err := decodeJSON([]byte(" callback ( {\"errno\":0} ) ; "), &out); err == nil {
		t.Fatal("unexpected whitespace in callback name accepted")
	}
	data, _ := json.Marshal(map[string]int{"errno": 0})
	if decodeJSON(data, &out) != nil {
		t.Fatal("plain JSON rejected")
	}
}
