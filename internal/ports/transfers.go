package ports

import (
	"context"
	"encoding/json"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
)

type AccountRepository interface {
	CreateAccount(context.Context, account.Entry) error
	Account(context.Context, string) (account.Entry, error)
}

type TransferRepository interface {
	CreateTransfer(context.Context, transfer.Record) (bool, error)
	Transfer(context.Context, string) (transfer.Record, error)
	TransitionTransfer(context.Context, string, []string, string) (bool, error)
	SaveTransferCheckpoint(context.Context, string, json.RawMessage) error
	FinishTransfer(context.Context, string, string, string) error
	InterruptTransfers(context.Context) error
}

type TransferEngine interface {
	Execute(context.Context, transfer.Record, account.Credentials, func(json.RawMessage) error) error
}
