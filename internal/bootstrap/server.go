// Package bootstrap owns HTTP service assembly, storage and process lifecycle.
package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	contract "github.com/qjfoidnh/BaiduPCS-Go/api/openapi"
	appaccounts "github.com/qjfoidnh/BaiduPCS-Go/internal/application/accounts"
	appartifacts "github.com/qjfoidnh/BaiduPCS-Go/internal/application/artifacts"
	appauth "github.com/qjfoidnh/BaiduPCS-Go/internal/application/auth"
	appconfig "github.com/qjfoidnh/BaiduPCS-Go/internal/application/configuration"
	appcontent "github.com/qjfoidnh/BaiduPCS-Go/internal/application/content"
	appfiles "github.com/qjfoidnh/BaiduPCS-Go/internal/application/files"
	applocal "github.com/qjfoidnh/BaiduPCS-Go/internal/application/local"
	appquery "github.com/qjfoidnh/BaiduPCS-Go/internal/application/query"
	appstaging "github.com/qjfoidnh/BaiduPCS-Go/internal/application/staging"
	appsystem "github.com/qjfoidnh/BaiduPCS-Go/internal/application/system"
	apptransfers "github.com/qjfoidnh/BaiduPCS-Go/internal/application/transfers"
	appwrites "github.com/qjfoidnh/BaiduPCS-Go/internal/application/writes"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/system"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/write"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/httpapi"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/baidu"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/identity"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/legacyconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/localfs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/network"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/persistence"
)

const DefaultListen = "127.0.0.1:5299"

type Options struct {
	Listen     string
	DataDir    string
	ConfigFile string
	TokenFile  string
	Version    string
	Logger     *slog.Logger
	// An assembly dependency, never a remotely writable startup field.
	ClientTransport func(configuration.Client) *http.Transport
}
type startup struct {
	LegacyAccountsFile string            `json:"legacy_accounts_file,omitempty"`
	Listen             string            `json:"listen"`
	DataDir            string            `json:"data_dir"`
	Principals         []identity.Record `json:"principals,omitempty"`
	Roots              []localfs.Root    `json:"roots,omitempty"`
	TLSCertificate     string            `json:"tls_certificate"`
	TLSKey             string            `json:"tls_key"`
}

// IsServerInvocation identifies only the server command through the existing
// global boolean flag prefix. Legacy help/version/commands keep their startup.
func IsServerInvocation(args []string) bool {
	for index, arg := range args {
		if arg == "--" {
			return index+1 < len(args) && args[index+1] == "server"
		}
		if arg == "--verbose" || arg == "-verbose" || strings.HasPrefix(arg, "--verbose=") || strings.HasPrefix(arg, "-verbose=") {
			continue
		}
		if arg == "help" || arg == "?" || arg == "？" {
			return index+1 < len(args) && args[index+1] == "server"
		}
		return arg == "server"
	}
	return false
}

type Runtime struct {
	Handler                  http.Handler
	Clients                  *baidu.ClientFactory
	Transfers                *apptransfers.Service
	DataDir                  string
	CredentialFile           string
	store                    *persistence.Store
	lock                     *persistence.DirectoryLock
	server                   *http.Server
	listen, certificate, key string
	draining                 atomic.Bool
	cancel                   context.CancelFunc
	once                     sync.Once
	closeError               error
	maintenanceDone          chan struct{}
}

