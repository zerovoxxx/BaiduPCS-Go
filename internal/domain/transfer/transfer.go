// Package transfer contains the first executable single-file service slice.
// These internal records are not public HTTP DTOs.
package transfer

import (
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/tool"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
)

type Request struct {
	Kind, AccountID, RootID, RelativePath, RemotePath string
	// Options contains canonical request options, not secrets. Public requests
	// resolve omitted values against their one persisted configuration snapshot.
	Options       string
	SemanticInput string `json:",omitempty"`
	WritePlan     string `json:",omitempty"`
	Reconcile     bool   `json:",omitempty"`
	ReconcileDry  bool   `json:",omitempty"`
	Batch         string `json:",omitempty"`
	ScanPlan      string `json:",omitempty"`
	ToolPlan      string `json:",omitempty"`
	ParentJobID   string `json:",omitempty"`
	RetryOf       string `json:",omitempty"`
	RetryScope    string `json:",omitempty"`
	Public        bool
}

type Record struct {
	ID, Owner, State, CreatedAt      string
	Request                          Request
	Config                           configuration.Client
	ConfigRevision                   uint64
	CredentialRevision               uint64
	Checkpoint                       json.RawMessage
	Plan                             json.RawMessage `json:",omitempty"`
	FailureCode                      string
	UpdatedAt, StartedAt, FinishedAt string
	Version                          uint64
	EffectiveOptions                 json.RawMessage
	TargetKey                        string
	ScanResult                       *ScanResult
	Admission                        operation.Mutation `json:"-"`
}

func (r Record) Terminal() bool {
	return Terminal(r.State)
}

func (r Record) Clone() Record {
	r.Config = r.Config.Clone()
	if r.ScanResult != nil {
		cp := *r.ScanResult
		cp.ArtifactIDs = append([]string{}, cp.ArtifactIDs...)
		cp.ChildJobIDs = append([]string{}, cp.ChildJobIDs...)
		cp.MutationItems = append([]Item{}, cp.MutationItems...)
		cp.IntegrityItems = append([]tool.ResultItem{}, cp.IntegrityItems...)
		cp.MissingFields = append([]string{}, cp.MissingFields...)
		r.ScanResult = &cp
	}
	r.Plan = append(json.RawMessage(nil), r.Plan...)
	r.Checkpoint = append(json.RawMessage(nil), r.Checkpoint...)
	r.EffectiveOptions = append(json.RawMessage(nil), r.EffectiveOptions...)
	return r
}
