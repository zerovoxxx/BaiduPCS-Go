package account

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCredentialImportCanonicalization(t *testing.T) {
	valid := []Import{{Kind: "bduss", BDUSS: "a", SToken: "b"}, {Kind: "cookies", Cookies: "BDUSS=a; STOKEN=b"}, {Kind: "access_token", AccessToken: "token-only"}}
	for _, input := range valid {
		credentials, err := Normalize(input)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(Entry{ID: "account", Credentials: credentials})
		if strings.Contains(string(encoded), "BDUSS") || strings.Contains(string(encoded), "AccessToken") {
			t.Fatal("account response exposes credentials")
		}
		if input.Kind == "cookies" && credentials.Cookies["STOKEN"] != "b" {
			t.Fatal("final cookie without semicolon was lost")
		}
	}
	invalid := []Import{{Kind: "cookies", Cookies: "STOKEN=b"}, {Kind: "cookies", Cookies: "BDUSS=a; BDUSS=b"}, {Kind: "cookies", Cookies: "BDUSS=a\r\nSTOKEN=b"}, {Kind: "cookies", Cookies: "BDUSS=a; broken"}, {Kind: "bduss", BDUSS: "a", AccessToken: "mixed"}, {Kind: "access_token", AccessToken: "token", SToken: "mixed"}}
	for _, input := range invalid {
		if _, err := Normalize(input); err == nil {
			t.Fatal("invalid or ambiguous credentials accepted")
		}
	}
}
