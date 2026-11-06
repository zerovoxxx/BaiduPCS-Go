package persistence

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/write"
	"time"
)

func (s *Store) SaveInspection(ctx context.Context, v write.Inspection) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM share_inspections WHERE expires_at<=?", time.Now().Unix()); err != nil {
		return err
	}
	var n, size int64
	if err = tx.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(length(payload)),0) FROM share_inspections").Scan(&n, &size); err != nil {
		return err
	}
	if n >= 128 {
		return fault.New(fault.RateLimited, "INSPECTION_QUOTA", "分享检查配额已满")
	}
	data, err := s.sealValue("inspection/"+v.ID, v)
	if err != nil {
		return err
	}
	if len(data) > 32<<20 || size+int64(len(data)) > 128<<20 {
		return fault.New(fault.TooLarge, "INSPECTION_LIMIT", "分享检查结果超过32MiB")
	}
	expiry, _ := time.Parse(time.RFC3339Nano, v.Access.ExpiresAt)
	_, err = tx.ExecContext(ctx, "INSERT INTO share_inspections VALUES(?,?,?)", v.ID, data, expiry.Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Inspection(ctx context.Context, id string) (write.Inspection, error) {
	var v write.Inspection
	var data []byte
	var expiry int64
	err := s.db.QueryRowContext(ctx, "SELECT payload,expires_at FROM share_inspections WHERE inspection_id=?", id).Scan(&data, &expiry)
	if errors.Is(err, sql.ErrNoRows) || err == nil && expiry <= time.Now().Unix() {
		return v, fault.New(fault.Gone, "SHARE_INSPECTION_EXPIRED", "分享检查已过期")
	}
	if err != nil {
		return v, err
	}
	return v, s.openValue("inspection/"+id, data, &v)
}
func (s *Store) BeginBusinessMutation(ctx context.Context, m operation.Mutation) (json.RawMessage, bool, error) {
	if !m.HasKey {
		return nil, false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var hash, data []byte
	err = tx.QueryRowContext(ctx, "SELECT request_hash,payload FROM business_mutations WHERE principal=? AND operation=? AND key_hash=?", m.Principal, m.Operation, m.KeyHash[:]).Scan(&hash, &data)
	if err == nil {
		if subtle.ConstantTimeCompare(hash, m.RequestHash[:]) != 1 {
			return nil, false, fault.New(fault.Conflict, "IDEMPOTENCY_KEY_REUSED", "幂等Key已用于不同请求")
		}
		if len(data) == 0 {
			return nil, false, fault.New(fault.Conflict, "REMOTE_RESULT_UNKNOWN", "此前分享提交结果未知，请先查询核验，禁止重放")
		}
		var raw json.RawMessage
		if err = s.openValue("mutation/"+m.Principal+"/"+m.Operation+"/"+string(m.KeyHash[:]), data, &raw); err != nil {
			return nil, false, err
		}
		return raw, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO business_mutations VALUES(?,?,?,?,NULL,?)", m.Principal, m.Operation, m.KeyHash[:], m.RequestHash[:], time.Now().Add(24*time.Hour).Unix())
	if err != nil {
		return nil, false, err
	}
	return nil, false, tx.Commit()
}
func (s *Store) FinishBusinessMutation(ctx context.Context, m operation.Mutation, raw json.RawMessage) error {
	if !m.HasKey {
		return nil
	}
	data, err := s.sealValue("mutation/"+m.Principal+"/"+m.Operation+"/"+string(m.KeyHash[:]), raw)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE business_mutations SET payload=? WHERE principal=? AND operation=? AND key_hash=? AND request_hash=?", data, m.Principal, m.Operation, m.KeyHash[:], m.RequestHash[:])
	if err != nil {
		return err
	}
	return oneRow(res)
}

func (s *Store) UpdateInspection(ctx context.Context, v write.Inspection) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old []byte
	var expiry int64
	if err = tx.QueryRowContext(ctx, "SELECT payload,expires_at FROM share_inspections WHERE inspection_id=?", v.ID).Scan(&old, &expiry); err != nil {
		return err
	}
	if expiry <= time.Now().Unix() {
		return fault.New(fault.Gone, "SHARE_INSPECTION_EXPIRED", "分享检查已过期")
	}
	var current write.Inspection
	if err = s.openValue("inspection/"+v.ID, old, &current); err != nil {
		return err
	}
	if current.Access.Owner != v.Access.Owner || current.Access.AccountID != v.Access.AccountID || current.Access.CredentialRevision != v.Access.CredentialRevision {
		return fault.New(fault.Conflict, "SHARE_INSPECTION_MISMATCH", "分享检查归属已变化")
	}
	seen := map[string]bool{}
	for _, entry := range current.Access.Entries {
		seen[entry.ID] = true
	}
	for _, entry := range v.Access.Entries {
		if !seen[entry.ID] {
			current.Access.Entries = append(current.Access.Entries, entry)
			seen[entry.ID] = true
		}
	}
	if len(current.Access.Entries) > 10000 {
		return fault.New(fault.TooLarge, "INSPECTION_LIMIT", "分享检查最多10000项")
	}
	data, err := s.sealValue("inspection/"+v.ID, current)
	if err != nil {
		return err
	}
	if len(data) > 32<<20 {
		return fault.New(fault.TooLarge, "INSPECTION_LIMIT", "分享检查结果超过32MiB")
	}
	var size int64
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(sum(length(payload)),0) FROM share_inspections").Scan(&size); err != nil {
		return err
	}
	if size-int64(len(old))+int64(len(data)) > 128<<20 {
		return fault.New(fault.RateLimited, "INSPECTION_QUOTA", "分享检查总量超过128MiB")
	}
	res, err := tx.ExecContext(ctx, "UPDATE share_inspections SET payload=? WHERE inspection_id=?", data, v.ID)
	if err != nil {
		return err
	}
	if err = oneRow(res); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) CleanupBusiness(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM share_inspections WHERE expires_at<=?", time.Now().Unix())
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "DELETE FROM business_mutations WHERE expires_at<=? AND payload IS NOT NULL", time.Now().Unix())
	return err
}
