package account

import "testing"

func str(v string) *string { return &v }
func TestP3CanonicalCredentialPatchPresenceAndRevocation(t *testing.T) {
	c, err := Normalize(Import{Kind: "cookies", Cookies: "BDUSS=one;STOKEN=old;PTOKEN=old-p"})
	if err != nil {
		t.Fatal(err)
	}
	c.AccessToken = "independent"
	changed, revoked, err := Update(c, Patch{"stoken": str("new")})
	if err != nil || revoked || changed.Cookies["BDUSS"] != "one" || changed.Cookies["STOKEN"] != "new" || c.Cookies["STOKEN"] != "old" {
		t.Fatal("patch mutated shared jar or lost missing fields", err)
	}
	changed, revoked, err = Update(changed, Patch{"stoken": nil})
	if err != nil || !revoked || changed.Cookies["STOKEN"] != "" || changed.Cookies["PTOKEN"] != "old-p" {
		t.Fatal("single clear did not update canonical jar", err)
	}
	changed, _, err = Update(changed, Patch{"cookies": str("BDUSS=replaced;BAIDUID=id")})
	if err != nil || changed.Cookies["PTOKEN"] != "" || changed.Cookies["STOKEN"] != "" || changed.AccessToken != "independent" {
		t.Fatal("replacement resurrected a cleared cookie", err)
	}
	changed, revoked, err = Update(changed, Patch{"cookies": nil})
	if err != nil || !revoked || len(changed.Cookies) != 0 || changed.AccessToken != "independent" {
		t.Fatal("jar clear affected unrelated token", err)
	}
	changed, revoked, err = Update(changed, Patch{"access_token": nil})
	if err != nil || !revoked || changed.AccessToken != "" {
		t.Fatal(err)
	}
	for _, patch := range []Patch{{}, {"cookies": str("STOKEN=missing-primary")}, {"cookies": nil, "bduss": str("mixed")}, {"bduss": str("")}, {"access_token": str("line\nbreak")}, {"unknown": nil}} {
		if _, _, err = Update(c, patch); err == nil {
			t.Fatal("invalid patch accepted")
		}
	}
}
