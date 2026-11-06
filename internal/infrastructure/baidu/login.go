package baidu

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/qjfoidnh/Baidu-Login/bdcrypto"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/login"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"golang.org/x/net/publicsuffix"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type loginExchange struct {
	http                                     *requester.HTTPClient
	transport                                *http.Transport
	cancel                                   context.CancelFunc
	input                                    login.Input
	last                                     *passwordReply
	step                                     login.Step
	gid, sign, code, verifyType, verifyStage string
	once                                     sync.Once
	serverTime, rsa, trace                   string
}

func (f *ClientFactory) OpenLogin(ctx context.Context, cfg configuration.Snapshot, in login.Input) (ports.LoginExchange, error) {
	if !cfg.Client().EnableHTTPS {
		return nil, fault.New(fault.Unsupported, "HTTPS_REQUIRED", "登录需要HTTPS")
	}
	jar, e := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if e != nil {
		return nil, e
	}
	lifetime, cancel := context.WithCancel(ctx)
	t := f.newTransport(cfg.Client())
	h := requester.NewHTTPClientWithTransport(t, jar, cfg.Client().UserAgent)
	h.Timeout = 12 * time.Second
	h.Transport = &boundTransport{transport: t, lifetime: lifetime, idleTimeout: 10 * time.Second}
	h.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !loginURL(req.URL) {
			return fault.New(fault.Forbidden, "LOGIN_TARGET_FORBIDDEN", "登录重定向不允许")
		}
		return nil
	}
	h.Transport = &loginTransport{base: h.Transport}
	return &loginExchange{http: h, transport: t, cancel: cancel, input: in}, nil
}

type loginTransport struct{ base http.RoundTripper }

func (t *loginTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !loginURL(r.URL) {
		return nil, fault.New(fault.Forbidden, "LOGIN_TARGET_FORBIDDEN", "登录目标不允许")
	}
	resp, e := t.base.RoundTrip(r)
	if e == nil && resp.Body != nil {
		resp.Body = &limitedLoginBody{ReadCloser: resp.Body, remaining: 2 << 20}
	}
	return resp, e
}

type limitedLoginBody struct {
	io.ReadCloser
	remaining int64
}

