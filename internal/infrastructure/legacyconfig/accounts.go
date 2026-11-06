// Package legacyconfig reads an explicitly selected legacy snapshot without
// initializing CLI globals, changing its active account or rewriting its file.
package legacyconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"io"
	"os"
	"strconv"
	"strings"
)

type Account struct {
	Label, Username, Key string
	Credentials          account.Credentials
}

func ReadAccounts(filename string) ([]Account, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, errors.New("legacy accounts file cannot be opened")
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > 1<<20 {
		return nil, errors.New("legacy accounts file must be regular and within 1MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("legacy accounts file cannot be read")
	}
	after, err := f.Stat()
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("legacy accounts file changed during import")
	}
	var input struct {
		Users []struct {
			Name, BDUSS, PTOKEN, STOKEN, SBOXTKN, BAIDUID, COOKIES string
			AccessToken                                            string `json:"accesstoken"`
		} `json:"baidu_user_list"`
	}
	if json.Unmarshal(data, &input) != nil || len(input.Users) > 1000 {
		return nil, errors.New("invalid legacy accounts snapshot")
	}
	out := make([]Account, 0, len(input.Users))
	for i, v := range input.Users {
		var c account.Credentials
		if v.COOKIES != "" {
			c, err = account.Normalize(account.Import{Kind: "cookies", Cookies: v.COOKIES})
			if err != nil {
				return nil, err
			}
			for name, value := range map[string]string{"BDUSS": v.BDUSS, "PTOKEN": v.PTOKEN, "STOKEN": v.STOKEN, "SBOXTKN": v.SBOXTKN, "BAIDUID": v.BAIDUID} {
				if value != "" && c.Cookies[name] != "" && value != c.Cookies[name] {
					return nil, errors.New("conflicting legacy credentials")
				}
				if value != "" {
					c.Cookies[name] = value
				}
			}
			// Reuse the canonical parser for merged Cookie fields as well.
			fields := []string{}
			for name, value := range c.Cookies {
				fields = append(fields, name+"="+value)
			}
			c, err = account.Normalize(account.Import{Kind: "cookies", Cookies: strings.Join(fields, ";")})
		} else if v.BDUSS != "" {
			c, err = account.Normalize(account.Import{Kind: "bduss", BDUSS: v.BDUSS, PToken: v.PTOKEN, SToken: v.STOKEN, SBoxTKN: v.SBOXTKN, BAIDUID: v.BAIDUID})
		} else {
			c, err = account.Normalize(account.Import{Kind: "access_token", AccessToken: v.AccessToken})
		}
		if err != nil {
			return nil, err
		}
		if v.AccessToken != "" {
			if _, err = account.Normalize(account.Import{Kind: "access_token", AccessToken: v.AccessToken}); err != nil {
				return nil, err
			}
			c.AccessToken = v.AccessToken
		}
		// UID is deliberately absent. The historical force-login fallback uses 24;
		// neither it nor a legacy display name establishes a verified Baidu identity.
		canonical, _ := json.Marshal(struct {
			Index   int
			Cookies map[string]string
			Token   string
		}{i, c.Cookies, c.AccessToken})
		sum := sha256.Sum256(canonical)
		out = append(out, Account{Label: "legacy:" + strconv.Itoa(i), Username: v.Name, Key: "legacy-" + hex.EncodeToString(sum[:]), Credentials: c})
	}
	return out, nil
}