func New(ctx context.Context, options Options) (*Runtime, error) {
	if !persistence.Supported() {
		return nil, errors.New("HTTP server is unsupported on this target; existing CLI commands remain available")
	}
	cfg := startup{Listen: DefaultListen}
	if options.ConfigFile != "" {
		info, err := os.Stat(options.ConfigFile)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return nil, errors.New("service config must be a readable regular JSON file within 1 MiB")
		}
		file, err := os.Open(options.ConfigFile)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		file.Close()
		if err != nil || len(data) > 1<<20 {
			return nil, errors.New("cannot read service config")
		}
		if err = httpapi.StrictJSON(data, &cfg); err != nil {
			return nil, errors.New("invalid service startup configuration")
		}
		// Resource paths in a service file are relative to that file, never CLI cwd.
		base, err := filepath.Abs(filepath.Dir(options.ConfigFile))
		if err != nil {
			return nil, err
		}
		resolve := func(value string) string {
			if value != "" && !filepath.IsAbs(value) {
				return filepath.Join(base, value)
			}
			return value
		}
		cfg.DataDir = resolve(cfg.DataDir)
		cfg.LegacyAccountsFile = resolve(cfg.LegacyAccountsFile)
		cfg.TLSCertificate = resolve(cfg.TLSCertificate)
		cfg.TLSKey = resolve(cfg.TLSKey)
	}
	if options.Listen != "" {
		cfg.Listen = options.Listen
	}
	if options.DataDir != "" {
		cfg.DataDir = options.DataDir
	}
	if cfg.Listen == "" {
		cfg.Listen = DefaultListen
	}
	if _, _, err := net.SplitHostPort(cfg.Listen); err != nil {
		return nil, errors.New("listen must be host:port")
	}
	if cfg.DataDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		cfg.DataDir = filepath.Join(base, "BaiduPCS-Go", "http-service")
	}
	if (cfg.TLSCertificate == "") != (cfg.TLSKey == "") {
		return nil, errors.New("both service TLS certificate and key are required")
	}
	if cfg.TLSCertificate != "" {
		if _, err := tls.LoadX509KeyPair(cfg.TLSCertificate, cfg.TLSKey); err != nil {
			return nil, errors.New("invalid service TLS certificate/key")
		}
	}
	directory, err := persistence.PrepareDirectory(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	lock, err := persistence.Lock(directory)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{lock: lock, DataDir: directory, listen: cfg.Listen, certificate: cfg.TLSCertificate, key: cfg.TLSKey}
	ok := false
	defer func() {
		if !ok {
			runtime.Close()
		}
	}()
	roots, err := localfs.New(cfg.Roots, directory, options.ConfigFile, options.TokenFile, cfg.TLSKey, cfg.LegacyAccountsFile)
	if err != nil {
		return nil, err
	}
	records := cfg.Principals
	if options.TokenFile != "" {
		if len(records) > 0 {
			return nil, errors.New("choose principals or --token-file, not both")
		}
		data, err := persistence.ReadPrivateFile(options.TokenFile, 513)
		if err != nil {
			return nil, err
		}
		value := strings.TrimSpace(string(data))
		if len(value) < 32 || !identity.ValidToken(value) {
			return nil, errors.New("API token file must contain 32 to 512 characters")
		}
		records = []identity.Record{adminRecord(value)}
	}
	if len(records) == 0 {
		records, runtime.CredentialFile, err = defaultIdentity(directory)
		if err != nil {
			return nil, err
		}
	}
	authenticator, err := identity.New(records)
	if err != nil {
		return nil, err
	}
	master, err := persistence.MasterKey(directory)
	if err != nil {
		return nil, err
	}
	defaults := baidu.DefaultConfiguration()
	runtime.store, err = persistence.Open(ctx, directory, master, defaults)
	for i := range master {
		master[i] = 0
	}
	if err != nil {
		return nil, err
	}
	runtime.Clients = baidu.NewClientFactoryWithTransport(options.ClientTransport)
	if _, err = runtime.store.Accounts(ctx); err != nil {
		return nil, err
	}
	if err = runtime.store.CleanupReplays(ctx); err != nil {
		return nil, err
	}
	cfgService := appconfig.New(runtime.store, defaults, roots, network.Environment{})
	operations, baseline, err := contract.Catalog()
	if err != nil {
		return nil, err
	}
	for i := range operations {
		if operations[i].ID == "CFG02" || operations[i].ID == "CFG04" || operations[i].ID == "CFG05" {
			limit := fmt.Sprint(httpapi.MaxJSONBodyBytes)
			operations[i].Limits = []system.Limit{{Name: "request_body_max_bytes", Value: &limit, Unit: "bytes", Source: "service"}}
		}
	}
	lifetime, cancel := context.WithCancel(context.Background())
	runtime.cancel = cancel
	artifactFiles, err := localfs.NewArtifacts(directory, runtime.store)
	if err != nil {
		return nil, err
	}
	queries := appquery.New(runtime.store)
	accountService := appaccounts.NewValidated(runtime.store, runtime.store, runtime.Clients)
	if cfg.LegacyAccountsFile != "" {
		legacy, err := legacyconfig.ReadAccounts(cfg.LegacyAccountsFile)
		if err != nil {
			return nil, err
		}
		for _, v := range legacy {
			if _, err = accountService.ImportCanonical(ctx, auth.Principal{ID: "legacy-import", Scopes: []string{"admin"}, Accounts: []string{"*"}}, v.Label, v.Username, v.Key, v.Credentials); err != nil {
				return nil, err
			}
		}
	}
	stageFiles, err := localfs.NewStaging(directory, runtime.store)
	if err != nil {
		return nil, err
	}
	transferEngine := baidu.NewSingleFileEngine(runtime.Clients, roots)
	transferEngine.SetRepository(runtime.store)
	transferEngine.SetStaging(stageFiles)
	writeEngine := appwrites.NewEngine(runtime.Clients, runtime.Clients, runtime.Clients, runtime.Clients, runtime.Clients, runtime.store)
	runtime.Transfers, err = apptransfers.New(lifetime, runtime.store, runtime.store, runtime.store, p3Engine{writes: writeEngine, transfer: transferEngine, scanner: appfiles.NewScanner(runtime.Clients, runtime.store, artifactFiles)})
	if err != nil {
		return nil, err
	}
	writeEngine.OnDownload(func(ctx context.Context, parent transfer.Record, paths []string) (string, error) {
		var plan write.Plan
		_ = json.Unmarshal(parent.Plan, &plan)
		var options write.Options
		_ = json.Unmarshal(parent.EffectiveOptions, &options)
		batch, _ := json.Marshal(transfer.Batch{Paths: paths, Directory: options.Destination.Directory})
		principal := auth.Principal{ID: parent.Owner, Scopes: []string{"transfer", "local.write"}, Accounts: []string{parent.Request.AccountID}, Roots: []string{options.Destination.RootID}}
		child, err := runtime.Transfers.Submit(ctx, principal, transfer.Request{Kind: "download", AccountID: parent.Request.AccountID, RootID: options.Destination.RootID, RelativePath: path.Join(options.Destination.Directory, path.Base(paths[0])), RemotePath: paths[0], Batch: string(batch), Options: string(options.DownloadOptions), Public: true, ParentJobID: parent.ID}, "parent-"+parent.ID)
		return child.ID, err
	})
	accountService.OnRevoke(func(ctx context.Context, id string) error {
		before := ^uint64(0)
		a, err := runtime.store.Account(ctx, id)
		if err == nil {
			before = a.Credentials.Revision
		}
		runtime.Clients.RevokeCredentials(id, before)
		return runtime.Transfers.RevokeAccount(ctx, id)
	})
	if err = stageFiles.Cleanup(ctx); err != nil {
		return nil, err
	}
	if err = artifactFiles.Cleanup(ctx); err != nil {
		return nil, err
	}
	configureP2Capabilities(operations)
	configureP3Capabilities(operations)
	configureP4Capabilities(operations)
	configureP5Capabilities(operations)
	fileService := appfiles.New(runtime.store, runtime.store, runtime.Clients, queries)
	contentService := appcontent.New(runtime.store, runtime.store, runtime.Clients, runtime.store)
	writeService := appwrites.New(runtime.store, runtime.store, runtime.store, runtime.Clients, runtime.Clients, runtime.Clients, queries)
	writeService.Selectors(fileService)
	handler, err := httpapi.New(httpapi.Dependencies{Auth: appauth.New(authenticator), Configuration: cfgService, Accounts: accountService, Transfers: runtime.Transfers, Files: fileService, Queries: queries, Local: applocal.New(roots), Writes: writeService, Content: contentService, Staging: appstaging.New(stageFiles), Artifacts: appartifacts.New(artifactFiles, runtime.Transfers), System: appsystem.New(options.Version, baseline, time.Now(), operations), Operations: operations, Contract: contract.JSON(), Draining: runtime.draining.Load, Logger: options.Logger, Ready: func(ctx context.Context) error {
		if err := runtime.Transfers.Ready(ctx); err != nil {
			return err
		}
		if err := runtime.store.Ping(ctx); err != nil {
			return err
		}
		snapshot, err := runtime.store.Read(ctx)
		if err != nil {
			return err
		}
		if directory := snapshot.Client().SaveDir; directory != nil {
			return roots.ValidateDestination(ctx, *directory)
		}
		return nil
	}})
	if err != nil {
		return nil, err
	}
	runtime.Handler = handler
	runtime.maintenanceDone = make(chan struct{})
	go func() {
		defer close(runtime.maintenanceDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-lifetime.Done():
				return
			case <-ticker.C:
				clean, done := context.WithTimeout(lifetime, 10*time.Second)
				err := errors.Join(artifactFiles.Cleanup(clean), stageFiles.Cleanup(clean), runtime.store.CleanupEvents(clean), runtime.store.CleanupReplays(clean), runtime.store.CleanupBusiness(clean))
				done()
				if err != nil && lifetime.Err() == nil && options.Logger != nil {
					options.Logger.Error("service retention cleanup failed")
				}
			}
		}
	}()
	runtime.server = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, BaseContext: func(net.Listener) context.Context { return lifetime }, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	ok = true
	return runtime, nil
}
func adminRecord(value string) identity.Record {
	sum := sha256.Sum256([]byte(value))
	return identity.Record{ID: "admin", TokenSHA256: hex.EncodeToString(sum[:]), Scopes: []string{"admin"}, Accounts: []string{"*"}, Roots: []string{"*"}}
}
func defaultIdentity(directory string) ([]identity.Record, string, error) {
	name := filepath.Join(directory, "principals.json")
	data, err := persistence.ReadPrivateFile(name, 1<<20)
	if err == nil {
		var records []identity.Record
		if err := httpapi.StrictJSON(data, &records); err != nil {
			return nil, "", errors.New("invalid stored service principals")
		}
		return records, "", nil
	}
	if !os.IsNotExist(err) {
		return nil, "", err
	}
	// This is a client handoff file. The authority reads principals.json hashes
	// on subsequent starts; the handoff can be moved or removed by its owner.
	handoff := filepath.Join(directory, "client-token")
	tokenData, err := persistence.ReadPrivateFile(handoff, 513)
	if os.IsNotExist(err) {
		random := make([]byte, 32)
		if _, err = rand.Read(random); err != nil {
			return nil, "", err
		}
		tokenData = []byte(base64.RawURLEncoding.EncodeToString(random))
		if err = persistence.WritePrivateFile(handoff, append(tokenData, '\n')); err != nil {
			return nil, "", err
		}
	} else if err != nil {
		return nil, "", err
	}
	value := strings.TrimSpace(string(tokenData))
	if len(value) < 32 || !identity.ValidToken(value) {
		return nil, "", errors.New("invalid client token handoff")
	}
	records := []identity.Record{adminRecord(value)}
	encoded, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return nil, "", err
	}
	if err = persistence.WritePrivateFile(name, append(encoded, '\n')); err != nil {
		return nil, "", err
	}
	return records, handoff, nil
}
func (r *Runtime) Serve(ctx context.Context, listener net.Listener) error {
	result := make(chan error, 1)
	go func() {
		if r.certificate != "" {
			result <- r.server.ServeTLS(listener, r.certificate, r.key)
		} else {
			result <- r.server.Serve(listener)
		}
	}()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		return r.Close()
	}
}
func (r *Runtime) Close() error {
	r.once.Do(func() {
		r.draining.Store(true)
		if r.server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := r.server.Shutdown(ctx)
			cancel()
			if err != nil {
				_ = r.server.Close()
				r.closeError = err
			}
		}
		if r.cancel != nil {
			r.cancel()
		}
		if r.maintenanceDone != nil {
			<-r.maintenanceDone
		}
		if r.Transfers != nil {
			if err := r.Transfers.Close(); err != nil {
				r.closeError = errors.Join(r.closeError, err)
				// Abort instance connections before another bounded wait. Storage
				// must outlive the last executor's checkpoint/terminal write.
				if r.Clients != nil {
					_ = r.Clients.Close()
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err = r.Transfers.CloseContext(ctx)
				cancel()
				if errors.Is(err, context.DeadlineExceeded) {
					go func() {
						_ = r.Transfers.CloseContext(context.Background())
						if r.store != nil {
							_ = r.store.Close()
						}
						_ = r.lock.Close()
					}()
					return
				}
				r.closeError = errors.Join(r.closeError, err)
			}
		}
		if r.Clients != nil {
			r.closeError = errors.Join(r.closeError, r.Clients.Close())
		}
		if r.store != nil {
			r.closeError = errors.Join(r.closeError, r.store.Close())
		}
		r.closeError = errors.Join(r.closeError, r.lock.Close())
	})
	return r.closeError
}

