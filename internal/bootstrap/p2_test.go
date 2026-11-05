package bootstrap

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestP2RuntimeAccountsCapabilitiesAndPersistence(t *testing.T) {
	options, _ := testOptions(t)
	runtime, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	created := request(t, runtime.Handler, "POST", "/api/v1/accounts", testToken, "", "account-idem", `{"label":"p2","credentials":{"kind":"bduss","bduss":"P2_NOT_A_REAL_CREDENTIAL"}}`)
	wantStatus(t, created, 201)
	var response struct {
		Data struct {
			AccountID string `json:"account_id"`
			State     string `json:"login_state"`
			Verified  bool   `json:"identity_verified"`
		}
	}
	if err = json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.State != "unverified" || response.Data.Verified {
		t.Fatal("import claimed online verification")
	}
	if strings.Contains(created.Body.String(), "P2_NOT_A_REAL_CREDENTIAL") {
		t.Fatal("credential leaked")
	}
	list := request(t, runtime.Handler, "GET", "/api/v1/accounts", readToken, "", "", "")
	wantStatus(t, list, 200)
	if strings.Contains(list.Body.String(), response.Data.AccountID) {
		t.Fatal("allowlist failed to filter accounts")
	}
	forbidden := request(t, runtime.Handler, "GET", "/api/v1/accounts/"+response.Data.AccountID, readToken, "", "", "")
	wantStatus(t, forbidden, 403)
	capabilities := request(t, runtime.Handler, "GET", "/api/v1/capabilities", testToken, "", "", "")
	wantStatus(t, capabilities, 200)
	var catalog struct {
		Data []struct {
			ID, Implementation, Availability string
			Enabled                          bool
			VerifiedAt                       *string             `json:"verified_at"`
			SupportedValues                  map[string][]string `json:"supported_option_values"`
			Limits                           []struct {
				Name  string
				Value *string
			}
		}
	}
	if err = json.Unmarshal(capabilities.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, op := range catalog.Data {
		counts[op.Implementation]++
		if op.Implementation == "partial" && (!op.Enabled || op.Availability != "unverified" || op.VerifiedAt != nil) {
			t.Fatal("partial implies upstream verification")
		}
		if op.ID == "UPL01" {
			values := map[string]string{}
			for _, limit := range op.Limits {
				if limit.Value != nil {
					values[limit.Name] = *limit.Value
				}
			}
			if values["max_file_size"] != "33554432" || values["max_files_per_request"] != "1" || values["parallel_per_file"] != "1" || len(op.SupportedValues["skip_rapid_check"]) != 1 || op.SupportedValues["skip_rapid_check"][0] != "true" {
				t.Fatal("missing fixed value constraints")
			}
		}
	}
	if counts["implemented"] != 40 || counts["partial"] != 3 || counts["planned"] != 62 {
		t.Fatalf("wrong implementation counts: %v", counts)
	}
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got := request(t, reopened.Handler, "GET", "/api/v1/accounts/"+response.Data.AccountID, testToken, "", "", "")
	wantStatus(t, got, 200)
	replay := request(t, reopened.Handler, "POST", "/api/v1/accounts", testToken, "", "account-idem", `{"label":"p2","credentials":{"kind":"bduss","bduss":"P2_NOT_A_REAL_CREDENTIAL"}}`)
	wantStatus(t, replay, 201)
	if !strings.Contains(replay.Body.String(), response.Data.AccountID) || !strings.Contains(replay.Body.String(), `"bduss":true`) {
		t.Fatal("account replay failed after restart")
	}
}
