package account

import (
	"net/http"
	"strings"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
)

// Import is an input, never an account response or job parameter.
type Import struct {
	Kind, BDUSS, PToken, SToken, SBoxTKN, BAIDUID, Cookies, AccessToken string
}

type Entry struct {
	ID, Label, CreatedAt string
	Credentials          Credentials `json:"-"`
}

func Normalize(input Import) (Credentials, error) {
	c := Credentials{Revision: 1, Cookies: map[string]string{}}
	invalid := func() (Credentials, error) {
		return Credentials{}, fault.New(fault.Invalid, "INVALID_CREDENTIAL_FORMAT", "凭据类型或Cookie格式无效")
	}
	switch input.Kind {
	case "bduss":
		if input.BDUSS == "" || input.Cookies != "" || input.AccessToken != "" {
			return invalid()
		}
		for name, value := range map[string]string{"BDUSS": input.BDUSS, "PTOKEN": input.PToken, "STOKEN": input.SToken, "SBOXTKN": input.SBoxTKN, "BAIDUID": input.BAIDUID} {
			if value != "" {
				c.Cookies[name] = value
			}
		}
	case "cookies":
		if input.Cookies == "" || len(input.Cookies) > 16384 || strings.ContainsAny(input.Cookies, "\r\n") || input.BDUSS != "" || input.PToken != "" || input.SToken != "" || input.SBoxTKN != "" || input.BAIDUID != "" || input.AccessToken != "" {
			return invalid()
		}
		for _, field := range strings.Split(input.Cookies, ";") {
			if strings.TrimSpace(field) == "" {
				continue
			}
			name, value, ok := strings.Cut(strings.TrimSpace(field), "=")
			if _, duplicate := c.Cookies[name]; !ok || duplicate || name == "" || value == "" {
				return invalid()
			}
			c.Cookies[name] = value
		}
		if c.Cookies["BDUSS"] == "" || len(c.Cookies) > 128 {
			return invalid()
		}
	case "access_token":
		if input.AccessToken == "" || len(input.AccessToken) > 8192 || strings.ContainsAny(input.AccessToken, "\r\n\x00") || input.BDUSS != "" || input.Cookies != "" || input.PToken != "" || input.SToken != "" || input.SBoxTKN != "" || input.BAIDUID != "" {
			return invalid()
		}
		c.AccessToken = input.AccessToken
	default:
		return invalid()
	}
	for name, value := range c.Cookies {
		if len(value) > 8192 || (&http.Cookie{Name: name, Value: value}).Valid() != nil {
			return invalid()
		}
	}
	return c, nil
}
