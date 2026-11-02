package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
)

var pcsHostPattern = regexp.MustCompile(`^([cd][0-9]?\.)?pcs\.baidu\.com$`)
var proxyRulePattern = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9.-]+$`)

type clientDTO struct {
	AppID              int                      `json:"appid"`
	CacheSize          string                   `json:"cache_size"`
	MaxParallel        int                      `json:"max_parallel"`
	MaxUploadParallel  int                      `json:"max_upload_parallel"`
	MaxDownloadLoad    int                      `json:"max_download_load"`
	MaxUploadLoad      int                      `json:"max_upload_load"`
	MaxDownloadRate    string                   `json:"max_download_rate"`
	MaxUploadRate      string                   `json:"max_upload_rate"`
	UserAgent          string                   `json:"user_agent"`
	PCSUA              string                   `json:"pcs_ua"`
	PCSAddr            string                   `json:"pcs_addr"`
	PanUA              string                   `json:"pan_ua"`
	SaveDir            *configuration.Directory `json:"savedir"`
	EnableHTTPS        bool                     `json:"enable_https"`
	FixPCSAddr         bool                     `json:"fix_pcs_addr"`
	ForceLoginUsername string                   `json:"force_login_username"`
	Proxy              proxyDTO                 `json:"proxy"`
	ProxyHostnames     []string                 `json:"proxy_hostnames"`
	LocalAddrs         []string                 `json:"local_addrs"`
	NoCheck            bool                     `json:"no_check"`
	IgnoreIllegal      bool                     `json:"ignore_illegal"`
	UploadPolicy       string                   `json:"upload_policy"`
}
type proxyDTO struct {
	Configured  bool    `json:"configured"`
	RedactedURL *string `json:"redacted_url"`
}

func publicClient(c configuration.Client) clientDTO {
	proxy := proxyDTO{Configured: c.Proxy != ""}
	if u, err := url.Parse(c.Proxy); err == nil && c.Proxy != "" {
		u.User = nil
		redacted := u.String()
		proxy.RedactedURL = &redacted
	}
	return clientDTO{AppID: c.AppID, CacheSize: strconv.FormatInt(c.CacheSize, 10), MaxParallel: c.MaxParallel, MaxUploadParallel: c.MaxUploadParallel, MaxDownloadLoad: c.MaxDownloadLoad, MaxUploadLoad: c.MaxUploadLoad, MaxDownloadRate: strconv.FormatInt(c.MaxDownloadRate, 10), MaxUploadRate: strconv.FormatInt(c.MaxUploadRate, 10), UserAgent: c.UserAgent, PCSUA: c.PCSUA, PCSAddr: c.PCSAddr, PanUA: c.PanUA, SaveDir: c.SaveDir, EnableHTTPS: c.EnableHTTPS, FixPCSAddr: c.FixPCSAddr, ForceLoginUsername: c.ForceLoginUsername, Proxy: proxy, ProxyHostnames: append([]string{}, c.ProxyHostnames...), LocalAddrs: append([]string{}, c.LocalAddrs...), NoCheck: c.NoCheck, IgnoreIllegal: c.IgnoreIllegal, UploadPolicy: c.UploadPolicy}
}

type configDTO struct {
	Revision string    `json:"revision"`
	Client   clientDTO `json:"client"`
	Pending  []string  `json:"pending_restart_fields"`
}

func publicSnapshot(snapshot configuration.Snapshot) configDTO {
	return configDTO{Revision: strconv.FormatUint(snapshot.Revision(), 10), Client: publicClient(snapshot.Client()), Pending: []string{}}
}

type changeDTO struct {
	Configuration configDTO `json:"configuration"`
	Immediate     []string  `json:"immediate_fields"`
	NewJob        []string  `json:"new_job_fields"`
	Restart       []string  `json:"restart_required_fields"`
}

func publicChange(change configuration.Change) changeDTO {
	return changeDTO{Configuration: publicSnapshot(change.Snapshot), Immediate: []string{}, NewJob: append([]string{}, change.Fields...), Restart: []string{}}
}

type directoryInput struct {
	RootID    *string `json:"root_id"`
	Directory *string `json:"directory"`
}
type patchInput struct {
	AppID              *int            `json:"appid"`
	CacheSize          *string         `json:"cache_size"`
	MaxParallel        *int            `json:"max_parallel"`
	MaxUploadParallel  *int            `json:"max_upload_parallel"`
	MaxDownloadLoad    *int            `json:"max_download_load"`
	MaxUploadLoad      *int            `json:"max_upload_load"`
	MaxDownloadRate    *string         `json:"max_download_rate"`
	MaxUploadRate      *string         `json:"max_upload_rate"`
	UserAgent          *string         `json:"user_agent"`
	PCSUA              *string         `json:"pcs_ua"`
	PCSAddr            *string         `json:"pcs_addr"`
	PanUA              *string         `json:"pan_ua"`
	SaveDir            *directoryInput `json:"savedir"`
	EnableHTTPS        *bool           `json:"enable_https"`
	FixPCSAddr         *bool           `json:"fix_pcs_addr"`
	ForceLoginUsername *string         `json:"force_login_username"`
	Proxy              *string         `json:"proxy"`
	ProxyHostnames     *[]string       `json:"proxy_hostnames"`
	LocalAddrs         *[]string       `json:"local_addrs"`
	NoCheck            *bool           `json:"no_check"`
	IgnoreIllegal      *bool           `json:"ignore_illegal"`
	UploadPolicy       *string         `json:"upload_policy"`
}

func invalid(message string) error { return fault.New(fault.Invalid, "INVALID_REQUEST", message) }
func decimal(value *string, positive bool) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	text := *value
	if text == "" || len(text) > 19 || len(text) > 1 && text[0] == '0' || positive && text == "0" {
		return nil, invalid("字节数必须是规范十进制整数字符串")
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return nil, invalid("字节数必须是规范十进制整数字符串")
		}
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return nil, invalid("字节数超出int64范围")
	}
	return &parsed, nil
}
func (p patchInput) domain() (configuration.Patch, error) {
	cache, err := decimal(p.CacheSize, true)
	if err != nil {
		return configuration.Patch{}, err
	}
	download, err := decimal(p.MaxDownloadRate, false)
	if err != nil {
		return configuration.Patch{}, err
	}
	upload, err := decimal(p.MaxUploadRate, false)
	if err != nil {
		return configuration.Patch{}, err
	}
	var directory *configuration.Directory
	if p.SaveDir != nil {
		if p.SaveDir.RootID == nil || p.SaveDir.Directory == nil || !configuration.ValidID(*p.SaveDir.RootID) || !configuration.ValidRelative(*p.SaveDir.Directory) {
			return configuration.Patch{}, invalid("savedir必须包含合法root_id和directory")
		}
		directory = &configuration.Directory{RootID: *p.SaveDir.RootID, Directory: *p.SaveDir.Directory}
	}
	if p.AppID != nil && (*p.AppID < 1 || int64(*p.AppID) > 2147483647) {
		return configuration.Patch{}, invalid("appid超出范围")
	}
	for _, number := range []*int{p.MaxParallel, p.MaxUploadParallel, p.MaxDownloadLoad, p.MaxUploadLoad} {
		if number != nil && (*number < 1 || *number > 256) {
			return configuration.Patch{}, invalid("并发数必须为1至256")
		}
	}
	for _, text := range []*string{p.UserAgent, p.PCSUA, p.PanUA} {
		if text != nil && (strings.ContainsAny(*text, "\r\n\x00") || utf8.RuneCountInString(*text) > 4096) {
			return configuration.Patch{}, invalid("请求头文本无效")
		}
	}
	if p.PCSAddr != nil && !pcsHostPattern.MatchString(*p.PCSAddr) {
		return configuration.Patch{}, invalid("PCS主机格式无效")
	}
	if p.ProxyHostnames != nil {
		for _, host := range *p.ProxyHostnames {
			if len(host) > 253 || !proxyRulePattern.MatchString(host) {
				return configuration.Patch{}, invalid("代理主机规则格式无效")
			}
		}
	}
	if p.ForceLoginUsername != nil && utf8.RuneCountInString(*p.ForceLoginUsername) > 256 {
		return configuration.Patch{}, invalid("用户名长度超限")
	}
	if p.Proxy != nil && utf8.RuneCountInString(*p.Proxy) > 8192 {
		return configuration.Patch{}, invalid("代理URL长度超限")
	}
	if p.ProxyHostnames != nil && len(*p.ProxyHostnames) > 100 || p.LocalAddrs != nil && len(*p.LocalAddrs) > 100 {
		return configuration.Patch{}, invalid("网络列表最多100项")
	}
	if p.LocalAddrs != nil {
		for _, addr := range *p.LocalAddrs {
			if len(addr) < 2 || len(addr) > 45 {
				return configuration.Patch{}, invalid("本地地址格式无效")
			}
		}
	}
	if p.UploadPolicy != nil && *p.UploadPolicy != "skip" && *p.UploadPolicy != "overwrite" && *p.UploadPolicy != "rsync" {
		return configuration.Patch{}, invalid("上传策略无效")
	}
	return configuration.Patch{AppID: p.AppID, CacheSize: cache, MaxParallel: p.MaxParallel, MaxUploadParallel: p.MaxUploadParallel, MaxDownloadLoad: p.MaxDownloadLoad, MaxUploadLoad: p.MaxUploadLoad, MaxDownloadRate: download, MaxUploadRate: upload, UserAgent: p.UserAgent, PCSUA: p.PCSUA, PCSAddr: p.PCSAddr, PanUA: p.PanUA, SaveDir: directory, EnableHTTPS: p.EnableHTTPS, FixPCSAddr: p.FixPCSAddr, ForceLoginUsername: p.ForceLoginUsername, Proxy: p.Proxy, ProxyHostnames: p.ProxyHostnames, LocalAddrs: p.LocalAddrs, NoCheck: p.NoCheck, IgnoreIllegal: p.IgnoreIllegal, UploadPolicy: p.UploadPolicy}, nil
}

// StrictJSON rejects unknown fields, duplicate keys, null and multiple values.
// It is also used for service startup files, without ever logging their contents.
func StrictJSON(data []byte, target any) error {
	if !utf8.Valid(data) {
		return invalid("JSON必须使用有效UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var scan func() error
	scan = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if token == nil {
			return errors.New("null is not supported")
		}
		switch delimiter := token.(type) {
		case json.Delim:
			switch delimiter {
			case '{':
				seen := map[string]bool{}
				for decoder.More() {
					key, err := decoder.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || seen[name] {
						return errors.New("duplicate key")
					}
					seen[name] = true
					if err = scan(); err != nil {
						return err
					}
				}
			case '[':
				for decoder.More() {
					if err := scan(); err != nil {
						return err
					}
				}
			default:
				return errors.New("unexpected delimiter")
			}
			_, err = decoder.Token()
			return err
		}
		return nil
	}
	if err := scan(); err != nil {
		return invalid("JSON格式、字段或null值无效")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return invalid("请求只能包含一个JSON对象")
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return invalid("JSON字段或类型无效")
	}
	return nil
}
func decodeRequest(w http.ResponseWriter, r *http.Request, target any) error {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return &httpError{status: 415, code: "UNSUPPORTED_MEDIA_TYPE", message: "需要application/json请求体"}
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			return &httpError{status: 413, code: "LIMIT_EXCEEDED", message: "请求体超出限制"}
		}
		return invalid("无法读取JSON请求")
	}
	if len(bytes.TrimSpace(body)) == 0 || bytes.Equal(bytes.TrimSpace(body), []byte("{}")) {
		return invalid("请求对象不能为空")
	}
	if err := StrictJSON(body, target); err != nil {
		return err
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(body, &properties); err != nil || len(properties) == 0 {
		return invalid("请求对象不能为空")
	}
	return nil
}
