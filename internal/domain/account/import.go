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
	ID, Label, CreatedAt  string
	Credentials           Credentials `json:"-"`
	UpdatedAt, LoginState string
	Revision              uint64
	Identity              Identity
	Present               map[string]bool
	// Public metadata survives idempotent replay without serializing secrets.
	CredentialVersion uint64
}

type Quota struct {
	Total, Used, Free uint64
	CollectedAt       string
}

// Patch is presence aware: a missing key preserves a secret, nil revokes it.
type Patch map[string]*string

func Update(c Credentials, patch Patch) (Credentials, bool, error) {
	c = c.Clone()
	if len(patch) == 0 {
		return c, false, fault.New(fault.Invalid, "INVALID_CREDENTIAL_FORMAT", "凭据更新不能为空")
	}
	invalid := func() (Credentials, bool, error) {
		return Credentials{}, false, fault.New(fault.Invalid, "INVALID_CREDENTIAL_FORMAT", "凭据更新格式无效")
	}
	_, jar := patch["cookies"]
	revoked := false
	for k, v := range patch {
		if k != "cookies" && k != "access_token" && k != "bduss" && k != "ptoken" && k != "stoken" && k != "sboxtkn" && k != "baiduid" {
			return invalid()
		}
		if jar && k != "cookies" && k != "access_token" {
			return invalid()
		}
		if v != nil && (*v == "" || strings.ContainsAny(*v, "\r\n\x00")) {
			return invalid()
		}
		if v == nil {
			revoked = true
		}
		if k == "access_token" {
			c.AccessToken = ""
			if v != nil {
				if len(*v) > 8192 {
					return invalid()
				}
				c.AccessToken = *v
			}
		} else if k == "cookies" {
			c.Cookies = map[string]string{}
			if v != nil {
				n, err := Normalize(Import{Kind: "cookies", Cookies: *v})
				if err != nil {
					return invalid()
				}
				c.Cookies = n.Cookies
			}
		} else {
			name := strings.ToUpper(k)
			delete(c.Cookies, name)
			if v != nil {
				if len(*v) > 8192 || (&http.Cookie{Name: name, Value: *v}).Valid() != nil {
					return invalid()
				}
				c.Cookies[name] = *v
			}
		}
	}
	// Clearing BDUSS also removes the dependent cookie session. It cannot leave
	// a usable jar with a missing primary credential.
	if v, ok := patch["bduss"]; ok && v == nil {
		c.Cookies = map[string]string{}
	}
	if len(c.Cookies) > 0 && c.Cookies["BDUSS"] == "" {
		return invalid()
	}
	c.Revision++
	return c, revoked, nil
}

// Identity deliberately distinguishes a Baidu UID from the netdisk UK. Unknown
// metadata is nullable; importing a credential does not verify an identity.
type Identity struct {
	UID, UK       *string
	Username, Sex *string
	Age           *float64
	Verified      bool
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
		if input.Cookies == "" || len(input.Cookies) > 32768 || strings.ContainsAny(input.Cookies, "\r\n") || input.BDUSS != "" || input.PToken != "" || input.SToken != "" || input.SBoxTKN != "" || input.BAIDUID != "" || input.AccessToken != "" {
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
