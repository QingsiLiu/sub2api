package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *accountSubmissionRepository) PrepareSubmissionProxy(ctx context.Context, hash string, connection service.SubmissionConnectionInput) (*int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	i, err := scanSubmission(tx.QueryRowContext(ctx, `SELECT `+submissionColumns+` FROM account_submission_invites_geili WHERE token_hash=$1 FOR UPDATE`, hash))
	if err != nil {
		return nil, err
	}
	var usable bool
	if tx.QueryRowContext(ctx, `SELECT submitted_at IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp() FROM account_submission_invites_geili WHERE id=$1`, i.ID).Scan(&usable) != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	if !usable {
		return nil, service.ErrSubmissionInactive
	}
	var old *int64
	_ = tx.QueryRowContext(ctx, `SELECT proxy_id FROM account_submission_connections_geili WHERE invite_id=$1`, i.ID).Scan(&old)
	if _, err = tx.ExecContext(ctx, `INSERT INTO account_submission_connections_geili(invite_id) VALUES($1) ON CONFLICT DO NOTHING`, i.ID); err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	id, err := writeSubmissionProxy(ctx, tx, i, connection)
	if err != nil {
		return nil, err
	}
	if old != nil && (id == nil || *old != *id) {
		if _, err = tx.ExecContext(ctx, `DELETE FROM proxies WHERE id=$1 AND name LIKE '[external] %' AND NOT EXISTS(SELECT 1 FROM accounts WHERE proxy_id=$1) AND NOT EXISTS(SELECT 1 FROM proxies WHERE backup_proxy_id=$1)`, *old); err != nil {
			return nil, service.ErrSubmissionUnavailable
		}
	}
	if tx.Commit() != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	return id, nil
}
