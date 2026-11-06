package ports

import (
	"context"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/write"
)

type FileWriter interface {
	OpenWriter(context.Context, configuration.Snapshot, account.Credentials) (FileWriteSession, error)
}
type FileWriteSession interface {
	Meta(string) (file.Entry, error)
	Mkdir(string) error
	Delete(string) error
	CopyMove(string, string, string, string) error
	Close() error
}
type ShareGateway interface {
	OpenShares(context.Context, configuration.Snapshot, account.Credentials) (ShareSession, error)
}
type ShareSession interface {
	Create([]string, string, int, bool) (write.Share, error)
	List(int, bool) ([]write.Share, error)
	Detail(string) (write.Share, error)
	Cancel(string) error
	Inspect(string, string) (write.Access, error)
	SharedDirectory(write.Access, string) ([]file.Entry, error)
	Transfer(write.Access, []string, string) (write.Outcome, error)
	Close() error
}
type OfflineGateway interface {
	OpenOffline(context.Context, configuration.Snapshot, account.Credentials) (OfflineSession, error)
}
type OfflineSession interface {
	Add(string, string) (string, error)
	List() ([]write.Offline, error)
	Query([]string) ([]write.Offline, error)
	Cancel(string) error
	Delete(string) error
	Clear() (uint64, error)
	Close() error
}
type RecycleGateway interface {
	OpenRecycle(context.Context, configuration.Snapshot, account.Credentials) (RecycleSession, error)
}
type RecycleSession interface {
	List(int) ([]write.Recycled, error)
	Restore(string) ([]string, error)
	Delete(string) error
	Clear() (uint64, error)
	Close() error
}
type BusinessRepository interface {
	SaveInspection(context.Context, write.Inspection) error
	Inspection(context.Context, string) (write.Inspection, error)
	UpdateInspection(context.Context, write.Inspection) error
	BeginBusinessMutation(context.Context, operation.Mutation) (json.RawMessage, bool, error)
	FinishBusinessMutation(context.Context, operation.Mutation, json.RawMessage) error
}
