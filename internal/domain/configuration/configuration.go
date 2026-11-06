package configuration

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
)

type Directory struct {
	RootID    string `json:"root_id"`
	Directory string `json:"directory"`
}

type Client struct {
	AppID              int        `json:"appid"`
	CacheSize          int64      `json:"cache_size"`
	MaxParallel        int        `json:"max_parallel"`
	MaxUploadParallel  int        `json:"max_upload_parallel"`
	MaxDownloadLoad    int        `json:"max_download_load"`
	MaxUploadLoad      int        `json:"max_upload_load"`
	MaxDownloadRate    int64      `json:"max_download_rate"`
	MaxUploadRate      int64      `json:"max_upload_rate"`
	UserAgent          string     `json:"user_agent"`
	PCSUA              string     `json:"pcs_ua"`
	PCSAddr            string     `json:"pcs_addr"`
	PanUA              string     `json:"pan_ua"`
	SaveDir            *Directory `json:"savedir"`
	EnableHTTPS        bool       `json:"enable_https"`
	FixPCSAddr         bool       `json:"fix_pcs_addr"`
	ForceLoginUsername string     `json:"force_login_username"`
	Proxy              string     `json:"proxy"`
	ProxyHostnames     []string   `json:"proxy_hostnames"`
	LocalAddrs         []string   `json:"local_addrs"`
	NoCheck            bool       `json:"no_check"`
	IgnoreIllegal      bool       `json:"ignore_illegal"`
	UploadPolicy       string     `json:"upload_policy"`
}

func (c Client) Clone() Client {
	c.ProxyHostnames = append([]string{}, c.ProxyHostnames...)
	c.LocalAddrs = append([]string{}, c.LocalAddrs...)
	if c.SaveDir != nil {
		d := *c.SaveDir
		c.SaveDir = &d
	}
	return c
}

// Snapshot owns its fields; callers only receive detached copies.
type Snapshot struct {
	revision uint64
	client   Client
}

func NewSnapshot(revision uint64, client Client) Snapshot {
	return Snapshot{revision: revision, client: client.Clone()}
}
func (s Snapshot) Revision() uint64 { return s.revision }
func (s Snapshot) Client() Client   { return s.client.Clone() }

type Patch struct {
	AppID              *int       `json:"appid,omitempty"`
	CacheSize          *int64     `json:"cache_size,omitempty"`
	MaxParallel        *int       `json:"max_parallel,omitempty"`
	MaxUploadParallel  *int       `json:"max_upload_parallel,omitempty"`
	MaxDownloadLoad    *int       `json:"max_download_load,omitempty"`
	MaxUploadLoad      *int       `json:"max_upload_load,omitempty"`
	MaxDownloadRate    *int64     `json:"max_download_rate,omitempty"`
	MaxUploadRate      *int64     `json:"max_upload_rate,omitempty"`
	UserAgent          *string    `json:"user_agent,omitempty"`
	PCSUA              *string    `json:"pcs_ua,omitempty"`
	PCSAddr            *string    `json:"pcs_addr,omitempty"`
	PanUA              *string    `json:"pan_ua,omitempty"`
	SaveDir            *Directory `json:"savedir,omitempty"`
	EnableHTTPS        *bool      `json:"enable_https,omitempty"`
	FixPCSAddr         *bool      `json:"fix_pcs_addr,omitempty"`
	ForceLoginUsername *string    `json:"force_login_username,omitempty"`
	Proxy              *string    `json:"proxy,omitempty"`
	ProxyHostnames     *[]string  `json:"proxy_hostnames,omitempty"`
	LocalAddrs         *[]string  `json:"local_addrs,omitempty"`
	NoCheck            *bool      `json:"no_check,omitempty"`
	IgnoreIllegal      *bool      `json:"ignore_illegal,omitempty"`
	UploadPolicy       *string    `json:"upload_policy,omitempty"`
}

var Fields = []string{"appid", "cache_size", "max_parallel", "max_upload_parallel", "max_download_load", "max_upload_load", "max_download_rate", "max_upload_rate", "user_agent", "pcs_ua", "pcs_addr", "pan_ua", "savedir", "enable_https", "fix_pcs_addr", "force_login_username", "proxy", "proxy_hostnames", "local_addrs", "no_check", "ignore_illegal", "upload_policy"}

