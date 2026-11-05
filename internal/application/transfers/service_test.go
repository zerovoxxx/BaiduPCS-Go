package transfers_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	appaccounts "github.com/qjfoidnh/BaiduPCS-Go/internal/application/accounts"
	apptransfers "github.com/qjfoidnh/BaiduPCS-Go/internal/application/transfers"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/baidu"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/persistence"
)

var admin = auth.Principal{ID: "admin", Scopes: []string{"admin"}, Accounts: []string{"*"}, Roots: []string{"*"}}

type controlledEngine struct {
	started   chan string
	release   chan struct{}
	mu        sync.Mutex
	targets   map[string]bool
	collision bool
}

func (e *controlledEngine) Prepare(_ context.Context, r transfer.Record, _ account.Credentials) (string, error) {
	return r.Request.RemotePath, nil
}
func (e *controlledEngine) Execute(ctx context.Context, r transfer.Record, _ account.Credentials, save func(json.RawMessage) error) error {
	e.mu.Lock()
	if e.targets[r.Request.RemotePath] {
		e.collision = true
	}
	e.targets[r.Request.RemotePath] = true
	e.mu.Unlock()
	defer func() { e.mu.Lock(); delete(e.targets, r.Request.RemotePath); e.mu.Unlock() }()
	e.started <- r.ID
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.release:
		return save(json.RawMessage(`{"Phase":"done","Size":1}`))
	}
}
func setup(t *testing.T) (*persistence.Store, *controlledEngine, string) {
	t.Helper()
	if !persistence.Supported() {
		t.Skip("storage unsupported")
	}
	store, err := persistence.Open(context.Background(), t.TempDir(), []byte(strings.Repeat("k", 32)), baidu.DefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	a, err := appaccounts.New(store).Import(context.Background(), admin, "", account.Import{Kind: "bduss", BDUSS: "TEST_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	return store, &controlledEngine{started: make(chan string, 32), release: make(chan struct{}), targets: map[string]bool{}}, a.ID
}
func waitState(t *testing.T, s *apptransfers.Service, id, state string) transfer.Record {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		r, err := s.Get(context.Background(), admin, id)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == state {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job never reached %s", state)
	return transfer.Record{}
}
func nextStart(t *testing.T, e *controlledEngine) string {
	t.Helper()
	select {
	case id := <-e.started:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not start")
		return ""
	}
}
func TestDurableAdmissionRepairsLostSubmissionAndRestart(t *testing.T) {
	store, engine, accountID := setup(t)
	repo := lostSubmissionStore{Store: store}
	service, err := apptransfers.New(context.Background(), repo, store, store, engine)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	req := transfer.Request{Kind: "upload", AccountID: accountID, RootID: "root", RelativePath: "a", RemotePath: "/a"}
	record, err := service.Submit(context.Background(), admin, req, "lost-after-commit")
	if !errors.Is(err, context.Canceled) {
		t.Fatal("failure window not injected", err)
	}
	if nextStart(t, engine) != record.ID {
		t.Fatal("durable row was not dispatched")
	}
	close(engine.release)
	waitState(t, service, record.ID, "succeeded")
	if err = service.Close(); err != nil {
		t.Fatal(err)
	}
	// A queued row at process death must execute after re-opening the service.
	record.ID = "job_queued_before_restart"
	record.Request.RemotePath = "/queued"
	record.Checkpoint = nil
	if _, err = store.CreateTransfer(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	next, err := apptransfers.New(context.Background(), store, store, store, engine)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	waitState(t, next, record.ID, "succeeded")
}

type lostSubmissionStore struct{ *persistence.Store }

func (s lostSubmissionStore) CreateTransfer(ctx context.Context, r transfer.Record) (bool, error) {
	created, err := s.Store.CreateTransfer(ctx, r)
	if err == nil && created {
		return created, context.Canceled
	}
	return created, err
}
func TestTargetLockCancellationAdmissionAndOwnerPermissions(t *testing.T) {
	store, engine, accountID := setup(t)
	service, err := apptransfers.New(context.Background(), store, store, store, engine)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	req := transfer.Request{Kind: "download", AccountID: accountID, RootID: "root_a", RelativePath: "same", RemotePath: "/same"}
	first, err := service.Submit(context.Background(), admin, req, "a")
	if err != nil {
		t.Fatal(err)
	}
	if nextStart(t, engine) != first.ID {
		t.Fatal("first did not start")
	}
	req.RootID = "root_alias"
	second, err := service.Submit(context.Background(), admin, req, "b")
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, service, second.ID, "running")
	select {
	case <-engine.started:
		t.Fatal("same target executed concurrently")
	case <-time.After(30 * time.Millisecond):
	}
	req.RemotePath = "/other"
	third, err := service.Submit(context.Background(), admin, req, "c")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Cancel(context.Background(), admin, second.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, service, second.ID, "cancelled")
	if nextStart(t, engine) != third.ID {
		t.Fatal("cancelled target lock did not free worker")
	}
	other := admin
	other.ID = "other"
	other.Scopes = []string{"transfer", "local.write"}
	if _, err = service.Get(context.Background(), other, first.ID); fault.As(err).Kind != fault.Forbidden {
		t.Fatal("owner permission bypass", err)
	}
	other = admin
	other.Roots = []string{"unrelated"}
	if _, err = service.Get(context.Background(), other, first.ID); fault.As(err).Kind != fault.Forbidden {
		t.Fatal("root permission bypass", err)
	}
	close(engine.release)
	waitState(t, service, first.ID, "succeeded")
	waitState(t, service, third.ID, "succeeded")
	engine.mu.Lock()
	collision := engine.collision
	engine.mu.Unlock()
	if collision {
		t.Fatal("target execution overlapped")
	}
}
func TestQueueBoundAndConcurrentSubmissionReplay(t *testing.T) {
	store, engine, accountID := setup(t)
	service, err := apptransfers.New(context.Background(), store, store, store, engine)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	req := transfer.Request{Kind: "upload", AccountID: accountID, RootID: "root", RelativePath: "file", RemotePath: "/one"}
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := service.Submit(context.Background(), admin, req, "same-key")
			if err != nil {
				errs <- err
			} else {
				ids <- r.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var id string
	for current := range ids {
		if id != "" && current != id {
			t.Fatal("concurrent replay duplicated jobs")
		}
		id = current
	}
	for i := 0; i < 17; i++ {
		if _, err = service.Submit(context.Background(), admin, req, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = service.Submit(context.Background(), admin, req, ""); fault.As(err).Kind != fault.RateLimited {
		t.Fatal("unbounded queue", err)
	}
	// Existing logical submissions remain replayable even when admission is full.
	if replay, err := service.Submit(context.Background(), admin, req, "same-key"); err != nil || replay.ID != id {
		t.Fatal("full queue blocked replay", err)
	}
	req.RemotePath = "/changed"
	if _, err = service.Submit(context.Background(), admin, req, "same-key"); fault.As(err).Kind != fault.Conflict {
		t.Fatal("different body reused idempotency key", err)
	}
}
func TestInterruptedCommitCannotBeReportedCancelled(t *testing.T) {
	store, engine, accountID := setup(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	cfg, _ := store.Read(context.Background())
	r := transfer.Record{ID: "job_commit_unknown", Owner: admin.ID, CreatedAt: now, Request: transfer.Request{Kind: "upload", AccountID: accountID, RootID: "root", RelativePath: "file", RemotePath: "/file"}, Config: cfg.Client(), ConfigRevision: cfg.Revision(), CredentialRevision: 1, Checkpoint: json.RawMessage(`{"Phase":"committing"}`)}
	if _, err := store.CreateTransfer(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionTransfer(context.Background(), r.ID, []string{"queued"}, "interrupted"); err != nil {
		t.Fatal(err)
	}
	service, err := apptransfers.New(context.Background(), store, store, store, engine)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err = service.Cancel(context.Background(), admin, r.ID); fault.As(err).Code != "RECONCILIATION_REQUIRED" {
		t.Fatal("unknown commit was discarded", err)
	}
	waitState(t, service, r.ID, "interrupted")
}

func TestP3PreservesP2IdempotentAdmissionIdentity(t *testing.T) {
	store, engine, accountID := setup(t)
	service, err := apptransfers.New(context.Background(), store, store, store, engine)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	req := transfer.Request{Kind: "upload", AccountID: accountID, RootID: "root", RelativePath: "source", RemotePath: "/target"}
	r, err := service.Submit(context.Background(), admin, req, "p2-existing-key")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]string{admin.ID, accountID, "upload", "p2-existing-key"})
	sum := sha256.Sum256(raw)
	if r.ID != "job_"+hex.EncodeToString(sum[:]) {
		t.Fatal("P3 changed a P2 idempotent job ID")
	}
}

func TestP3LegacyAdmissionReplaysInitialReceiptWithoutResettingJob(t *testing.T) {
	store, engine, accountID := setup(t)
	ctx := context.Background()
	req := transfer.Request{Kind: "upload", AccountID: accountID, RootID: "root", RelativePath: "source", RemotePath: "/target"}
	raw, _ := json.Marshal([]string{admin.ID, accountID, "upload", "legacy-receipt"})
	sum := sha256.Sum256(raw)
	cfg, err := store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacy := transfer.Record{ID: "job_" + hex.EncodeToString(sum[:]), Owner: admin.ID, Request: req, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Config: cfg.Client(), ConfigRevision: cfg.Revision(), CredentialRevision: 1}
	if _, err = store.CreateTransfer(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err = store.TransitionTransfer(ctx, legacy.ID, []string{"queued"}, "running"); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveTransferCheckpoint(ctx, legacy.ID, json.RawMessage(`{"Phase":"done","Size":1}`)); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishTransfer(ctx, legacy.ID, "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	service, err := apptransfers.New(ctx, store, store, store, engine)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	receipt, err := service.Submit(ctx, admin, req, "legacy-receipt")
	if err != nil || receipt.State != "queued" || receipt.Version != 1 || receipt.FinishedAt != "" || len(receipt.Checkpoint) != 0 {
		t.Fatal("legacy submission did not replay its first receipt", err)
	}
	current, err := service.Get(ctx, admin, legacy.ID)
	if err != nil || current.State != "succeeded" || len(current.Checkpoint) == 0 {
		t.Fatal("receipt replay reset actual job state", err)
	}
}

func TestP3QueuedUnknownCommitSurvivesCancellationAndRevocation(t *testing.T) {
	store, engine, accountID := setup(t)
	ctx := context.Background()
	cfg, err := store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := transfer.Record{ID: "job_p3_unknown", Owner: admin.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Request: transfer.Request{Kind: "upload", AccountID: accountID, RootID: "root", RelativePath: "source", RemotePath: "/unknown"}, Config: cfg.Client(), ConfigRevision: cfg.Revision(), CredentialRevision: 1, Checkpoint: json.RawMessage(`{"Phase":"committing"}`)}
	if _, err = store.CreateTransfer(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err = store.TransitionTransfer(ctx, r.ID, []string{"queued"}, "running"); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishTransfer(ctx, r.ID, "interrupted", "REMOTE_RESULT_UNKNOWN"); err != nil {
		t.Fatal(err)
	}
	service, err := apptransfers.New(ctx, store, store, store, engine)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	for _, remote := range []string{"/block-a", "/block-b"} {
		if _, err = service.Submit(ctx, admin, transfer.Request{Kind: "upload", AccountID: accountID, RootID: "root", RelativePath: "source", RemotePath: remote}, ""); err != nil {
			t.Fatal(err)
		}
		_ = nextStart(t, engine)
	}
	if _, err = service.Resume(ctx, admin, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Cancel(ctx, admin, r.ID); fault.As(err).Code != "RECONCILIATION_REQUIRED" {
		t.Fatal("queued recovery falsely allowed cancellation", err)
	}
	accounts := appaccounts.New(store)
	accounts.OnRevoke(service.RevokeAccount)
	if _, err = accounts.Patch(ctx, admin, accountID, 1, nil, account.Patch{"cookies": nil}, ""); err != nil {
		t.Fatal(err)
	}
	restored := waitState(t, service, r.ID, "interrupted")
	if restored.FailureCode != "CREDENTIAL_REVOKED" {
		t.Fatal("revocation lost uncertain commit state", restored.FailureCode)
	}
}