func (b *limitedLoginBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, e := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, e
}
func loginURL(u *url.URL) bool {
	if u == nil || u.User != nil || u.Scheme != "https" || u.Port() != "" && u.Port() != "443" {
		return false
	}
	switch u.Hostname() {
	case "passport.baidu.com", "wappass.baidu.com", "pan.baidu.com", "www.baidu.com":
		return true
	}
	return false
}
func (e *loginExchange) Close() error {
	e.once.Do(func() {
		e.input = login.Input{}
		e.last = nil
		e.step = login.Step{}
		e.gid = ""
		e.sign = ""
		e.code = ""
		e.cancel()
		e.transport.CloseIdleConnections()
	})
	return nil
}
func (e *loginExchange) request(method, address string, values url.Values, out any) error {
	u, err := url.Parse(address)
	if err != nil || !loginURL(u) {
		return fault.New(fault.Forbidden, "LOGIN_TARGET_FORBIDDEN", "登录上游目标不允许")
	}
	var body io.Reader
	if method == "GET" {
		q := u.Query()
		for k, v := range values {
			q[k] = v
		}
		u.RawQuery = q.Encode()
	} else {
		body = strings.NewReader(values.Encode())
	}
	req, err := http.NewRequest(method, u.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Referer", "https://pan.baidu.com/")
	req.Header.Set("User-Agent", e.http.UserAgent)
	if method != "GET" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := e.http.Client.Do(req)
	if err != nil {
		return upstreamFault(err)
	}
	defer resp.Body.Close()
	if u.Hostname() == "wappass.baidu.com" && u.Path == "/" {
		e.trace = resp.Header.Get("Trace-Id")
	}
	if resp.StatusCode != 200 {
		return fault.New(fault.BadGateway, "LOGIN_UPSTREAM_ERROR", "登录上游拒绝请求")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(b) > 2<<20 {
		return fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "登录响应无效")
	}
	if raw, ok := out.(*[]byte); ok {
		*raw = b
		return nil
	}
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] != '{' {
		i := bytes.IndexByte(b, '(')
		j := bytes.LastIndexByte(b, ')')
		if i < 1 || j < i {
			return fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "登录JSONP无效")
		}
		for _, c := range b[:i] {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$' || c == '.') {
				return fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "登录JSONP无效")
			}
		}
		b = b[i+1 : j]
	}
	if json.Unmarshal(b, out) != nil {
		return fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "登录响应无效")
	}
	return nil
}
func (e *loginExchange) image(address string) ([]byte, error) {
	var b []byte
	if err := e.request("GET", address, nil, &b); err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 2048 || cfg.Height > 2048 {
		return nil, fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "登录图片无效")
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err = png.Encode(&out, img); err != nil {
		return nil, err
	}
	if out.Len() > 2<<20 {
		return nil, fault.New(fault.TooLarge, "LOGIN_IMAGE_LIMIT", "登录PNG超过2MiB")
	}
	return out.Bytes(), nil
}
func (e *loginExchange) Begin() (login.Step, error) {
	if e.input.Method == "password" {
		if err := e.initializePassword(); err != nil {
			return login.Step{}, err
		}
		return e.password("", "")
	}
	var n [16]byte
	if _, err := rand.Read(n[:]); err != nil {
		return login.Step{}, err
	}
	n[6] = (n[6] & 15) | 64
	n[8] = (n[8] & 63) | 128
	e.gid = fmt.Sprintf("%X-%X-%X-%X-%X", n[:4], n[4:6], n[6:8], n[8:10], n[10:])
	var raw struct {
		Errno *int   `json:"errno"`
		Sign  string `json:"sign"`
		Image string `json:"imgurl"`
	}
	if err := e.request("GET", "https://passport.baidu.com/v2/api/getqrcode", url.Values{"lp": {"pc"}, "qrloginfrom": {"pc"}, "tpl": {"netdisk"}, "apiver": {"v3"}, "gid": {e.gid}, "callback": {"bd__cbs__qrcreate"}}, &raw); err != nil {
		return login.Step{}, err
	}
	if raw.Errno == nil || *raw.Errno != 0 || raw.Sign == "" || raw.Image == "" {
		return login.Step{}, fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "二维码交换状态无效")
	}
	address := raw.Image
	if strings.HasPrefix(address, "//") {
		address = "https:" + address
	} else if !strings.Contains(address, "://") {
		address = "https://" + address
	}
	u, err := url.Parse(address)
	if err != nil || !loginURL(u) || u.Hostname() != "passport.baidu.com" {
		return login.Step{}, fault.New(fault.Forbidden, "LOGIN_TARGET_FORBIDDEN", "二维码来源无效")
	}
	img, err := e.image(address)
	if err != nil {
		return login.Step{}, err
	}
	e.sign = raw.Sign
	e.step = login.Step{State: "waiting", Image: img}
	return e.step, nil
}
func (e *loginExchange) password(vcode, codestr string) (login.Step, error) {
	raw, err := e.passwordRequest(vcode, codestr)
	if err != nil {
		return login.Step{}, err
	}
	return e.passwordStep(raw)
}
func (e *loginExchange) passwordStep(raw *passwordReply) (login.Step, error) {
	if raw == nil {
		return login.Step{}, fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "密码响应缺失")
	}
	e.last = raw
	switch raw.ErrInfo.No {
	case "0":
		c, err := e.credentials("https://wappass.baidu.com/", raw.Data.SToken)
		if err != nil {
			c, err = account.Normalize(account.Import{Kind: "bduss", BDUSS: raw.Data.BDUSS, PToken: raw.Data.PToken, SToken: raw.Data.SToken})
		}
		if err != nil {
			return login.Step{}, fault.New(fault.BadGateway, "LOGIN_EXCHANGE_INCOMPLETE", "登录未取得可验证凭据")
		}
		e.step = login.Step{State: "confirmed", Credentials: c}
		e.input.Password = ""
		return e.step, nil
	case "500001", "500002":
		if raw.Data.CodeString == "" {
			return login.Step{}, fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "图片挑战缺少状态")
		}
		e.code = raw.Data.CodeString
		img, err := e.image("https://wappass.baidu.com/cgi-bin/genimage?" + url.QueryEscape(e.code))
		if err != nil {
			return login.Step{}, err
		}
		e.step = login.Step{State: "challenge_required", ChallengeType: "captcha", Prompt: "输入图片验证码", Image: img}
		return e.step, nil
	case "400023", "400101":
		e.verifyType = "mobile"
		kind := "sms"
		if raw.Data.Phone == "" && raw.Data.Email != "" {
			e.verifyType = "email"
			kind = "email"
		}
		if raw.Data.Phone == "" && raw.Data.Email == "" {
			return login.Step{}, fault.New(fault.Unsupported, "LOGIN_CHALLENGE_UNSUPPORTED", "上游验证方式未实现")
		}
		e.verifyStage = "select"
		e.step = login.Step{State: "challenge_required", ChallengeType: kind, Prompt: "响应send发送验证码；sms或email选择验证方式"}
		return e.step, nil
	default:
		return login.Step{}, fault.New(fault.Unsupported, "LOGIN_REJECTED", "百度拒绝密码登录或要求未支持的挑战")
	}
}
func (e *loginExchange) Respond(response string) (login.Step, error) {
	if e.step.State != "challenge_required" {
		return e.step, fault.New(fault.Conflict, "LOGIN_STATE_CONFLICT", "当前会话没有待响应挑战")
	}
	if e.step.ChallengeType == "captcha" {
		return e.password(response, e.code)
	}
	if e.step.ChallengeType != "sms" && e.step.ChallengeType != "email" {
		return e.step, fault.New(fault.Unsupported, "LOGIN_CHALLENGE_UNSUPPORTED", "此挑战尚不支持")
	}
	raw := e.last
	if raw == nil {
		return e.step, fault.New(fault.Conflict, "LOGIN_STATE_CONFLICT", "交换状态已失效")
	}
	if e.verifyStage == "select" {
		switch response {
		case "send":
		case "sms":
			if raw.Data.Phone == "" {
				return e.step, fault.New(fault.Unsupported, "LOGIN_CHALLENGE_UNSUPPORTED", "短信不可用")
			}
			e.verifyType = "mobile"
		case "email":
			if raw.Data.Email == "" {
				return e.step, fault.New(fault.Unsupported, "LOGIN_CHALLENGE_UNSUPPORTED", "邮箱不可用")
			}
			e.verifyType = "email"
		default:
			return e.step, fault.New(fault.Invalid, "INVALID_REQUEST", "先选择并发送验证码")
		}
		message := ""
		if raw.Data.AuthID != "" {
			message = e.sendCode(raw)
		} else {
			message = e.sendCode(raw)
		}
		if strings.Contains(message, "系统出错") || strings.Contains(message, "未知消息") || message == "" {
			return e.step, fault.New(fault.BadGateway, "LOGIN_CHALLENGE_SEND_FAILED", "上游未确认验证码发送")
		}
		e.verifyStage = "verify"
		kind := "sms"
		if e.verifyType == "email" {
			kind = "email"
		}
		e.step = login.Step{State: "challenge_required", ChallengeType: kind, Prompt: "输入收到的验证码"}
		return e.step, nil
	}
	next, err := e.verifyCode(raw, response)
	if err != nil {
		return e.step, err
	}
	if next.ErrInfo.No != "0" {
		return e.step, fault.New(fault.Unsupported, "LOGIN_CHALLENGE_REJECTED", "上游拒绝挑战响应")
	}
	if next.Data.BDUSS == "" && next.Data.CookieString == "" {
		return e.password("", "")
	}
	return e.passwordStep(next)
}
func (e *loginExchange) Poll() (login.Step, error) {
	if e.input.Method != "qrcode" || e.step.State != "waiting" {
		return e.step, nil
	}
	var raw struct {
		Errno   *int            `json:"errno"`
		Channel json.RawMessage `json:"channel_v"`
	}
	if err := e.request("GET", "https://passport.baidu.com/channel/unicast", url.Values{"channel_id": {e.sign}, "gid": {e.gid}, "tpl": {"netdisk"}, "apiver": {"v3"}, "_sdkFrom": {"1"}, "callback": {"bd__cbs__qrpoll"}}, &raw); err != nil {
		return e.step, err
	}
	if raw.Errno == nil {
		return e.step, fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "扫码轮询状态缺失")
	}
	if *raw.Errno == 1 {
		return e.step, nil
	}
	if *raw.Errno != 0 {
		return e.step, fault.New(fault.Unsupported, "LOGIN_REJECTED", "上游拒绝扫码轮询")
	}
	b := bytes.TrimSpace(raw.Channel)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if json.Unmarshal(b, &s) != nil {
			return e.step, fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "扫码消息无效")
		}
		b = []byte(s)
	}
	var v struct {
		Status *int   `json:"status"`
		Ticket string `json:"v"`
	}
	if json.Unmarshal(b, &v) != nil || v.Status == nil {
		return e.step, fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "扫码消息无效")
	}
	if *v.Status == 1 {
		return e.step, nil
	}
	if *v.Status != 0 || v.Ticket == "" {
		return e.step, fault.New(fault.Unsupported, "LOGIN_CHALLENGE_UNSUPPORTED", "扫码被拒绝或需要额外安全验证")
	}
	var exchange json.RawMessage
	if err := e.request("GET", "https://passport.baidu.com/v3/login/main/qrbdusslogin", url.Values{"bduss": {v.Ticket}, "gid": {e.gid}, "u": {"https://pan.baidu.com/disk/main"}, "tpl": {"netdisk"}, "apiver": {"v3"}, "qrcode": {"1"}, "loginVersion": {"v4"}, "callback": {"bd__cbs__login"}, "time": {strconv.FormatInt(time.Now().Unix(), 10)}}, &exchange); err != nil {
		return e.step, err
	}
	var ignored []byte
	if err := e.request("GET", "https://pan.baidu.com/disk/main", nil, &ignored); err != nil {
		return e.step, err
	}
	if err := e.request("GET", "https://passport.baidu.com/v3/login/api/auth/", url.Values{"return_type": {"5"}, "tpl": {"netdisk"}, "u": {"https://pan.baidu.com/disk/main"}}, &ignored); err != nil {
		return e.step, err
	}
	origin, _ := url.Parse("https://pan.baidu.com/")
	parts := []string{}
	for _, c := range e.http.Jar.Cookies(origin) {
		parts = append(parts, c.Name+"="+c.Value)
	}
	c, err := account.Normalize(account.Import{Kind: "cookies", Cookies: strings.Join(parts, ";")})
	if err != nil {
		return e.step, fault.New(fault.BadGateway, "LOGIN_EXCHANGE_INCOMPLETE", "未取得完整网盘凭据")
	}
	e.step = login.Step{State: "confirmed", Credentials: c}
	e.sign = ""
	return e.step, nil
}