func (p Patch) Apply(c Client) (Client, []string) {
	c = c.Clone()
	names := []string{}
	set := func(name string, present bool, apply func()) {
		if present {
			apply()
			names = append(names, name)
		}
	}
	set("appid", p.AppID != nil, func() { c.AppID = *p.AppID })
	set("cache_size", p.CacheSize != nil, func() { c.CacheSize = *p.CacheSize })
	set("max_parallel", p.MaxParallel != nil, func() { c.MaxParallel = *p.MaxParallel })
	set("max_upload_parallel", p.MaxUploadParallel != nil, func() { c.MaxUploadParallel = *p.MaxUploadParallel })
	set("max_download_load", p.MaxDownloadLoad != nil, func() { c.MaxDownloadLoad = *p.MaxDownloadLoad })
	set("max_upload_load", p.MaxUploadLoad != nil, func() { c.MaxUploadLoad = *p.MaxUploadLoad })
	set("max_download_rate", p.MaxDownloadRate != nil, func() { c.MaxDownloadRate = *p.MaxDownloadRate })
	set("max_upload_rate", p.MaxUploadRate != nil, func() { c.MaxUploadRate = *p.MaxUploadRate })
	set("user_agent", p.UserAgent != nil, func() { c.UserAgent = *p.UserAgent })
	set("pcs_ua", p.PCSUA != nil, func() { c.PCSUA = *p.PCSUA })
	set("pcs_addr", p.PCSAddr != nil, func() { c.PCSAddr = *p.PCSAddr })
	set("pan_ua", p.PanUA != nil, func() { c.PanUA = *p.PanUA })
	set("savedir", p.SaveDir != nil, func() { d := *p.SaveDir; c.SaveDir = &d })
	set("enable_https", p.EnableHTTPS != nil, func() { c.EnableHTTPS = *p.EnableHTTPS })
	set("fix_pcs_addr", p.FixPCSAddr != nil, func() { c.FixPCSAddr = *p.FixPCSAddr })
	set("force_login_username", p.ForceLoginUsername != nil, func() { c.ForceLoginUsername = *p.ForceLoginUsername })
	set("proxy", p.Proxy != nil, func() { c.Proxy = *p.Proxy })
	set("proxy_hostnames", p.ProxyHostnames != nil, func() { c.ProxyHostnames = append([]string{}, (*p.ProxyHostnames)...) })
	set("local_addrs", p.LocalAddrs != nil, func() { c.LocalAddrs = append([]string{}, (*p.LocalAddrs)...) })
	set("no_check", p.NoCheck != nil, func() { c.NoCheck = *p.NoCheck })
	set("ignore_illegal", p.IgnoreIllegal != nil, func() { c.IgnoreIllegal = *p.IgnoreIllegal })
	set("upload_policy", p.UploadPolicy != nil, func() { c.UploadPolicy = *p.UploadPolicy })
	sort.Strings(names)
	return c, names
}

func Reset(c, defaults Client, fields []string) (Client, []string, error) {
	if len(fields) == 0 || len(fields) > len(Fields) {
		return Client{}, nil, fault.New(fault.Invalid, "INVALID_REQUEST", "fields 必须包含1至22个字段")
	}
	seen := map[string]bool{}
	p := Patch{}
	for _, field := range fields {
		if seen[field] {
			return Client{}, nil, fault.New(fault.Invalid, "INVALID_REQUEST", "fields 不能重复")
		}
		seen[field] = true
		switch field {
		case "appid":
			p.AppID = &defaults.AppID
		case "cache_size":
			p.CacheSize = &defaults.CacheSize
		case "max_parallel":
			p.MaxParallel = &defaults.MaxParallel
		case "max_upload_parallel":
			p.MaxUploadParallel = &defaults.MaxUploadParallel
		case "max_download_load":
			p.MaxDownloadLoad = &defaults.MaxDownloadLoad
		case "max_upload_load":
			p.MaxUploadLoad = &defaults.MaxUploadLoad
		case "max_download_rate":
			p.MaxDownloadRate = &defaults.MaxDownloadRate
		case "max_upload_rate":
			p.MaxUploadRate = &defaults.MaxUploadRate
		case "user_agent":
			p.UserAgent = &defaults.UserAgent
		case "pcs_ua":
			p.PCSUA = &defaults.PCSUA
		case "pcs_addr":
			p.PCSAddr = &defaults.PCSAddr
		case "pan_ua":
			p.PanUA = &defaults.PanUA
		case "savedir":
			c = c.Clone()
			c.SaveDir = nil
			if defaults.SaveDir != nil {
				d := *defaults.SaveDir
				c.SaveDir = &d
			}
		case "enable_https":
			p.EnableHTTPS = &defaults.EnableHTTPS
		case "fix_pcs_addr":
			p.FixPCSAddr = &defaults.FixPCSAddr
		case "force_login_username":
			p.ForceLoginUsername = &defaults.ForceLoginUsername
		case "proxy":
			p.Proxy = &defaults.Proxy
		case "proxy_hostnames":
			p.ProxyHostnames = &defaults.ProxyHostnames
		case "local_addrs":
			p.LocalAddrs = &defaults.LocalAddrs
		case "no_check":
			p.NoCheck = &defaults.NoCheck
		case "ignore_illegal":
			p.IgnoreIllegal = &defaults.IgnoreIllegal
		case "upload_policy":
			p.UploadPolicy = &defaults.UploadPolicy
		default:
			return Client{}, nil, fault.New(fault.Invalid, "INVALID_REQUEST", "未知的配置字段")
		}
	}
	result, _ := p.Apply(c)
	names := append([]string(nil), fields...)
	sort.Strings(names)
	return result, names, nil
}

