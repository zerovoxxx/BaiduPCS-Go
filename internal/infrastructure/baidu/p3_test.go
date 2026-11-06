package baidu

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"testing"
)

func TestP3PreservesP2CheckpointBinding(t *testing.T) {
	// This is the exact public P2 Request encoding at baseline 4fc00ce.
	type legacyRequest struct {
		Kind, AccountID, RootID, RelativePath, RemotePath string
		Options                                           string
		Public                                            bool
	}
	original := legacyRequest{Kind: "upload", AccountID: "acct_p2", RootID: "root", RelativePath: "source", RemotePath: "/target", Options: "{}", Public: true}
	raw, _ := json.Marshal([]any{"job_p2", original, uint64(3), uint64(7)})
	sum := sha256.Sum256(raw)
	r := transfer.Record{ID: "job_p2", ConfigRevision: 3, CredentialRevision: 7, Request: transfer.Request{Kind: original.Kind, AccountID: original.AccountID, RootID: original.RootID, RelativePath: original.RelativePath, RemotePath: original.RemotePath, Options: original.Options, Public: true}}
	if binding(r) != hex.EncodeToString(sum[:]) {
		t.Fatal("empty P3 fields changed a P2 checkpoint binding")
	}
}
