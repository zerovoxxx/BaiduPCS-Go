// Package transfer contains the first executable single-file service slice.
// These internal records are not public HTTP DTOs.
package transfer

import (
	"encoding/json"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
)

type Request struct {
	Kind, AccountID, RootID, RelativePath, RemotePath string
	// Options contains canonical request options, not secrets. Public requests
	// resolve omitted values against their one persisted configuration snapshot.
	Options string
	Public  bool
}

type Record struct {
	ID, Owner, State, CreatedAt      string
	Request                          Request
	Config                           configuration.Client
	ConfigRevision                   uint64
	CredentialRevision               uint64
	Checkpoint                       json.RawMessage
	FailureCode                      string
	UpdatedAt, StartedAt, FinishedAt string
	Version                          uint64
	EffectiveOptions                 json.RawMessage
	TargetKey                        string
}

func (r Record) Terminal() bool {
	return r.State == "succeeded" || r.State == "failed" || r.State == "cancelled"
}

func (r Record) Clone() Record {
	r.Config = r.Config.Clone()
	r.Checkpoint = append(json.RawMessage(nil), r.Checkpoint...)
	r.EffectiveOptions = append(json.RawMessage(nil), r.EffectiveOptions...)
	return r
}