var hostPattern = regexp.MustCompile(`^([cd][0-9]?\.)?pcs\.baidu\.com$`)
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func ValidID(id string) bool { return len(id) <= 128 && idPattern.MatchString(id) }
func ValidRelative(p string) bool {
	if strings.ContainsAny(p, "\\:\x00") || strings.HasPrefix(p, "/") || utf8.RuneCountInString(p) > 4096 {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// Validate returns safe field errors, never the supplied proxy/secret values.
func Validate(c Client) []fault.Issue {
	issues := []fault.Issue{}
	issue := func(field, msg string) {
		issues = append(issues, fault.Issue{Pointer: "/" + field, Code: "INVALID_CONFIGURATION", Message: msg})
	}
	if c.AppID < 1 || int64(c.AppID) > 2147483647 {
		issue("appid", "必须是1至2147483647的整数")
	}
	if c.CacheSize < 1024 || uint64(c.CacheSize) > uint64(^uint(0)>>1) {
		issue("cache_size", "必须至少1024字节且能用平台int表示")
	}
	for _, x := range []struct {
		name string
		v    int
	}{{"max_parallel", c.MaxParallel}, {"max_upload_parallel", c.MaxUploadParallel}, {"max_download_load", c.MaxDownloadLoad}, {"max_upload_load", c.MaxUploadLoad}} {
		if x.v < 1 || x.v > 256 {
			issue(x.name, "必须是1至256的整数")
		}
	}
	if c.MaxDownloadRate < 0 {
		issue("max_download_rate", "速率不能为负数")
	}
	if c.MaxUploadRate < 0 {
		issue("max_upload_rate", "速率不能为负数")
	}
	for _, x := range []struct{ name, value string }{{"user_agent", c.UserAgent}, {"pcs_ua", c.PCSUA}, {"pan_ua", c.PanUA}} {
		if strings.ContainsAny(x.value, "\r\n\x00") || utf8.RuneCountInString(x.value) > 4096 {
			issue(x.name, "请求头文本无效")
		}
	}
	if !hostPattern.MatchString(c.PCSAddr) {
		issue("pcs_addr", "不在允许的PCS主机集合内")
	}
	if utf8.RuneCountInString(c.ForceLoginUsername) > 256 {
		issue("force_login_username", "不能超过256个字符")
	}
	if c.UploadPolicy != "skip" && c.UploadPolicy != "overwrite" && c.UploadPolicy != "rsync" {
		issue("upload_policy", "必须为skip、overwrite或rsync")
	}
	if c.SaveDir != nil && (!ValidID(c.SaveDir.RootID) || !ValidRelative(c.SaveDir.Directory)) {
		issue("savedir", "必须是有效的本地根目录和相对目录")
	}
	if c.Proxy != "" {
		u, err := url.Parse(c.Proxy)
		if err != nil || u.Hostname() == "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || len(c.Proxy) > 8192 || !(u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "socks5" || u.Scheme == "socks5h") {
			issue("proxy", "必须是有效的http、https或socks5代理URL")
		} else if u.Port() != "" {
			if port, e := strconv.Atoi(u.Port()); e != nil || port < 1 || port > 65535 {
				issue("proxy", "代理端口无效")
			}
		}
	}
	if len(c.ProxyHostnames) > 100 {
		issue("proxy_hostnames", "最多100条规则")
	}
	for _, rule := range c.ProxyHostnames {
		host := strings.TrimPrefix(rule, "*.")
		if !validHostname(host) || strings.Contains(strings.TrimPrefix(rule, "*."), "*") {
			issue("proxy_hostnames", "主机规则必须是精确域名或*.domain")
			break
		}
	}
	if len(c.LocalAddrs) > 100 {
		issue("local_addrs", "最多100个本地IP")
	}
	for _, addr := range c.LocalAddrs {
		if net.ParseIP(addr) == nil {
			issue("local_addrs", "必须是有效的本地IP")
			break
		}
	}
	return issues
}
func validHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
func Normalize(c Client) Client {
	c = c.Clone()
	for i, h := range c.ProxyHostnames {
		c.ProxyHostnames[i] = strings.ToLower(h)
	}
	for i, addr := range c.LocalAddrs {
		if ip := net.ParseIP(addr); ip != nil {
			c.LocalAddrs[i] = ip.String()
		}
	}
	if c.SaveDir != nil && ValidRelative(c.SaveDir.Directory) {
		c.SaveDir.Directory = path.Clean(c.SaveDir.Directory)
		if c.SaveDir.Directory == "." {
			c.SaveDir.Directory = ""
		}
	}
	return c
}

type Mutation struct {
	PrincipalID string
	Operation   string
	KeyHash     [32]byte
	RequestHash [32]byte
	HasKey      bool
	Fields      []string
}

func NewMutation(principalID, operation, key string, revision uint64, canonical []byte) Mutation {
	return Mutation{PrincipalID: principalID, Operation: operation, HasKey: key != "", KeyHash: sha256.Sum256([]byte(key)), RequestHash: sha256.Sum256(append([]byte(fmt.Sprintf("%s\n%d\n", operation, revision)), canonical...))}
}

type Change struct {
	Snapshot Snapshot
	Fields   []string
}
