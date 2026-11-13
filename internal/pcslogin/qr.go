// Package pcslogin implements Baidu's browser QR login without a browser or a
// local web server. Authentication responses and tokens must not be logged.
package pcslogin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/baidu-tools/tieba/tiebautil"
)

const (
	passportURL      = "https://passport.baidu.com"
	panURL           = "https://pan.baidu.com"
	BrowserUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	DefaultTimeout   = 5 * time.Minute
	maxResponseSize  = 2 << 20
)

var (
	ErrProtocol             = errors.New("百度扫码接口返回格式异常")
	ErrNetwork              = errors.New("百度扫码登录网络请求失败")
	ErrVerificationRequired = errors.New("需要完成百度官方安全验证后重新扫码")
	ErrCredentials          = errors.New("没有取得完整的网盘登录凭证")
)

// RemoteError deliberately excludes the response body, URL and server message.
type RemoteError struct {
	Stage string
	Code  int
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("%s失败，百度返回码 %d", e.Stage, e.Code)
}

type State string

const (
	Creating   State = "正在获取二维码"
	Waiting    State = "请使用百度网盘官方 App 扫码，并在手机上确认登录"
	Scanned    State = "已扫码，请在手机上确认登录"
	Exchanging State = "正在获取网盘登录会话"
	Verifying  State = "正在验证账号和网盘访问"
)

type Options struct {
	Timeout  time.Duration
	AppID    int
	OnQRCode func([]byte) error
	OnState  func(State)
}

// Session is only returned after identity and read-only disk checks succeed.
// Its UID uses the same Baidu/Tieba identity as legacy accounts, not netdisk UK.
type Session struct {
	UID     uint64
	Name    string
	Cookies []requester.CookieEntry
}

func (s Session) String() string { return "QRLoginSession{credentials redacted}" }

type Client struct {
	http           *http.Client
	jar            *requester.CookieJar
	opts           Options
	pollInterval   time.Duration
	requestTimeout time.Duration
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

// NewClient copies the supplied client. nil uses the project's secure transport
// and proxy settings. The authentication cookie jar never shares an account jar.
func NewClient(client *http.Client, opts Options) (*Client, error) {
	if opts.Timeout == 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.Timeout < 0 {
		return nil, fmt.Errorf("扫码超时必须大于零")
	}
	if opts.AppID == 0 {
		opts.AppID = 266719
	}
	if opts.AppID < 0 {
		return nil, fmt.Errorf("无效的 appid")
	}
	if opts.OnQRCode == nil {
		return nil, fmt.Errorf("没有设置二维码显示方式")
	}
	if client == nil {
		secure := requester.NewSecureHTTPClient()
		client = &secure.Client
	}
	h := *client
	jar, err := requester.NewCookieJar(nil)
	if err != nil {
		return nil, err
	}
	h.Jar = jar
	h.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !allowedURL(req.URL) {
			return fmt.Errorf("登录重定向不被允许")
		}
		return nil
	}
	return &Client{http: &h, jar: jar, opts: opts, pollInterval: 2 * time.Second, requestTimeout: 40 * time.Second}, nil
}

func allowedURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	switch u.Hostname() {
	case "passport.baidu.com", "pan.baidu.com", "wappass.baidu.com", "www.baidu.com", "pcs.baidu.com", "tieba.baidu.com":
		return true
	}
	return false
}

func (c *Client) state(state State) {
	if c.opts.OnState != nil {
		c.opts.OnState(state)
	}
}

