package localfs

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/session"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/staging"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"hash/fnv"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Receptions struct {
	files *Staging
	repo  ports.ReceptionRepository
	locks [64]sync.Mutex
}

func NewReceptions(files *Staging, repo ports.ReceptionRepository) *Receptions {
	return &Receptions{files: files, repo: repo}
}
func (s *Receptions) lock(id string) func() {
	h := fnv.New32a()
	h.Write([]byte(id))
	m := &s.locks[h.Sum32()%64]
	m.Lock()
	return m.Unlock
}
func (s *Receptions) read(ctx context.Context, id string) (staging.Reception, error) {
	r, e := s.repo.Resource(ctx, "reception", id)
	var v staging.Reception
	if e != nil {
		return v, e
	}
	if e = json.Unmarshal(r.Data, &v); e != nil {
		return v, e
	}
	v.Revision = r.Revision
	v.Expires = r.Expires
	return v, nil
}
func (s *Receptions) put(ctx context.Context, v staging.Reception, m operation.Mutation) (staging.Reception, error) {
	data, _ := json.Marshal(v)
	r, e := s.repo.PutResource(ctx, session.Resource{Kind: "reception", ID: v.ID, Owner: v.Owner, Expires: v.Expires, Data: data}, v.Revision, m)
	if e != nil {
		return v, e
	}
	if e = json.Unmarshal(r.Data, &v); e != nil {
		return v, e
	}
	v.Revision = r.Revision
	v.Expires = r.Expires
	return v, nil
}
func (s *Receptions) CreateReception(ctx context.Context, owner, name string, size int64, expected *staging.Expected, key string) (staging.Reception, error) {
	var v staging.Reception
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 1024 || strings.ContainsAny(name, "\x00\r\n/\\") || size < 0 || size > 256<<20 {
		return v, fault.New(fault.Invalid, "INVALID_REQUEST", "接收名称或长度无效")
	}
	m := operation.New(owner, "UPL05", key, []any{name, size, expected})
	unlock := s.lock(owner + "/" + key)
	defer unlock()
	if r, e := s.repo.ReplayResource(ctx, m); e != nil {
		return v, e
	} else if r != nil {
		_ = json.Unmarshal(r.Data, &v)
		v, e = s.read(ctx, v.ID)
		if e == nil {
			e = receptionUsable(v)
		}
		return v, e
	}
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return v, e
	}
	v = staging.Reception{ID: "receive_" + hex.EncodeToString(b[:]), StageID: "staged_" + hex.EncodeToString(b[:]), Owner: owner, Filename: name, State: "receiving", Size: size, Expected: expected, Expires: time.Now().Add(24 * time.Hour).Unix()}
	if _, e := rand.Read(b[:]); e != nil {
		return v, e
	}
	v.StageID = "staged_" + hex.EncodeToString(b[:])
	stage := staging.File{ID: v.StageID, Owner: owner, Filename: name, State: "writing", ExpiresAt: time.Unix(v.Expires, 0).UTC().Format(time.RFC3339Nano)}
	if e := s.repo.ReserveStage(ctx, stage); e != nil {
		return v, e
	}
	f, e := s.files.registry.OpenFile(ctx, "staging", v.StageID, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		_ = s.files.Delete(context.Background(), v.StageID, operation.Mutation{})
		return v, localError(e)
	}
	e = f.Sync()
	f.Close()
	if e != nil {
		_ = s.files.Delete(context.Background(), v.StageID, operation.Mutation{})
		return v, localError(e)
	}
	// A reserved writing row protects quotas even if the process dies before this
	// session commits. Retention later removes that orphan without publishing it.
	out, e := s.put(ctx, v, m)
	if e != nil {
		_ = s.files.Delete(context.Background(), v.StageID, operation.Mutation{})
	}
	return out, e
}
func receptionUsable(v staging.Reception) error {
	if v.State == "expired" || v.Expires <= time.Now().Unix() {
		return fault.New(fault.Gone, "RESOURCE_EXPIRED", "接收会话已过期或撤销")
	}
	return nil
}
func (s *Receptions) Reception(ctx context.Context, id string) (staging.Reception, error) {
	unlock := s.lock(id)
	defer unlock()
	v, e := s.read(ctx, id)
	if e != nil {
		return v, e
	}
	if e = receptionUsable(v); e != nil {
		return v, e
	}
	if v.State == "receiving" {
		f, e := s.open(ctx, v)
		if e != nil {
			return v, e
		}
		f.Close()
	}
	return v, nil
}
func (s *Receptions) open(ctx context.Context, v staging.Reception) (*os.File, error) {
	f, e := s.files.registry.OpenFile(ctx, "staging", v.StageID, os.O_RDWR, 0)
	if e != nil {
		return nil, fault.New(fault.Unavailable, "RECEPTION_DATA_UNAVAILABLE", "接收文件不可用")
	}
	st, e := f.Stat()
	if e != nil || st.Size() < v.Offset {
		f.Close()
		return nil, fault.New(fault.Unavailable, "RECEPTION_DATA_UNAVAILABLE", "接收文件短于已提交偏移")
	}
	if st.Size() > v.Offset {
		if e = f.Truncate(v.Offset); e == nil {
			e = f.Sync()
		}
		if e != nil {
			f.Close()
			return nil, localError(e)
		}
	}
	return f, nil
}
func (s *Receptions) PatchReception(ctx context.Context, id string, offset int64, reader io.Reader) (staging.Reception, error) {
	unlock := s.lock(id)
	defer unlock()
	v, e := s.read(ctx, id)
	if e != nil {
		return v, e
	}
	if e = receptionUsable(v); e != nil {
		return v, e
	}
	stage, e := s.repo.Stage(ctx, v.StageID)
	if e != nil {
		return v, e
	}
	if v.State != "receiving" || stage.State != "writing" || offset != v.Offset || v.Offset == v.Size {
		return v, fault.New(fault.Conflict, "UPLOAD_OFFSET_MISMATCH", "接收状态或偏移不匹配")
	}
	f, e := s.open(ctx, v)
	if e != nil {
		return v, e
	}
	defer f.Close()
	if _, e = f.Seek(offset, io.SeekStart); e != nil {
		return v, e
	}
	limit := min(v.Size-v.Offset, 32<<20)
	copyFn := s.files.copyFile
	if copyFn == nil {
		copyFn = io.Copy
	}
	n, e := copyFn(f, io.LimitReader(contextReader{ctx, reader}, limit+1))
	if e == nil && n > limit {
		e = fault.New(fault.TooLarge, "LIMIT_EXCEEDED", "块超过32MiB或剩余长度")
	}
	if e == nil {
		e = ctx.Err()
	}
	if e == nil {
		e = f.Sync()
	}
	if e != nil {
		_ = f.Truncate(offset)
		_ = f.Sync()
		return v, e
	}
	v.Offset += n
	v.Expires = time.Now().Add(24 * time.Hour).Unix()
	if e = s.repo.TouchStage(ctx, v.StageID, v.Expires); e != nil {
		_ = f.Truncate(offset)
		_ = f.Sync()
		return v, e
	}
	out, e := s.put(ctx, v, operation.Mutation{})
	if e != nil {
		_ = f.Truncate(offset)
		_ = f.Sync()
	}
	return out, e
}
func (s *Receptions) CompleteReception(ctx context.Context, id string) (staging.File, error) {
	unlock := s.lock(id)
	defer unlock()
	v, e := s.read(ctx, id)
	if e != nil {
		return staging.File{}, e
	}
	if e = receptionUsable(v); e != nil {
		return staging.File{}, e
	}
	stage, e := s.repo.Stage(ctx, v.StageID)
	if e != nil {
		return stage, e
	}
	if stage.State == "ready" || stage.State == "in_use" {
		if v.State != "completed" {
			v.State = "completed"
			if _, e = s.put(ctx, v, operation.Mutation{}); e != nil {
				return stage, e
			}
		}
		return stage, nil
	}
	if v.Offset != v.Size {
		return stage, fault.New(fault.Conflict, "UPLOAD_INCOMPLETE", "完整长度尚未接收")
	}
	f, e := s.open(ctx, v)
	if e != nil {
		return stage, e
	}
	defer f.Close()
	md, sha := md5.New(), sha256.New()
	if _, e = io.CopyBuffer(io.MultiWriter(md, sha), contextReader{ctx, f}, make([]byte, 64<<10)); e != nil {
		return stage, e
	}
	if v.Expected != nil {
		sum := hex.EncodeToString(md.Sum(nil))
		if v.Expected.Algorithm == "sha256" {
			sum = hex.EncodeToString(sha.Sum(nil))
		}
		if !strings.EqualFold(sum, v.Expected.Value) {
			return stage, fault.New(fault.Conflict, "CHECKSUM_MISMATCH", "接收内容与期望摘要不一致")
		}
	}
	if e = f.Sync(); e != nil {
		return stage, localError(e)
	}
	stage.Size = v.Size
	stage.State = "ready"
	stage, e = s.repo.FinishStage(ctx, stage, operation.Mutation{})
	if e != nil {
		return stage, e
	}
	v.State = "completed"
	_, e = s.put(ctx, v, operation.Mutation{})
	return stage, e
}
func (s *Receptions) DeleteReception(ctx context.Context, id string) error {
	unlock := s.lock(id)
	defer unlock()
	v, e := s.read(ctx, id)
	if e != nil {
		return e
	}
	if v.State == "expired" {
		return nil
	}
	if e = s.files.Delete(ctx, v.StageID, operation.Mutation{}); e != nil {
		return e
	}
	v.State = "expired"
	if v.Expires > time.Now().Unix() {
		v.Expires = time.Now().Unix() - 1
	}
	_, e = s.put(ctx, v, operation.Mutation{})
	return e
}
func (s *Receptions) CleanupReceptions(ctx context.Context) error {
	rows, e := s.repo.Resources(ctx, "reception", 10000)
	if e != nil {
		return e
	}
	for _, r := range rows {
		if r.Expires <= time.Now().Unix() {
			if e = s.DeleteReception(ctx, r.ID); e != nil && fault.As(e).Code != "STAGED_FILE_IN_USE" {
				return e
			}
		}
	}
	return nil
}
