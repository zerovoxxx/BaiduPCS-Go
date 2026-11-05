package localfs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/artifact"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const ArtifactMaxBytes int64 = 256 << 20

// Artifacts is separate from user roots. Metadata is durable; reader leases are
// process-local because a restarted process cannot still own an open response.
type Artifacts struct {
	directory string
	registry  *Registry
	repo      ports.ArtifactRepository
	mu        sync.Mutex
	readers   map[string]int
}

func NewArtifacts(directory string, r ports.ArtifactRepository) (*Artifacts, error) {
	dir := filepath.Join(directory, "artifacts")
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("invalid artifact directory")
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	return &Artifacts{directory: dir, registry: &Registry{roots: map[string]Root{"artifacts": {ID: "artifacts", Path: dir, Read: true, Write: true}}}, repo: r, readers: map[string]int{}}, nil
}
func (s *Artifacts) file(id string) string { return filepath.Join(s.directory, id) }
func (s *Artifacts) Create(ctx context.Context, job, name, media string) (ports.ArtifactWriter, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	a := artifact.Metadata{ID: "artifact_" + hex.EncodeToString(random[:]), JobID: job, Filename: name, MediaType: media, CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339Nano), State: "writing"}
	if err := s.repo.ReserveArtifact(ctx, a); err != nil {
		return nil, err
	}
	f, err := s.registry.OpenFile(ctx, "artifacts", a.ID, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		_ = s.repo.AbortArtifact(ctx, a.ID)
		return nil, err
	}
	return &artifactWriter{store: s, meta: a, file: f}, nil
}

type artifactWriter struct {
	store  *Artifacts
	meta   artifact.Metadata
	file   *os.File
	closed bool
}

func (w *artifactWriter) Write(b []byte) (int, error) {
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	if int64(len(b)) > ArtifactMaxBytes-w.meta.Size {
		return 0, fault.New(fault.TooLarge, "ARTIFACT_SIZE_LIMIT", "结果文件超过256MiB")
	}
	n, err := w.file.Write(b)
	w.meta.Size += int64(n)
	return n, err
}
func (w *artifactWriter) Complete(ctx context.Context, complete bool) (artifact.Metadata, error) {
	if w.closed {
		return w.meta, io.ErrClosedPipe
	}
	w.closed = true
	err := w.file.Sync()
	err = errors.Join(err, w.file.Close())
	if err != nil {
		return w.meta, err
	}
	w.meta.ExpiresAt = time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	w.meta.Complete = complete
	w.meta.State = "ready"
	return w.meta, w.store.repo.FinishArtifact(ctx, w.meta)
}
func (w *artifactWriter) Abort(ctx context.Context) error {
	if !w.closed {
		w.closed = true
		_ = w.file.Close()
	}
	if err := w.store.repo.AbortArtifact(ctx, w.meta.ID); err != nil {
		return err
	}
	err := os.Remove(w.store.file(w.meta.ID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
func (s *Artifacts) Metadata(ctx context.Context, id string) (artifact.Metadata, error) {
	return s.repo.Artifact(ctx, id)
}
func (s *Artifacts) List(ctx context.Context, job string) ([]artifact.Metadata, error) {
	return s.repo.JobArtifacts(ctx, job)
}
func usable(a artifact.Metadata) error {
	expiry, err := time.Parse(time.RFC3339Nano, a.ExpiresAt)
	if err != nil {
		return err
	}
	if a.State == "deleted" || !time.Now().Before(expiry) {
		return fault.New(fault.Gone, "RESOURCE_EXPIRED", "结果文件已删除或过期")
	}
	if a.State != "ready" {
		return fault.New(fault.Conflict, "ARTIFACT_NOT_READY", "结果文件尚未完成")
	}
	return nil
}
func (s *Artifacts) Open(ctx context.Context, id string) (artifact.Metadata, ports.ArtifactContent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.repo.Artifact(ctx, id)
	if err != nil {
		return a, nil, err
	}
	if err = usable(a); err != nil {
		return a, nil, err
	}
	f, err := s.registry.OpenFile(ctx, "artifacts", id, os.O_RDONLY, 0)
	if err != nil {
		return a, nil, fault.New(fault.Unavailable, "ARTIFACT_UNAVAILABLE", "结果文件不可用")
	}
	info, err := f.Stat()
	if err != nil || info.Size() != a.Size {
		_ = f.Close()
		return a, nil, fault.New(fault.Unavailable, "ARTIFACT_UNAVAILABLE", "结果文件长度不符")
	}
	s.readers[id]++
	return a, &artifactLease{File: f, store: s, id: id}, nil
}

type artifactLease struct {
	*os.File
	store *Artifacts
	id    string
	once  sync.Once
	err   error
}

func (r *artifactLease) Close() error {
	r.once.Do(func() {
		r.err = r.File.Close()
		r.store.mu.Lock()
		r.store.readers[r.id]--
		if r.store.readers[r.id] == 0 {
			delete(r.store.readers, r.id)
		}
		r.store.mu.Unlock()
	})
	return r.err
}
func (s *Artifacts) Delete(ctx context.Context, id string, m operation.Mutation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if replay, err := s.repo.ArtifactReplayed(ctx, m); replay || err != nil {
		return err
	}
	if s.readers[id] > 0 {
		return fault.New(fault.Conflict, "ARTIFACT_IN_USE", "结果文件正被读取")
	}
	// Tombstone first: if unlink fails or a crash occurs, cleanup can retry without
	// allowing new readers to access a partially deleted resource.
	if err := s.repo.RemoveArtifact(ctx, id, m); err != nil {
		return err
	}
	err := os.Remove(s.file(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
func (s *Artifacts) Cleanup(ctx context.Context) error {
	candidates, err := s.repo.ArtifactCandidates(ctx)
	if err != nil {
		return err
	}
	for _, a := range candidates {
		if err = s.Delete(ctx, a.ID, operation.Mutation{}); err != nil && fault.As(err).Code != "ARTIFACT_IN_USE" {
			return err
		}
	}
	return nil
}