// request does not return url.Error, JSON errors or raw server messages: those
// can contain the authentication URL, ticket or response cookie values.
func (c *Client) request(ctx context.Context, stage, method, endpoint string, params url.Values) ([]byte, error) {
	u, err := url.Parse(endpoint)
	if err != nil || !allowedURL(u) {
		return nil, fmt.Errorf("%s: 不允许的登录地址", stage)
	}
	var body io.Reader
	if method == http.MethodGet {
		u.RawQuery = params.Encode()
	} else {
		body = strings.NewReader(params.Encode())
	}
	reqCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", stage, ErrProtocol)
	}
	req.Header.Set("User-Agent", BrowserUserAgent)
	req.Header.Set("Referer", panURL+"/")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if u.Hostname() == "tieba.baidu.com" {
			req.Header.Set("User-Agent", "bdtb for Android 6.9.2.1")
			req.Header.Set("Cookie", "ka=open")
			req.Header.Set("net", "1")
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, &requestTimeoutError{stage: stage}
		}
		return nil, fmt.Errorf("%s: %w", stage, ErrNetwork)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		var netErr net.Error
		if reqCtx.Err() == context.DeadlineExceeded || errors.As(err, &netErr) && netErr.Timeout() {
			return nil, &requestTimeoutError{stage: stage}
		}
		return nil, fmt.Errorf("%s: %w", stage, ErrNetwork)
	}
	if len(data) > maxResponseSize {
		return nil, fmt.Errorf("%s: 响应超过大小限制", stage)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Preserve only a numeric service code when a failed HTTP request has JSON.
		var failure struct {
			ErrorCode *number `json:"error_code"`
			Errno     *number `json:"errno"`
		}
		if decodeJSON(data, &failure) == nil {
			if failure.ErrorCode != nil {
				return nil, &RemoteError{stage, int(*failure.ErrorCode)}
			}
			if failure.Errno != nil {
				return nil, &RemoteError{stage, int(*failure.Errno)}
			}
		}
		return nil, fmt.Errorf("%s: HTTP 状态 %d", stage, resp.StatusCode)
	}
	return data, nil
}

type requestTimeoutError struct{ stage string }

func (e *requestTimeoutError) Error() string { return e.stage + ": 请求超时" }

// number accepts the numeric and quoted numeric codes both used by Baidu.
type number int64

func (n *number) UnmarshalJSON(data []byte) error {
	var text string
	if len(data) > 0 && data[0] == '"' {
		if json.Unmarshal(data, &text) != nil {
			return ErrProtocol
		}
	} else {
		text = string(data)
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return ErrProtocol
	}
	*n = number(value)
	return nil
}

func decodeJSON(data []byte, out interface{}) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return ErrProtocol
	}
	if data[0] != '{' && data[0] != '[' {
		open := bytes.IndexByte(data, '(')
		if open < 1 {
			return ErrProtocol
		}
		for _, r := range string(data[:open]) {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '$' || r == '.') {
				return ErrProtocol
			}
		}
		end := len(data) - 1
		if data[end] == ';' {
			end--
			for end >= 0 && (data[end] == ' ' || data[end] == '\n' || data[end] == '\r' || data[end] == '\t') {
				end--
			}
		}
		if end <= open || data[end] != ')' {
			return ErrProtocol
		}
		data = data[open+1 : end]
	}
	if json.Unmarshal(data, out) != nil {
		return ErrProtocol
	}
	return nil
}

func timestamp() string { return strconv.FormatInt(time.Now().UnixMilli(), 10) }
func newGID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("无法生成扫码会话标识")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%X-%X-%X-%X-%X", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func (c *Client) Login(ctx context.Context) (*Session, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
	defer cancel()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	c.state(Creating)
	gid, err := newGID()
	if err != nil {
		return nil, err
	}
	now := timestamp()
	params := url.Values{"lp": {"pc"}, "qrloginfrom": {"pc"}, "tpl": {"netdisk"}, "apiver": {"v3"}, "gid": {gid},
		"callback": {"bd__cbs__qrcreate"}, "tt": {now}, "_": {now}, "logPage": {"traceId:pc_loginv4,logPage:loginv4"}}
	data, err := c.request(ctx, "获取二维码", http.MethodGet, passportURL+"/v2/api/getqrcode", params)
	if err != nil {
		return nil, err
	}
	var challenge struct {
		Errno    *number `json:"errno"`
		Sign     string  `json:"sign"`
		ImageURL string  `json:"imgurl"`
	}
	if decodeJSON(data, &challenge) != nil || challenge.Errno == nil {
		return nil, ErrProtocol
	}
	if *challenge.Errno != 0 {
		return nil, &RemoteError{"获取二维码", int(*challenge.Errno)}
	}
	if challenge.Sign == "" || challenge.ImageURL == "" {
		return nil, ErrProtocol
	}
	imageURL := challenge.ImageURL
	if strings.HasPrefix(imageURL, "//") {
		imageURL = "https:" + imageURL
	} else if !strings.Contains(imageURL, "://") {
		imageURL = "https://" + imageURL
	}
	u, err := url.Parse(imageURL)
	if err != nil || !allowedURL(u) || u.Hostname() != "passport.baidu.com" {
		return nil, ErrProtocol
	}
	imageQuery := u.Query()
	u.RawQuery = ""
	data, err = c.request(ctx, "下载二维码", http.MethodGet, u.String(), imageQuery)
	if err != nil {
		return nil, err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 2048 || config.Height > 2048 {
		return nil, fmt.Errorf("二维码图片无效")
	}
	if err = c.opts.OnQRCode(data); err != nil {
		return nil, err
	}
	c.state(Waiting)
	ticket, err := c.wait(ctx, gid, challenge.Sign)
	if err != nil {
		return nil, err
	}
	c.state(Exchanging)
	if err = c.exchange(ctx, gid, ticket); err != nil {
		return nil, err
	}
	c.state(Verifying)
	return c.verify(ctx)
}

