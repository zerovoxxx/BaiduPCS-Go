package ports

import (
	"context"
	"encoding/json"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
)

type AccountRepository interface {
	CreateAccount(context.Context, account.Entry) error
	Account(context.Context, string) (account.Entry, error)
	Accounts(context.Context) ([]account.Entry, error)
	CreateAccountOnce(context.Context, account.Entry, operation.Mutation) (account.Entry, error)
	UpdateAccount(context.Context, account.Entry, uint64) error
	MutateAccount(context.Context, string, uint64, *string, account.Patch, operation.Mutation) (account.Entry, bool, error)
	DeleteAccount(context.Context, string, operation.Mutation) error
	CredentialVersion(context.Context, string, uint64) (account.Credentials, error)
}

type QuotaReader interface {
	AccountQuota(context.Context, configuration.Snapshot, account.Credentials) (account.Quota, error)
}

type AccountValidator interface {
	ValidateAccount(context.Context, configuration.Snapshot, account.Credentials) (account.Identity, error)
}

type TransferRepository interface {
	CreateTransfer(context.Context, transfer.Record) (bool, error)
	Transfer(context.Context, string) (transfer.Record, error)
	TransitionTransfer(context.Context, string, []string, string) (bool, error)
	SaveTransferCheckpoint(context.Context, string, json.RawMessage) error
	FinishTransfer(context.Context, string, string, string) error
	InterruptTransfers(context.Context) error
	QueuedTransfers(context.Context, int) ([]transfer.Record, error)
	ControlTransfer(context.Context, string, []string, string, operation.Mutation) (transfer.Record, error)
	ReplayControl(context.Context, operation.Mutation) (*transfer.Record, error)
	ActiveTransferCount(context.Context) (int, error)
	ListTransfers(context.Context, transfer.Filter, int, func(transfer.Record) bool) ([]transfer.Record, error)
	TransferItems(context.Context, string, int) ([]transfer.Item, error)
	TransferItemPage(context.Context, string, string, string, int) (QueryPage, error)
	RetryTransferItems(context.Context, string, bool) ([]transfer.Item, error)
	SaveScanItem(context.Context, string, transfer.Item, transfer.ScanResult) error
	SaveScanResult(context.Context, string, transfer.ScanResult) error
	EventSnapshot(context.Context, string) (transfer.Record, uint64, error)
	TransferEvents(context.Context, string, uint64, int) (transfer.EventBatch, error)
}

type TransferEngine interface {
	Execute(context.Context, transfer.Record, account.Credentials, func(json.RawMessage) error) error
}

type TransferPreparer interface {
	Prepare(context.Context, transfer.Record, account.Credentials) (string, error)
}

type TransferCleaner interface {
	CleanupTransfer(context.Context, transfer.Record) error
}
