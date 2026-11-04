package baidu

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
}

func NewSingleFileEngine(clients *ClientFactory, roots *localfs.Registry) *SingleFileEngine {
	return &SingleFileEngine{clients, roots}
}

type fileCheckpoint struct {
	Version                     int
	Binding, Phase, SHA256, MD5 string
	Temp                        string
	Skipped                     bool
	Size, Mtime, Offset         int64
	Upload                      *uploader.InstanceState
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
	if r.Request.Kind == "upload" && r.Config.MaxUploadRate != 0 || r.Request.Kind == "download" && r.Config.MaxDownloadRate != 0 {
		return unsupported("UNSUPPORTED_OPTION", "P2最小验证暂不支持限速，不能忽略已配置的速率限制")
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
	switch r.Request.Kind {
	case "upload":
		return e.upload(ctx, r, session, &cp, persist)
	case "download":
		return e.download(ctx, r, session, &cp, persist)
	default:
		return unsupported("UNSUPPORTED_OPTION", "最小验证仅支持单文件上传和下载")
	}
}
func sourceFingerprint(file *os.File) (size, mtime int64, sha, md string, err error) {
	before, err := file.Stat()
	if err != nil {
		return 0, 0, "", "", err
	}
	if !before.Mode().IsRegular() || before.Size() == 0 || before.Size() > 32<<20 {
		return 0, 0, "", "", unsupported("UNSUPPORTED_OPTION", "P2最小验证仅支持1字节至32MiB的普通文件")
	}
	s, m := sha256.New(), md5.New()
	if _, err = io.Copy(io.MultiWriter(s, m), file); err != nil {
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
	size, mtime, sha, md, err := sourceFingerprint(f)
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
	if cp.Upload == nil {
		// skip is a successful per-item outcome and transfers zero bytes.
		meta, metaErr := s.PCS.FilesDirectoriesMeta(r.Request.RemotePath)
		if metaErr == nil {
			if meta.Isdir {
				return fault.New(fault.Conflict, "TARGET_EXISTS", "上传目标是目录")
			}
			cp.Skipped = true
			cp.Phase = "done"
			return persist()
		}
		if fault.As(upstreamFault(metaErr)).Kind != fault.NotFound {
			return upstreamFault(metaErr)
		}
		// Persist the source binding before creating an upstream upload session.
		cp.Phase = "preparing"
		if err = persist(); err != nil {
			return err
		}
		upErr, pre := s.PCS.FakeRapidUpload(r.Request.RemotePath, baidupcs.SkipPolicy, size)
		if upErr != nil {
			return upErr
		}
		if pre.UploadID == "" {
			return errors.New("upstream omitted upload session")
		}
		cp.Upload = &uploader.InstanceState{Uploadid: pre.UploadID, BlockList: uploader.SplitBlock(size, baidupcs.MinUploadBlockSize)}
		cp.Phase = "uploading"
		if err = persist(); err != nil {
			return err
		}
	}
	provider := &singleUpload{session: s, ctx: ctx, checkpoint: cp, persist: persist, target: r.Request.RemotePath}
	provider.verify = func() error {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		currentSize, currentMtime, currentSHA, _, err := sourceFingerprint(f)
		if err != nil {
			return err
		}
		if currentSize != cp.Size || currentMtime != cp.Mtime || currentSHA != cp.SHA256 {
			return unsupported("CHECKPOINT_SOURCE_CHANGED", "上传期间源文件发生变化，拒绝合并")
		}
		return ctx.Err()
	}
	engine := uploader.NewMultiUploader(provider, rio.NewFileReaderAtLen64(f), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: baidupcs.MinUploadBlockSize, Policy: baidupcs.SkipPolicy}, r.Request.RemotePath)
	engine.SetInstanceState(cp.Upload)
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
}

func (p *singleUpload) Precreate() (string, pcserror.Error) { return p.session.PCS.GetPCSAddr(), nil }
func (p *singleUpload) TmpFile(ctx context.Context, id, target string, part int, offset int64, reader rio.ReaderLen64) (string, error) {
	checksum, upErr := p.session.PCS.UploadTmpFile(id, target, part, offset, func(address string, _ http.CookieJar) (*http.Response, error) {
		body := multipartreader.NewMultipartReader()
		body.AddFormFile("uploadedfile", "", reader)
		body.CloseMultipart()
		return p.session.HTTP.Req(http.MethodPost, address, body, nil)
	})
	if upErr != nil {
		return "", &uploader.MultiError{Terminated: true, Err: upErr}
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
		return fault.New(fault.Conflict, "REMOTE_RESULT_UNKNOWN", "远端合并结果未确认，须核对后恢复")
	}
	p.checkpoint.Phase = "done"
	if err := p.persist(); err != nil {
		return fault.New(fault.Conflict, "REMOTE_RESULT_UNKNOWN", "远端已合并但本地完成状态未持久化，须核对后恢复")
	}
	return nil
}
func (e *SingleFileEngine) download(ctx context.Context, r transfer.Record, s *Session, cp *fileCheckpoint, persist func() error) error {
	target, err := e.roots.File(ctx, r.Request.RootID, r.Request.RelativePath, true)
	if err != nil {
		return fault.New(fault.Forbidden, "LOCAL_PATH_FORBIDDEN", "本地下载目标不可访问")
	}
	meta, err := s.PCS.FilesDirectoriesMeta(r.Request.RemotePath)
	if err != nil {
		return err
	}
	if meta.Isdir || meta.Size < 1 || meta.Size > 32<<20 {
		return unsupported("UNSUPPORTED_OPTION", "P2最小验证仅支持不超过32MiB的单文件")
	}
	if cp.Phase != "" && (cp.Size != meta.Size || cp.MD5 != meta.MD5) {
		return unsupported("CHECKPOINT_SOURCE_CHANGED", "远端文件与检查点不一致")
	}
	if len(meta.MD5) != 32 {
		return unsupported("CHECKSUM_UNAVAILABLE", "缺少可核对摘要，拒绝复用下载检查点")
	}
	cp.Size, cp.MD5 = meta.Size, meta.MD5
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
	if _, err = os.Lstat(target); err == nil || !os.IsNotExist(err) {
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
	err = s.PCS.DownloadFile(r.Request.RemotePath, func(address string, _ http.CookieJar) error {
		d := downloader.NewDownloader(address, f, &downloader.Config{MaxParallel: 1})
		d.SetClient(s.HTTP)
		d.SetFileContentLength(meta.Size)
		return d.ExecuteContext(ctx, cp.Offset, func(offset int64) error {
			if err := f.Sync(); err != nil {
				return err
			}
			cp.Offset = offset
			return persist()
		})
	})
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, _, sha, md, err := sourceFingerprint(f)
	if err != nil {
		return err
	}
	if cp.MD5 != "" && !strings.EqualFold(cp.MD5, md) {
		return fault.New(fault.Conflict, "CHECKSUM_MISMATCH", "下载内容摘要与远端元信息不一致")
	}
	cp.SHA256 = sha
	cp.Phase = "committing"
	if err = persist(); err != nil {
		return err
	}
	if err = e.roots.CommitFile(ctx, r.Request.RootID, partialRef, r.Request.RelativePath, f); err != nil {
		return fault.New(fault.Conflict, "LOCAL_RESULT_UNKNOWN", "文件提交结果须在恢复时核对")
	}
	cp.Phase = "done"
	if err = persist(); err != nil {
		return fault.New(fault.Conflict, "LOCAL_RESULT_UNKNOWN", "文件已提交但完成状态未持久化，须核对后恢复")
	}
	return nil
}
