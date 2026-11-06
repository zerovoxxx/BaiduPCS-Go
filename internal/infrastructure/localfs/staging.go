package localfs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/staging"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
)

type Staging struct {
	registry *Registry
	repo     ports.StagingRepository
	copyFile func(io.Writer, io.Reader) (int64, error)
}

func NewStaging(directory string, repo ports.StagingRepository) (*Staging, error) {
	dir := filepath.Join(directory, "staging")
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("invalid staging directory")
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	return &Staging{registry: &Registry{roots: map[string]Root{"staging": {ID: "staging", Path: dir, Read: true, Write: true}}}, repo: repo}, nil
}
func (s *Staging) Registry() *Registry { return s.registry }
func (s *Staging) Metadata(ctx context.Context, id string) (staging.File, error) {
	f, err := s.repo.Stage(ctx, id)
	if err != nil {
		return f, err
	}
	expiry, _ := time.Parse(time.RFC3339Nano, f.ExpiresAt)
	if f.State == "deleted" || f.References == 0 && !time.Now().Before(expiry) {
		return f, fault.New(fault.Gone, "RESOURCE_EXPIRED", "暂存文件已过期或删除")
	}
	return f, nil
}
func (s *Staging) Receive(ctx context.Context, owner, filename, key string, reader io.Reader, nameAfter ...func() (string, error)) (staging.File, error) {
	var f staging.File
	if !utf8.ValidString(filename) || utf8.RuneCountInString(filename) > 1024 || filename == "" || strings.ContainsAny(filename, "\x00\r\n/\\") {
		return f, fault.New(fault.Invalid, "INVALID_REQUEST", "暂存文件名无效")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return f, err
	}
	f = staging.File{ID: "staged_" + hex.EncodeToString(nonce[:]), Owner: owner, Filename: filename, State: "writing", ExpiresAt: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)}
	if err := s.repo.ReserveStage(ctx, f); err != nil {
		return f, err
	}
	file, err := s.registry.OpenFile(ctx, "staging", f.ID, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return f, err
	}
	keep := false
	defer func() {
		file.Close()
		if !keep {
			_ = s.repo.DeleteStage(context.Background(), f.ID, operation.Mutation{})
			_ = s.registry.RemoveFile(context.Background(), "staging", f.ID)
		}
	}()
	h := sha256.New()
	copyFile := s.copyFile
	if copyFile == nil {
		copyFile = io.Copy
	}
	f.Size, err = copyFile(io.MultiWriter(file, h), io.LimitReader(reader, (256<<20)+1))
	if err != nil {
		return f, err
	}
	if f.Size > 256<<20 {
		return f, fault.New(fault.TooLarge, "STAGING_SIZE_LIMIT", "暂存单文件超过256MiB")
	}
	if err = ctx.Err(); err != nil {
		return f, err
	}
	if err = file.Sync(); err != nil {
		return f, err
	}
	if len(nameAfter) > 0 {
		f.Filename, err = nameAfter[0]()
		if err != nil {
			return f, err
		}
		if f.Filename == "" || !utf8.ValidString(f.Filename) || utf8.RuneCountInString(f.Filename) > 1024 || strings.ContainsAny(f.Filename, "\x00\r\n/\\") {
			return f, fault.New(fault.Invalid, "INVALID_REQUEST", "暂存文件名无效")
		}
	}
	f.State = "ready"
	m := operation.New(owner, "UPL02", key, []string{f.Filename, hex.EncodeToString(h.Sum(nil))})
	result, err := s.repo.FinishStage(ctx, f, m)
	if err != nil {
		return f, err
	}
	keep = result.ID == f.ID
	return result, nil
}
func (s *Staging) Delete(ctx context.Context, id string, m operation.Mutation) error {
	if err := s.repo.DeleteStage(ctx, id, m); err != nil {
		return err
	}
	return s.registry.RemoveFile(ctx, "staging", id)
}
func (s *Staging) Cleanup(ctx context.Context) error {
	files, err := s.repo.StageCandidates(ctx)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err = s.Delete(ctx, f.ID, operation.Mutation{}); err != nil {
			return err
		}
	}
	return nil
}
