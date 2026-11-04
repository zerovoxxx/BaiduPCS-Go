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
