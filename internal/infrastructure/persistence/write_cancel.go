package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/write"
)

// Queued cancellations publish their per-item no-effect outcomes in the same
// transaction as the terminal state, control receipt and final event.
func (s *Store) cancelWriteItems(ctx context.Context, tx *sql.Tx, r *transfer.Record) error {
	var plan write.Plan
	if err := json.Unmarshal(r.Plan, &plan); err != nil {
		return err
	}
	result := r.ScanResult
	if result == nil {
		result = &transfer.ScanResult{Complete: false, ArtifactIDs: []string{}}
	}
	result.Counts.Total = uint64(len(plan.Units))
	for i, u := range plan.Units {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM job_items WHERE job_id=? AND item_index=?", r.ID, i).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		input := u.From
		if u.ID != "" {
			input = u.ID
		}
		if u.SourceURL != "" {
			input = fmt.Sprintf("source_%d", i)
		}
		target := u.To
		if target == "" {
			target = u.From
		}
		item := transfer.Item{ID: fmt.Sprintf("item_%d", i), Index: uint64(i), Input: input, Path: target, State: "cancelled", RemoteEffect: "none"}
		data, err := s.sealValue(fmt.Sprintf("item/%s/%d", r.ID, i), item)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO job_items VALUES(?,?,?,?)", r.ID, i, data, item.State); err != nil {
			return err
		}
		result.Counts.Processed++
		result.Counts.Cancelled++
		if len(result.MutationItems) < 1000 {
			result.MutationItems = append(result.MutationItems, item)
		}
		if err = s.appendEvent(ctx, tx, r.ID, "item.completed", &item); err != nil {
			return err
		}
	}
	r.ScanResult = result
	return nil
}
