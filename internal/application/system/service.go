package system

import (
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/system"
	"sort"
	"time"
)

type Service struct {
	version, baseline string
	started           time.Time
	operations        []system.Operation
	now               func() time.Time
}

func New(version, baseline string, started time.Time, operations []system.Operation) *Service {
	copied := append([]system.Operation{}, operations...)
	for i := range copied {
		copied[i].Scopes = append([]string(nil), copied[i].Scopes...)
	}
	sort.Slice(copied, func(i, j int) bool { return copied[i].ID < copied[j].ID })
	return &Service{version: version, baseline: baseline, started: started, operations: copied, now: time.Now}
}
func (s *Service) Info() system.Info {
	return system.Info{APIVersion: "v1", ProgramVersion: s.version, SourceBaseline: s.baseline, StartedAt: s.started.UTC().Format(time.RFC3339Nano), Now: s.now().UTC().Format(time.RFC3339Nano)}
}
func (s *Service) Capabilities() []system.Capability {
	result := make([]system.Capability, 0, len(s.operations))
	for _, op := range s.operations {
		reason := "此操作尚未实现"
		availability := "unverified"
		implementation := "planned"
		if op.Implemented {
			implementation = "implemented"
			availability = "available"
			reason = "本地HTTP能力已实现，离线契约验收见P1报告"
		}
		if op.ID == "INT06" {
			availability = "upstream_unavailable"
			reason = "旧式秒传上游能力不可用"
		}
		options := []string{}
		if op.ID == "CFG02" || op.ID == "CFG04" {
			options = append(options, configuration.Fields...)
		} else if op.ID == "CFG05" {
			options = []string{"fields"}
		}
		limits := append([]system.Limit{}, op.Limits...)
		result = append(result, system.Capability{ID: op.ID, Implementation: implementation, Availability: availability, Enabled: op.Implemented, Reason: &reason, RequiredCredentials: []string{}, SupportedOptions: options, Limits: limits})
	}
	return result
}
