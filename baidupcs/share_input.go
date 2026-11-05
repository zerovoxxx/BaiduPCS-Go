package baidupcs

import (
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var shareInputURL = regexp.MustCompile(`https?://pan\.baidu\.com/(?:s/[A-Za-z0-9_-]+|share/init\?surl=[A-Za-z0-9_-]+)(?:\?pwd=[A-Za-z0-9]{4})?`)
var shareInputPassword = regexp.MustCompile(`(?:提取码|密码|pwd)\s*[:：=]?\s*([A-Za-z0-9]{4})`)
var sharePasswordFormat = regexp.MustCompile(`^[A-Za-z0-9]{4}$`)

// ParseShareInput is shared by CLI and HTTP adapters. Explicit password wins
// over URL pwd, which wins over a code in the surrounding shared text.
func ParseShareInput(input, password string) (string, string, error) {
	match := shareInputURL.FindString(input)
	if match == "" {
		return "", "", errors.New("未找到有效百度分享链接")
	}
	u, err := url.Parse(match)
	if err != nil {
		return "", "", errors.New("分享链接格式无效")
	}
	feature := path.Base(strings.TrimSuffix(u.Path, "/"))
	if feature == "init" {
		feature = "1" + u.Query().Get("surl")
	}
	if password == "" {
		password = u.Query().Get("pwd")
		if password == "" {
			if values := shareInputPassword.FindStringSubmatch(input); len(values) > 1 {
				password = values[1]
			}
		}
	}
	if password != "" && !sharePasswordFormat.MatchString(password) {
		return "", "", errors.New("提取码必须为4位字母或数字")
	}
	if !strings.HasPrefix(feature, "1") || len(feature) > 64 || len(feature) < 2 {
		return "", "", errors.New("分享链接特征无效")
	}
	return feature, password, nil
}