type passwordReply struct {
	ErrInfo struct {
		No string `json:"no"`
	} `json:"errInfo"`
	Data struct {
		CodeString   string `json:"codeString"`
		GotoURL      string `json:"gotoUrl"`
		Token        string `json:"token"`
		U            string `json:"u"`
		AuthSID      string `json:"authsid"`
		AuthID       string `json:"authid"`
		Phone        string `json:"phone"`
		Email        string `json:"email"`
		VerifyURL    string `json:"verifyUrl"`
		LoginProxy   string `json:"loginProxy"`
		BDUSS        string `json:"bduss"`
		PToken       string `json:"ptoken"`
		SToken       string `json:"stoken"`
		CookieString string `json:"cookieString"`
	} `json:"data"`
}

func (e *loginExchange) initializePassword() error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	e.gid = hex.EncodeToString(nonce[:])
	var data struct {
		Time string `json:"time"`
	}
	if err := e.request("GET", "https://wappass.baidu.com/wp/api/security/antireplaytoken", nil, &data); err != nil {
		return err
	}
	if data.Time == "" {
		return fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "密码防重放状态缺失")
	}
	e.serverTime = data.Time
	var js []byte
	if err := e.request("GET", "https://wappass.baidu.com/static/touch/js/login_d9bffc9.js", nil, &js); err != nil {
		return err
	}
	key := regexp.MustCompile(`,rsa:"([0-9A-Fa-f]+)",error:`).FindSubmatch(js)
	e.rsa = legacyLoginRSA
	if len(key) == 2 {
		e.rsa = string(key[1])
	}
	if len(e.rsa) > 1024 {
		return fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "登录公钥无效")
	}
	var ignored []byte
	return e.request("GET", "https://wappass.baidu.com/", nil, &ignored)
}
func (e *loginExchange) passwordRequest(vcode, codestr string) (*passwordReply, error) {
	encoded, err := bdcrypto.RSAEncryptOfWapBaidu(e.rsa, []byte(e.input.Password+e.serverTime))
	if err != nil {
		return nil, fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "密码加密准备失败")
	}
	v := url.Values{"username": {e.input.Username}, "password": {encoded}, "verifycode": {vcode}, "vcodestr": {codestr}, "isphone": {"0"}, "loginmerge": {"1"}, "action": {"login"}, "uid": {strconv.FormatInt(time.Now().Unix(), 10) + "773_357"}, "skin": {"default_v2"}, "connect": {"0"}, "dv": {legacyLoginDV}, "mobilenum": {"undefined"}, "servertime": {e.serverTime}, "gid": {e.gid}, "logLoginType": {"wap_loginTouch"}, "FP_UID": {"0b58c206c9faa8349576163341ef1321"}, "traceid": {e.trace}}
	var raw passwordReply
	if err := e.request("POST", "https://wappass.baidu.com/wp/api/login", v, &raw); err != nil {
		return nil, err
	}
	if raw.ErrInfo.No == "" {
		return nil, fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "密码登录响应缺少状态")
	}
	if raw.ErrInfo.No == "400023" || raw.ErrInfo.No == "400101" {
		u, err := url.Parse(raw.Data.VerifyURL)
		if err != nil || !loginURL(u) {
			return nil, fault.New(fault.Forbidden, "LOGIN_TARGET_FORBIDDEN", "验证目标不允许")
		}
		q := u.Query()
		if raw.Data.AuthID == "" {
			raw.Data.AuthID = q.Get("authid")
		}
		if raw.Data.Token == "" {
			raw.Data.Token = q.Get("token")
		}
		if raw.Data.U == "" {
			raw.Data.U = q.Get("u")
		}
		if raw.Data.GotoURL != "" {
			var ignored []byte
			if err := e.request("GET", u.String(), nil, &ignored); err != nil {
				return nil, err
			}
		}
	}
	return &raw, nil
}
func (e *loginExchange) credentials(origin, stoken string) (account.Credentials, error) {
	u, _ := url.Parse(origin)
	parts := []string{}
	for _, c := range e.http.Jar.Cookies(u) {
		if c.Name == "STOKEN" && stoken != "" {
			continue
		}
		parts = append(parts, c.Name+"="+c.Value)
	}
	if stoken != "" {
		parts = append(parts, "STOKEN="+stoken)
	}
	return account.Normalize(account.Import{Kind: "cookies", Cookies: strings.Join(parts, ";")})
}
func (e *loginExchange) widget(action string, raw *passwordReply, code string) ([]byte, error) {
	payload, _ := json.Marshal(map[string]any{"tpl": "wi", "client": "", "clientfrom": "", "authid": raw.Data.AuthID, "authField": e.verifyType, "authType": "check", "action": action, "authParams": map[string]string{"banktype": "", "type": "vcode", "vcode": code, "secret": ""}})
	req, err := http.NewRequest("POST", "https://wappass.baidu.com/v3/api/auth/widget", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", "https://wappass.baidu.com/")
	resp, err := e.http.Client.Do(req)
	if err != nil {
		return nil, upstreamFault(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || resp.StatusCode != 200 || len(b) > 1<<20 {
		return nil, fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "验证响应无效")
	}
	return b, nil
}
func (e *loginExchange) sendCode(raw *passwordReply) string {
	var b []byte
	var err error
	if raw.Data.AuthID != "" {
		b, err = e.widget("send", raw, "")
	} else {
		err = e.request("GET", "https://wappass.baidu.com/passport/authwidget", url.Values{"action": {"send"}, "tpl": {""}, "type": {e.verifyType}, "token": {raw.Data.Token}, "adapter": {"2"}}, &b)
	}
	if err != nil {
		return "系统出错"
	}
	if raw.Data.AuthID != "" {
		if regexp.MustCompile(`"send"\s*:\s*"success"`).Match(b) {
			return "sent"
		}
	} else {
		if regexp.MustCompile(`(?:已|已经).*发送|发送.*成功`).Match(b) {
			return "sent"
		}
	}
	return "系统出错"
}
func (e *loginExchange) verifyCode(raw *passwordReply, answer string) (*passwordReply, error) {
	var result passwordReply
	if raw.Data.AuthID != "" {
		b, err := e.widget("check", raw, answer)
		if err != nil {
			return nil, err
		}
		if !regexp.MustCompile(`"check"\s*:\s*"success"`).Match(b) || !regexp.MustCompile(`"status"\s*:\s*"authed"`).Match(b) {
			return nil, fault.New(fault.Unsupported, "LOGIN_CHALLENGE_REJECTED", "验证码未被确认")
		}
		if raw.Data.LoginProxy == "" {
			result.ErrInfo.No = "0"
			return &result, nil
		}
		var ignored []byte
		if err := e.request("GET", raw.Data.LoginProxy, url.Values{"authsid": {raw.Data.AuthSID}, "fromtype": {e.verifyType}, "bindToSmsLogin": {""}}, &ignored); err != nil {
			return nil, err
		}
		result.ErrInfo.No = "0"
		c, err := e.credentials("https://wappass.baidu.com/", "")
		if err == nil {
			result.Data.BDUSS = c.Cookies["BDUSS"]
		}
		return &result, nil
	}
	if err := e.request("GET", "https://wappass.baidu.com/passport/authwidget", url.Values{"vcode": {answer}, "token": {raw.Data.Token}, "u": {raw.Data.U}, "action": {"check"}, "type": {e.verifyType}, "adapter": {"2"}, "callback": {"jsonp1"}}, &result); err != nil {
		return nil, err
	}
	if result.ErrInfo.No != "0" {
		return &result, nil
	}
	var ignored []byte
	if err := e.request("GET", raw.Data.U, url.Values{"authsid": {result.Data.AuthSID}, "fromtype": {e.verifyType}, "bindToSmsLogin": {""}}, &ignored); err != nil {
		return nil, err
	}
	c, err := e.credentials(raw.Data.U, "")
	if err == nil {
		result.Data.BDUSS = c.Cookies["BDUSS"]
	}
	return &result, nil
}

// Wire constants from the existing v1.4.1 password flow; no CLI globals.
const legacyLoginRSA = "B3C61EBBA4659C4CE3639287EE871F1F48F7930EA977991C7AFE3CC442FEA49643212E7D570C853F368065CC57A2014666DA8AE7D493FD47D171C0D894EEE3ED7F99F6798B7FFD7B5873227038AD23E3197631A8CB642213B9F27D4901AB0D92BFA27542AE890855396ED92775255C977F5C302F1E7ED4B1E369C12CB6B1822F"
const legacyLoginDV = "tk0.408376350146535171516806245342@oov0QqrkqfOuwaCIxUELn3oYlSOI8f51tbnGy-nk3crkqfOuwaCIxUou2iobENoYBf51tb4Gy-nk3cuv0ounk5vrkBynGyvn1QzruvN6z3drLJi6LsdFIe3rkt~4Lyz5ktfn1Qlrk5v5D5fOuwaCIxUobJWOI3~rkt~4Lyi5kBfni0vrk8~n15fOuwaCIxUobJWOI3~rkt~4Lyz5DQfn1oxrk0v5k5eruvN6z3drLneFYeVEmy-nk3c-qq6Cqw3h7CChwvi5-y-rkFizvmEufyr1By4k5bn15e5k0~n18inD0b5D8vn1Tyn1t~nD5~5T__ivmCpA~op5gr-wbFLhyFLnirYsSCIAerYnNOGcfEIlQ6I6VOYJQIvh515f51tf5DBv5-yln15f5DFy5myl5kqf5DFy5myvnktxrkT-5T__Hv0nq5myv5myv4my-nWy-4my~n-yz5myz4Gyx4myv5k0f5Dqirk0ynWyv5iTf5DB~rk0z5Gyv4kTf5DQxrkty5Gy-5iQf51B-rkt~4B__"
