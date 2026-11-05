package legacyconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestP3LegacyImportRejectsConflictingCookieCopies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.json")
	for _, body := range []string{`{"baidu_user_list":[{"bduss":"one","cookies":"BDUSS=two"}]}`, `{"baidu_user_list":[{"cookies":"STOKEN=missing-primary"}]}`, `{"baidu_user_list":[{"accesstoken":"bad\ntoken"}]}`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadAccounts(path); err == nil {
			t.Fatal("conflicting or invalid legacy credential accepted")
		}
	}
}
