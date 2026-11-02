package pcsconfig

import (
	"errors"
	"net/url"
	"strings"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcslogin"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

// SaveQRSession commits a verified session before changing the active in-memory
// account. Failure leaves both the previous accounts and the old file intact.
func (c *PCSConfig) SaveQRSession(session *pcslogin.Session) (*Baidu, error) {
	if session == nil || session.UID == 0 || strings.TrimSpace(session.Name) == "" || len(session.Cookies) == 0 {
		return nil, errors.New("扫码账号信息不完整")
	}
	jar, err := requester.NewCookieJar(session.Cookies)
	if err != nil {
		return nil, errors.New("扫码 Cookie 属性无效")
	}
	account := &Baidu{Workdir: "/"}
	users := make(BaiduUserList, 0, len(c.BaiduUserList)+1)
	for _, old := range c.BaiduUserList {
		if old != nil && old.UID == session.UID {
			*account = *old // Keep workdir, profile and optional accessToken.
		} else {
			users = append(users, old)
		}
	}
	account.BaiduBase = BaiduBase{UID: session.UID, Name: session.Name}
	if account.Workdir == "" {
		account.Workdir = "/"
	}
	setAccountCookies(account, jar)
	if account.BDUSS == "" || account.STOKEN == "" || account.BAIDUID == "" {
		return nil, pcslogin.ErrCredentials
	}
	users = append(users, account)
	previousUsers, previousUID := c.BaiduUserList, c.BaiduActiveUID
	c.BaiduUserList, c.BaiduActiveUID = users, account.UID
	if err := c.Save(); err != nil {
		c.BaiduUserList, c.BaiduActiveUID = previousUsers, previousUID
		return nil, errors.New("保存扫码登录配置失败，原有账号未替换")
	}
	c.setupNewUser(account)
	return account, nil
}

// Persist cookie refreshes and deletions without reviving the compatibility
// strings. The pointer check excludes an old session during QR replacement.
func (c *PCSConfig) syncActiveCookies() {
	if c.activeUser == nil || c.pcs == nil || c.activeUser.CookieEntries == nil || c.activeUser.UID != c.BaiduActiveUID {
		return
	}
	found := false
	for _, account := range c.BaiduUserList {
		if account == c.activeUser {
			found = true
			break
		}
	}
	if !found {
		return
	}
	jar, ok := c.pcs.GetClient().Jar.(*requester.CookieJar)
	if !ok {
		return
	}
	setAccountCookies(c.activeUser, jar)
}

func setAccountCookies(account *Baidu, jar *requester.CookieJar) {
	account.CookieEntries = jar.Entries() // Keep non-nil [] when every cookie expired.
	pan, _ := url.Parse("https://pan.baidu.com/")
	passport, _ := url.Parse("https://passport.baidu.com/")
	values := make(map[string]string)
	var header []string
	for _, cookie := range jar.Cookies(pan) {
		if _, ok := values[cookie.Name]; !ok {
			values[cookie.Name] = cookie.Value
		}
		header = append(header, cookie.String())
	}
	account.BDUSS, account.STOKEN, account.BAIDUID = values["BDUSS"], values["STOKEN"], values["BAIDUID"]
	account.SBOXTKN, account.PTOKEN = values["SBOXTKN"], ""
	for _, cookie := range jar.Cookies(passport) {
		if cookie.Name == "PTOKEN" {
			account.PTOKEN = cookie.Value
			break
		}
	}
	account.COOKIES = strings.Join(header, "; ")
}
