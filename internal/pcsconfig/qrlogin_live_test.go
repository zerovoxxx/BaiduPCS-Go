package pcsconfig

import (
	"encoding/json"
	"os"
	"testing"
)

// TestLiveSavedQRSession is opt-in and only reads one directory entry. The
// caller supplies an isolated, already authenticated configuration and removes
// it afterwards. It never prints file names, account identifiers or cookies.
func TestLiveSavedQRSession(t *testing.T) {
	path := os.Getenv("BAIDUPCS_QR_LIVE_CONFIG")
	if path == "" {
		t.Skip("set BAIDUPCS_QR_LIVE_CONFIG to an isolated QR login configuration")
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		t.Fatal("isolated configuration is not a regular file")
	}
	c := NewConfig(path)
	defer c.Close()
	if c.Init() != nil {
		t.Fatal("cannot load isolated QR configuration")
	}
	if c.ActiveUser().UID == 0 || len(c.ActiveUser().CookieEntries) == 0 {
		t.Fatal("isolated configuration has no QR account")
	}
	pcs := c.ActiveUserBaiduPCS()
	pcs.SetHTTPS(true)
	pcs.GetClient().SetHTTPSecure(true)
	reader, err := pcs.PrepareFilesDirectoriesList("/", nil, 1, 1)
	if err != nil {
		t.Fatal("reloaded session could not read netdisk directory")
	}
	defer reader.Close()
	var data struct {
		Errno *int              `json:"errno"`
		List  []json.RawMessage `json:"list"`
	}
	if json.NewDecoder(reader).Decode(&data) != nil || data.Errno == nil || *data.Errno != 0 || data.List == nil {
		t.Fatal("reloaded directory request was not authenticated")
	}
}