func pause(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) wait(ctx context.Context, gid, sign string) (string, error) {
	scanned := false
	failures := 0
	for {
		now := timestamp()
		data, err := c.request(ctx, "等待扫码", http.MethodGet, passportURL+"/channel/unicast", url.Values{
			"channel_id": {sign}, "gid": {gid}, "tpl": {"netdisk"}, "apiver": {"v3"}, "_sdkFrom": {"1"}, "callback": {"bd__cbs__qrpoll"}, "tt": {now}, "_": {now}})
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			var timeout *requestTimeoutError
			if !errors.As(err, &timeout) {
				if !errors.Is(err, ErrNetwork) {
					return "", err
				}
				failures++
				if failures >= 3 {
					return "", err
				}
			}
		} else {
			failures = 0
			var response struct {
				Errno   *number         `json:"errno"`
				Channel json.RawMessage `json:"channel_v"`
			}
			if decodeJSON(data, &response) != nil || response.Errno == nil {
				return "", ErrProtocol
			}
			switch *response.Errno {
			case 1: // Long-poll returned without a message, observed in the live trial.
			case 0:
				channel := bytes.TrimSpace(response.Channel)
				if len(channel) == 0 || bytes.Equal(channel, []byte("null")) {
					return "", ErrProtocol
				}
				if channel[0] == '"' {
					var text string
					if json.Unmarshal(channel, &text) != nil {
						return "", ErrProtocol
					}
					channel = []byte(text)
				}
				var value struct {
					Status *number `json:"status"`
					Ticket string  `json:"v"`
				}
				if decodeJSON(channel, &value) != nil || value.Status == nil {
					return "", ErrProtocol
				}
				switch *value.Status {
				case 0:
					if value.Ticket == "" {
						return "", ErrCredentials
					}
					return value.Ticket, nil
				case 1:
					if !scanned {
						c.state(Scanned)
						scanned = true
					}
				default:
					// Do not guess meanings of undocumented server status codes.
					return "", &RemoteError{"扫码授权", int(*value.Status)}
				}
			default:
				return "", &RemoteError{"等待扫码", int(*response.Errno)}
			}
		}
		if err := pause(ctx, c.pollInterval); err != nil {
			return "", err
		}
	}
}

func (c *Client) exchange(ctx context.Context, gid, ticket string) error {
	now := timestamp()
	data, err := c.request(ctx, "交换登录凭证", http.MethodGet, passportURL+"/v3/login/main/qrbdusslogin", url.Values{
		"v": {now}, "bduss": {ticket}, "gid": {gid}, "u": {panURL + "/disk/main"}, "loginVersion": {"v4"}, "qrcode": {"1"},
		"tpl": {"netdisk"}, "apiver": {"v3"}, "tt": {now}, "traceid": {""}, "time": {strconv.FormatInt(time.Now().Unix(), 10)},
		"alg": {"v3"}, "callback": {"bd__cbs__login"}})
	if err != nil {
		return err
	}
	if cookieValue(c.jar, panURL, "BDUSS") == "" {
		var info struct {
			ErrInfo struct {
				No number `json:"no"`
			} `json:"errInfo"`
		}
		if decodeJSON(data, &info) == nil && (info.ErrInfo.No == 400101 || info.ErrInfo.No == 400023) {
			return ErrVerificationRequired
		}
		return ErrCredentials
	}
	if _, err = c.request(ctx, "同步网盘会话", http.MethodGet, panURL+"/disk/main", nil); err != nil {
		return err
	}
	_, err = c.request(ctx, "同步网盘认证", http.MethodGet, passportURL+"/v3/login/api/auth/", url.Values{
		"return_type": {"5"}, "tpl": {"netdisk"}, "u": {panURL + "/disk/main"}})
	return err
}

