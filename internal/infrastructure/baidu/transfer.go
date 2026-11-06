package baidu

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/infrastructure/localfs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/downloader"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/multipartreader"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

// SingleFileEngine deliberately validates a small real slice before the full
// directory/parallel/rapid-upload service is implemented. It has no CLI globals.
type SingleFileEngine struct {
	clients *ClientFactory
	roots   *localfs.Registry
	repo    ports.TransferRepository
	staging *localfs.Staging
}

func NewSingleFileEngine(clients *ClientFactory, roots *localfs.Registry) *SingleFileEngine {
	return &SingleFileEngine{clients: clients, roots: roots}
}

type fileCheckpoint struct {
	Version                     int
	Binding, Phase, SHA256, MD5 string
	Temp                        string
	Skipped                     bool
	Size, Mtime, Offset         int64
	Upload                      *uploader.InstanceState
	BlockMD5                    []string
	BlockSize                   int64
	Rapid                       bool
	Workers                     []transfer.Worker
}

func unsupported(code, message string) error { return fault.New(fault.Unsupported, code, message) }
func binding(r transfer.Record) string {
	var request any = r.Request
	if !r.Request.Public && r.Request.Options == "" {
		request = struct{ Kind, AccountID, RootID, RelativePath, RemotePath string }{r.Request.Kind, r.Request.AccountID, r.Request.RootID, r.Request.RelativePath, r.Request.RemotePath}
	}
	b, _ := json.Marshal([]any{r.ID, request, r.ConfigRevision, r.CredentialRevision})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (e *SingleFileEngine) Execute(ctx context.Context, r transfer.Record, c account.Credentials, save func(json.RawMessage) error) (result error) {
	defer func() {
		if result != nil {
			result = upstreamFault(result)
		}
	}()
	ctx = transferContext(ctx, r)
	if len(r.Plan) > 0 {
		return e.executePlan(ctx, r, c, save)
	}
	if !configuration.ValidID(r.ID) || r.Request.AccountID != c.AccountID || r.CredentialRevision != c.Revision || r.Request.RemotePath == "/" || !strings.HasPrefix(r.Request.RemotePath, "/") || path.Clean(r.Request.RemotePath) != r.Request.RemotePath || strings.ContainsAny(r.Request.RemotePath, "\x00\r\n") {
		return unsupported("INVALID_TRANSFER_BINDING", "任务必须绑定明确的账号版本和绝对文件路径")
	}
	var cp fileCheckpoint
	if len(r.Checkpoint) > 0 {
		if json.Unmarshal(r.Checkpoint, &cp) != nil || cp.Binding != binding(r) {
			return unsupported("CHECKPOINT_BINDING_MISMATCH", "检查点不属于当前账号、目标和任务版本")
		}
		if cp.Version > 1 {
			return unsupported("CHECKPOINT_VERSION_UNSUPPORTED", "检查点版本高于当前服务")
		}
		if cp.Version == 0 && r.Request.Kind == "download" && cp.Phase != "" {
			cp.Temp = ".baidupcs-" + r.ID + ".part"
		}
	}
	cp.Version = 1
	cp.Binding = binding(r)
	persist := func() error {
		b, err := json.Marshal(cp)
		if err != nil {
			return err
		}
		return save(b)
	}
	session, err := e.clients.New(ctx, configuration.NewSnapshot(r.ConfigRevision, r.Config), c)
	if err != nil {
		return err
	}
	defer session.Close()
	// The job context owns transfer duration; synchronous metadata still has the
	// configured transport's header/connection timeouts.
	session.HTTP.SetTimeout(0)
	session.HTTP.Client.Transport = pacedTransport{base: session.HTTP.Client.Transport, limiter: ctx.Value(limiterKey{}).(*rio.Limiter), ctx: ctx}
	switch r.Request.Kind {
	case "upload":
		return executeAttempts(ctx, r, &cp, persist, func() error { return e.upload(ctx, r, session, &cp, persist) })
	case "download":
		return executeAttempts(ctx, r, &cp, persist, func() error { return e.download(ctx, r, session, &cp, persist) })
	default:
		return unsupported("UNSUPPORTED_OPTION", "最小验证仅支持单文件上传和下载")
	}
}
func sourceFingerprint(file *os.File, contexts ...context.Context) (size, mtime int64, sha, md string, err error) {
	before, err := file.Stat()
	if err != nil {
		return 0, 0, "", "", err
	}
	if !before.Mode().IsRegular() || before.Size() < 0 || before.Size() > baidupcs.MaxUploadSize {
		return 0, 0, "", "", unsupported("UNSUPPORTED_OPTION", "仅支持不超过128GiB的普通文件")
	}
	s, m := sha256.New(), md5.New()
	if _, err = io.Copy(io.MultiWriter(s, m), contextReader{Reader: file, ctx: firstContext(contexts)}); err != nil {
		return 0, 0, "", "", err
	}
	after, err := file.Stat()
	if err != nil {
		return 0, 0, "", "", err
	}
	if before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return 0, 0, "", "", unsupported("CHECKPOINT_SOURCE_CHANGED", "计算摘要期间源文件发生变化")
	}
	return before.Size(), before.ModTime().UnixNano(), hex.EncodeToString(s.Sum(nil)), hex.EncodeToString(m.Sum(nil)), nil
}
func (e *SingleFileEngine) upload(ctx context.Context, r transfer.Record, s *Session, cp *fileCheckpoint, persist func() error) error {
	_, err := e.roots.File(ctx, r.Request.RootID, r.Request.RelativePath, false)
	if err != nil {
		return fault.New(fault.Forbidden, "LOCAL_PATH_FORBIDDEN", "本地上传源不可访问")
	}
	f, err := e.roots.OpenFile(ctx, r.Request.RootID, r.Request.RelativePath, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	size, mtime, sha, md, err := sourceFingerprint(f, ctx)
	if err != nil {
		return err
	}
	if cp.Phase != "" && (cp.Size != size || cp.Mtime != mtime || cp.SHA256 != sha) {
		return unsupported("CHECKPOINT_SOURCE_CHANGED", "源文件与检查点不一致")
	}
	cp.Size, cp.Mtime, cp.SHA256, cp.MD5 = size, mtime, sha, md
	if cp.Skipped && cp.Phase == "done" {
		return persist()
	}
	if cp.Phase == "committing" || cp.Phase == "done" {
		meta, err := s.PCS.FilesDirectoriesMeta(r.Request.RemotePath)
		if err != nil || meta.Isdir || meta.Size != size || !strings.EqualFold(meta.MD5, md) {
			return fault.New(fault.Conflict, "REMOTE_RESULT_UNKNOWN", "必须核对远端合并结果，禁止自动重放")
		}
		cp.Phase = "done"
		return persist()
	}
	opts := uploadSettings(r)
	if err = prepareUpload(ctx, r, s, f, cp, persist, opts); err != nil {
		return err
	}
	if cp.Phase == "done" {
		return nil
	}
	provider := &singleUpload{session: s, ctx: ctx, checkpoint: cp, persist: persist, target: r.Request.RemotePath, file: f}
	if size == 0 {
		return provider.CreateSuperFile("", opts.Policy, cp.Upload.Uploadid, 0, map[int]string{0: baidupcs.EmptyContentMD5})
	}
	provider.verify = func() error {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		currentSize, currentMtime, currentSHA, _, err := sourceFingerprint(f, ctx)
		if err != nil {
			return err
		}
		if currentSize != cp.Size || currentMtime != cp.Mtime || currentSHA != cp.SHA256 {
			return unsupported("CHECKPOINT_SOURCE_CHANGED", "上传期间源文件发生变化，拒绝合并")
		}
		return ctx.Err()
	}
	engine := uploader.NewMultiUploader(provider, rio.NewFileReaderAtLen64(f), &uploader.MultiUploaderConfig{Parallel: opts.Parallel, BlockSize: cp.BlockSize, Policy: opts.Policy}, r.Request.RemotePath)
	engine.SetInstanceState(cp.Upload)
	stop := context.AfterFunc(ctx, engine.Cancel)
	defer stop()
	var runErr error
	engine.OnError(func(err error) { runErr = err })
	engine.OnCancel(func() { runErr = context.Canceled })
	engine.Execute()
	if runErr != nil {
		return runErr
	}
	if err = ctx.Err(); err != nil && cp.Phase != "done" {
		return err
	}
	return nil
}

func (e *SingleFileEngine) CleanupTransfer(ctx context.Context, r transfer.Record) error {
	if len(r.Plan) > 0 && r.State == "cancelled" {
		var plan transfer.Plan
		var cp planCheckpoint
		if json.Unmarshal(r.Plan, &plan) != nil || json.Unmarshal(r.Checkpoint, &cp) != nil || cp.Binding != binding(r) {
			return nil
		}
		for i, u := range plan.Units {
			child := r.Clone()
			child.ID = fmt.Sprintf("%s_u%d", r.ID, i)
			child.Plan = nil
			child.Request.Batch = ""
			child.Request.RootID = u.RootID
			child.Request.RelativePath = u.RelativePath
			child.Request.RemotePath = u.RemotePath
			child.Checkpoint = cp.Units[fmt.Sprint(i)]
			if err := e.CleanupTransfer(ctx, child); err != nil {
				return err
			}
		}
		return nil
	}

	if r.Request.Kind != "download" || r.State != "cancelled" || len(r.Checkpoint) == 0 {
		return nil
	}
	var cp fileCheckpoint
	if json.Unmarshal(r.Checkpoint, &cp) != nil || cp.Binding != binding(r) || cp.Version != 1 {
		return nil
	}
	// Old predictable names are retained. Only the persisted random allocation
	// of this exact job may be removed; a final committed target is never removed.
	prefix := ".baidupcs-" + r.ID + "-"
	if filepath.Base(cp.Temp) != cp.Temp || !strings.HasPrefix(cp.Temp, prefix) || len(strings.TrimSuffix(cp.Temp, ".part")) != len(prefix)+32 || !strings.HasSuffix(cp.Temp, ".part") {
		return nil
	}
	relative := filepath.ToSlash(filepath.Join(filepath.Dir(r.Request.RelativePath), cp.Temp))
	return e.roots.RemoveFile(ctx, r.Request.RootID, relative)
}

type singleUpload struct {
	session    *Session
	ctx        context.Context
	checkpoint *fileCheckpoint
	persist    func() error
	target     string
	mu         sync.Mutex
	verify     func() error
	file       *os.File
}

func (p *singleUpload) Precreate() (string, pcserror.Error) { return p.session.PCS.GetPCSAddr(), nil }
func (p *singleUpload) TmpFile(ctx context.Context, id, target string, part int, offset int64, reader rio.ReaderLen64) (string, error) {
	checksum, upErr := p.session.PCS.UploadTmpFile(id, target, part, offset, func(address string, _ http.CookieJar) (*http.Response, error) {
		body := multipartreader.NewMultipartReader()
		body.AddFormFile("uploadedfile", "", reader)
		body.CloseMultipart()
		req, err := http.NewRequestWithContext(p.ctx, http.MethodPost, address, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", body.ContentType())
		req.ContentLength = body.Len()
		return p.session.HTTP.Client.Do(req)
	})
	if upErr != nil {
		return "", &uploader.MultiError{Terminated: true, Err: upErr}
	}
	if part < 0 || part >= len(p.checkpoint.BlockMD5) || !strings.EqualFold(checksum, p.checkpoint.BlockMD5[part]) {
		return "", &uploader.MultiError{Terminated: true, Err: fault.New(fault.Conflict, "CHECKSUM_MISMATCH", "上游分片摘要与本地分片不一致")}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, b := range p.checkpoint.Upload.BlockList {
		if b.ID == part {
			b.CheckSum = checksum
		}
	}
	if err := p.persist(); err != nil {
		return "", &uploader.MultiError{Terminated: true, Err: err}
	}
	return checksum, nil
}
func (p *singleUpload) CreateSuperFile(_, policy, id string, size int64, checksums map[int]string) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if p.verify != nil {
		if err := p.verify(); err != nil {
			return err
		}
	}
	p.checkpoint.Phase = "committing"
	if err := p.persist(); err != nil {
		return err
	}
	if err := p.session.PCS.UploadCreateSuperFile(id, policy, size, p.target, checksums); err != nil {
		if err.GetErrType() == pcserror.ErrTypeRemoteError {
			p.checkpoint.Phase = "uploading"
			if saveErr := p.persist(); saveErr != nil {
				return saveErr
			}
			return err
		}
		return fault.New(fault.Conflict, "REMOTE_RESULT_UNKNOWN", "远端合并结果未确认，须核对后恢复")
	}
	p.checkpoint.Phase = "done"
	if err := p.persist(); err != nil {
		return fault.New(fault.Conflict, "REMOTE_RESULT_UNKNOWN", "远端已合并但本地完成状态未持久化，须核对后恢复")
	}
	return nil
}
func (e *SingleFileEngine) download(ctx context.Context, r transfer.Record, s *Session, cp *fileCheckpoint, persist func() error) error {
	opts := downloadSettings(r)
	target, err := e.roots.File(ctx, r.Request.RootID, r.Request.RelativePath, true)
	if err != nil {
		return fault.New(fault.Forbidden, "LOCAL_PATH_FORBIDDEN", "本地下载目标不可访问")
	}
	meta, err := s.PCS.FilesDirectoriesMeta(r.Request.RemotePath)
	if err != nil {
		return err
	}
	if meta.Isdir || meta.Size < 0 || meta.Size > baidupcs.MaxUploadSize {
		return unsupported("UNSUPPORTED_OPTION", "仅支持不超过128GiB的单个普通文件")
	}
	if cp.Phase != "" && (cp.Size != meta.Size || cp.MD5 != meta.MD5) {
		return unsupported("CHECKPOINT_SOURCE_CHANGED", "远端文件与检查点不一致")
	}
	if opts.Verify && len(meta.MD5) != 32 {
		return unsupported("CHECKSUM_UNAVAILABLE", "缺少可核对摘要，拒绝复用下载检查点")
	}
	cp.Size, cp.MD5 = meta.Size, meta.MD5
	if opts.Test && cp.Phase == "done" {
		return persist()
	}
	if cp.Phase == "committing" || cp.Phase == "done" {
		if file, err := e.roots.OpenFile(ctx, r.Request.RootID, r.Request.RelativePath, os.O_RDWR, 0); err == nil {
			size, _, sha, _, err := sourceFingerprint(file)
			file.Close()
			if err == nil && size == cp.Size && sha == cp.SHA256 {
				cp.Phase = "done"
				return persist()
			}
			return fault.New(fault.Conflict, "LOCAL_RESULT_UNKNOWN", "最终文件与已确认下载结果不一致")
		}
	}
	if _, err = os.Lstat(target); !opts.Overwrite && !opts.Test && (err == nil || !os.IsNotExist(err)) {
		return fault.New(fault.Conflict, "TARGET_EXISTS", "下载目标已存在")
	}
	if cp.Temp == "" {
		var nonce [16]byte
		if _, err = rand.Read(nonce[:]); err != nil {
			return err
		}
		cp.Temp = ".baidupcs-" + r.ID + "-" + hex.EncodeToString(nonce[:]) + ".part"
		cp.Phase = "allocating"
		// Record the unguessable job-owned name before creating a file. A crash
		// immediately after creation can now reopen precisely this allocation.
		if err = persist(); err != nil {
			return err
		}
	}
	legacy := cp.Temp == ".baidupcs-"+r.ID+".part" && !r.Request.Public
	if filepath.Base(cp.Temp) != cp.Temp || (!legacy && !strings.HasPrefix(cp.Temp, ".baidupcs-"+r.ID+"-")) || !strings.HasSuffix(cp.Temp, ".part") {
		return unsupported("CHECKPOINT_BINDING_MISMATCH", "临时文件不属于当前任务")
	}
	partial := filepath.Join(filepath.Dir(target), cp.Temp)
	relative, _ := filepath.Rel(filepath.Dir(target), partial)
	partialRef := filepath.ToSlash(filepath.Join(filepath.Dir(r.Request.RelativePath), relative))
	if _, err = e.roots.File(ctx, r.Request.RootID, partialRef, true); err != nil {
		return err
	}
	flags := os.O_RDWR
	if cp.Phase == "allocating" {
		flags |= os.O_CREATE | os.O_EXCL
	}
	f, err := e.roots.OpenFile(ctx, r.Request.RootID, partialRef, flags, 0600)
	if os.IsExist(err) && cp.Phase == "allocating" {
		f, err = e.roots.OpenFile(ctx, r.Request.RootID, partialRef, os.O_RDWR, 0600)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.Size() < cp.Offset {
		return unsupported("CHECKPOINT_SOURCE_CHANGED", "下载临时文件小于已确认偏移")
	}
	if err = f.Truncate(cp.Offset); err != nil {
		return err
	}
	cp.Phase = "downloading"
	if err = persist(); err != nil {
		return err
	}
	address, urlErr := downloadURL(s, r.Request.RemotePath, opts.Mode, opts.Index)
	if urlErr != nil {
		return urlErr
	}
	d := downloader.NewDownloader(address, f, &downloader.Config{MaxParallel: opts.Parallel})
	d.SetClient(s.HTTP)
	d.SetFileContentLength(meta.Size)
	var progressMu sync.Mutex
	err = d.ExecuteContext(ctx, cp.Offset, func(offset int64) error {
		progressMu.Lock()
		defer progressMu.Unlock()
		if err := f.Sync(); err != nil {
			return err
		}
		cp.Offset = offset
		return persist()
	}, func(id int, offset, n int64, state string) {
		if !opts.Report {
			return
		}
		progressMu.Lock()
		defer progressMu.Unlock()
		for len(cp.Workers) <= id {
			cp.Workers = append(cp.Workers, transfer.Worker{ID: fmt.Sprintf("worker_%d", len(cp.Workers)), State: "waiting"})
		}
		cp.Workers[id].State = state
		cp.Workers[id].Offset = offset + n
		cp.Workers[id].Transferred += n
	})
	if err != nil {
		return err
	}
	for i := range cp.Workers {
		cp.Workers[i].State = "completed"
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, _, sha, md, err := sourceFingerprint(f, ctx)
	if err != nil {
		return err
	}
	if opts.Verify && cp.MD5 != "" && !strings.EqualFold(cp.MD5, md) {
		return fault.New(fault.Conflict, "CHECKSUM_MISMATCH", "下载内容摘要与远端元信息不一致")
	}
	cp.SHA256 = sha
	if opts.Executable {
		if err = f.Chmod(0700); err != nil {
			return err
		}
	}
	if opts.Mtime {
		if err = localfs.SetFileMtime(f, meta.Mtime); err != nil {
			return err
		}
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if opts.Test {
		if err = e.roots.RemoveFile(ctx, r.Request.RootID, partialRef); err != nil {
			return err
		}
		cp.Phase = "done"
		return persist()
	}
	cp.Phase = "committing"
	if err = persist(); err != nil {
		return err
	}
	commit := e.roots.CommitFile
	if opts.Overwrite {
		commit = e.roots.ReplaceFile
	}
	if err = commit(ctx, r.Request.RootID, partialRef, r.Request.RelativePath, f); err != nil {
		return fault.New(fault.Conflict, "LOCAL_RESULT_UNKNOWN", "文件提交结果须在恢复时核对")
	}
	cp.Phase = "done"
	if err = persist(); err != nil {
		return fault.New(fault.Conflict, "LOCAL_RESULT_UNKNOWN", "文件已提交但完成状态未持久化，须核对后恢复")
	}
	return nil
}

type contextReader struct {
	io.Reader
	ctx context.Context
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(b)
}
func firstContext(all []context.Context) context.Context {
	if len(all) > 0 {
		return all[0]
	}
	return context.Background()
}