func configureP2Capabilities(operations []system.Operation) {
	for i := range operations {
		op := &operations[i]
		if op.Implementation != "partial" {
			continue
		}
		add := func(name, value, unit string) {
			if op.SupportedValues == nil {
				op.SupportedValues = map[string][]string{}
			}
			op.SupportedValues[name] = []string{value}
			if _, err := strconv.ParseUint(value, 10, 64); err != nil {
				return
			}
			if unit != "bytes" && unit != "bytes_per_second" && unit != "seconds" {
				unit = "count"
			}
			v := value
			op.Limits = append(op.Limits, system.Limit{Name: name, Value: &v, Unit: unit, Source: "service"})
		}
		add("request_body_max_bytes", fmt.Sprint(httpapi.MaxJSONBodyBytes), "bytes")
		switch op.ID {
		case "ACC01":
			op.SupportedValues["credential_kind"] = []string{"bduss", "cookies", "access_token"}
			add("cookie_input_max_bytes", "32768", "bytes")
			add("force_login_username", "unsupported", "policy")
		case "ACC02":
			add("max_accounts", "1000", "items")
		case "ACC07":
			op.SupportedValues["credential_kind"] = []string{"bduss", "cookies", "access_token"}
			add("upstream_scheme", "https", "policy")
			add("token_only_identity", "unverified", "policy")
		case "UPL01", "DWN01":
			op.RequiredCredentials = []string{"bduss"}
			add("max_files_per_request", "1", "items")
			add("min_file_size", "1", "bytes")
			add("max_file_size", "33554432", "bytes")
			add("running_jobs", "2", "jobs")
			add("waiting_jobs", "16", "jobs")
			add("max_retries", "0", "value")
			add("file_concurrency", "1", "value")
			add("max_rate_bytes_per_second", "0", "bytes_per_second")
			add("dry_run", "false", "value")
			add("remote_path", "absolute", "policy")
			add("upstream_scheme", "https", "policy")
			if op.ID == "UPL01" {
				op.SupportedOptions = []string{"file_concurrency", "parallel_per_file", "max_retries", "policy", "skip_rapid_check", "check_filename", "no_split_file", "max_rate_bytes_per_second"}
				add("source_type", "local", "value")
				add("parallel_per_file", "1", "value")
				add("policy", "skip", "value")
				add("skip_rapid_check", "true", "value")
				add("check_filename", "false", "value")
				add("no_split_file", "false", "value")
			} else {
				op.SupportedOptions = []string{"mode", "path_mode", "parallel", "file_concurrency", "max_retries", "overwrite", "verify_checksum", "preserve_mtime", "executable", "link_index", "test", "report_workers", "max_rate_bytes_per_second"}
				for name, value := range map[string]string{"mode": "pcs", "path_mode": "relative", "parallel": "1", "overwrite": "false", "verify_checksum": "true", "preserve_mtime": "false", "executable": "false", "link_index": "0", "test": "false", "report_workers": "false", "selector_kind": "paths"} {
					add(name, value, "value")
				}
			}
		case "JOB02", "JOB04", "JOB06", "JOB08":
			add("job_kinds", "upload,download", "values")
			add("control_body_reason_max_length", "1024", "characters")
			add("idempotency_min_retention", "86400", "seconds")
		}
	}
}
func Run(ctx context.Context, options Options, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	if options.Logger == nil {
		options.Logger = slog.New(slog.NewJSONHandler(output, nil))
	}
	runtime, err := New(ctx, options)
	if err != nil {
		return err
	}
	defer runtime.Close()
	listener, err := net.Listen("tcp", runtime.listen)
	if err != nil {
		return err
	}
	if runtime.CredentialFile != "" {
		fmt.Fprintf(output, "初始管理员客户端凭据：%s（0600；服务权限库只保存SHA-256摘要）\n", runtime.CredentialFile)
	}
	scheme := "http"
	if runtime.certificate != "" {
		scheme = "https"
	}
	fmt.Fprintf(output, "HTTP API: %s://%s，数据目录：%s\n", scheme, listener.Addr(), runtime.DataDir)
	return runtime.Serve(ctx, listener)
}