func cookieValue(jar http.CookieJar, endpoint, name string) string {
	u, _ := url.Parse(endpoint)
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func (c *Client) verify(ctx context.Context) (*Session, error) {
	for _, name := range []string{"BDUSS", "STOKEN", "BAIDUID"} {
		if cookieValue(c.jar, panURL, name) == "" {
			return nil, ErrCredentials
		}
	}
	data, err := c.request(ctx, "验证网盘目录", http.MethodGet, panURL+"/api/list", url.Values{
		"dir": {"/"}, "num": {"1"}, "page": {"1"}, "order": {"name"}, "desc": {"0"}, "clienttype": {"0"},
		"channel": {"chunlei"}, "web": {"1"}, "app_id": {"250528"}})
	if err != nil {
		return nil, err
	}
	var list struct {
		Errno *number         `json:"errno"`
		List  json.RawMessage `json:"list"`
	}
	if decodeJSON(data, &list) != nil || list.Errno == nil {
		return nil, ErrProtocol
	}
	if *list.Errno != 0 {
		return nil, &RemoteError{"验证网盘目录", int(*list.Errno)}
	}
	var entries []json.RawMessage
	if len(list.List) == 0 || bytes.Equal(bytes.TrimSpace(list.List), []byte("null")) || json.Unmarshal(list.List, &entries) != nil {
		return nil, ErrProtocol
	}
	data, err = c.request(ctx, "验证网盘容量", http.MethodGet, "https://pcs.baidu.com/rest/2.0/pcs/quota", url.Values{
		"method": {"info"}, "app_id": {strconv.Itoa(c.opts.AppID)}})
	if err != nil {
		return nil, err
	}
	var quota struct {
		ErrorCode *number `json:"error_code"`
		Quota     *int64  `json:"quota"`
		Used      *int64  `json:"used"`
	}
	if decodeJSON(data, &quota) != nil {
		return nil, ErrProtocol
	}
	if quota.ErrorCode != nil && *quota.ErrorCode != 0 {
		return nil, &RemoteError{"验证网盘容量", int(*quota.ErrorCode)}
	}
	if quota.Quota == nil || quota.Used == nil || *quota.Quota < 0 || *quota.Used < 0 {
		return nil, ErrProtocol
	}
	uid, name, err := c.identity(ctx)
	if err != nil {
		return nil, err
	}
	return &Session{UID: uid, Name: name, Cookies: c.jar.Entries()}, nil
}

func (c *Client) identity(ctx context.Context) (uint64, string, error) {
	// Same signed request and ID semantics as baidu-tools.NewUserInfoByBDUSS,
	// using HTTPS and this session's verified transport instead of its HTTP URL.
	post := map[string]string{"bdusstoken": cookieValue(c.jar, panURL, "BDUSS") + "|null", "channel_id": "", "channel_uid": "",
		"stErrorNums": "0", "subapp_type": "mini", "timestamp": strconv.FormatInt(time.Now().Unix(), 10) + "922"}
	tiebautil.TiebaClientSignature(post)
	values := url.Values{}
	for key, value := range post {
		values.Set(key, value)
	}
	data, err := c.request(ctx, "验证账号身份", http.MethodPost, "https://tieba.baidu.com/c/s/login", values)
	if err != nil {
		return 0, "", err
	}
	var identity struct {
		ErrorCode *number `json:"error_code"`
		User      struct {
			ID   number `json:"id"`
			Name string `json:"name"`
		} `json:"user"`
	}
	if decodeJSON(data, &identity) != nil || identity.ErrorCode == nil {
		return 0, "", ErrProtocol
	}
	if *identity.ErrorCode != 0 {
		return 0, "", &RemoteError{"验证账号身份", int(*identity.ErrorCode)}
	}
	if identity.User.ID <= 0 || strings.TrimSpace(identity.User.Name) == "" {
		return 0, "", ErrProtocol
	}
	return uint64(identity.User.ID), identity.User.Name, nil
}
