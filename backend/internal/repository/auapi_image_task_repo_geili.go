package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type auapiImageTaskRepository struct{ db *sql.DB }

func NewAUAPIImageTaskRepository(db *sql.DB) service.AUAPIImageTaskRepository {
	return &auapiImageTaskRepository{db: db}
}
func (r *auapiImageTaskRepository) Create(ctx context.Context, t *service.AUAPIImageTaskRecord) (*service.AUAPIImageTaskRecord, bool, error) {
	if r == nil || r.db == nil || t == nil {
		return nil, false, errors.New("auapi image repository unavailable")
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return nil, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `INSERT INTO auapi_image_tasks(task_id,user_id,api_key_id,account_id,idempotency_key,request_hash,phase,snapshot,next_poll_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10) ON CONFLICT(user_id,api_key_id,idempotency_key) DO NOTHING`, t.TaskID, t.UserID, t.APIKeyID, t.AccountID, t.IdempotencyKey, t.RequestHash, t.Phase, string(raw), t.NextPollAt, t.CreatedAt)
	if err != nil {
		return nil, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if n == 0 {
		existing, err := scanAUAPIImageTask(tx.QueryRowContext(ctx, `SELECT task_id,phase,snapshot,next_poll_at,created_at FROM auapi_image_tasks WHERE user_id=$1 AND api_key_id=$2 AND idempotency_key=$3`, t.UserID, t.APIKeyID, t.IdempotencyKey))
		if err != nil {
			return nil, false, err
		}
		if existing.RequestHash != t.RequestHash {
			return nil, false, service.ErrAUAPIImageConflict
		}
		return existing, false, nil
	}
	// Task acceptance, unique idempotency ownership and its balance hold share
	// one commit. A loser or crash cannot leave an orphaned second hold.
	if t.SubscriptionID == nil && t.HoldAmount > 0 {
		cmd := &service.BatchImageBalanceHoldCommand{
			RequestID: "auapi_image_hold:" + t.TaskID, HoldRequestID: "auapi_image_hold:" + t.TaskID,
			APIKeyID: t.APIKeyID, UserID: t.UserID, BatchID: t.TaskID,
			RequestFingerprint: t.RequestHash, RequestPayloadHash: t.RequestHash, HoldAmount: t.HoldAmount,
		}
		if _, err := (&usageBillingRepository{db: r.db}).applyBatchImageBalanceHoldTx(ctx, tx, cmd, reserveUsageBillingBatchImageBalance, "reserve"); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return t, true, nil
}
func (r *auapiImageTaskRepository) scan(ctx context.Context, q string, args ...any) (*service.AUAPIImageTaskRecord, error) {
	return scanAUAPIImageTask(r.db.QueryRowContext(ctx, q, args...))
}

func scanAUAPIImageTask(row interface{ Scan(...any) error }) (*service.AUAPIImageTaskRecord, error) {
	var id, phase string
	var raw []byte
	var next, created time.Time
	err := row.Scan(&id, &phase, &raw, &next, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	var t service.AUAPIImageTaskRecord
	if err = json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	t.TaskID = id
	t.Phase = phase
	t.NextPollAt = next
	t.CreatedAt = created
	return &t, nil
}
func (r *auapiImageTaskRepository) Get(ctx context.Context, id string) (*service.AUAPIImageTaskRecord, error) {
	return r.scan(ctx, `SELECT task_id,phase,snapshot,next_poll_at,created_at FROM auapi_image_tasks WHERE task_id=$1`, id)
}
func (r *auapiImageTaskRepository) GetByIdempotency(ctx context.Context, u, k int64, key string) (*service.AUAPIImageTaskRecord, error) {
	return r.scan(ctx, `SELECT task_id,phase,snapshot,next_poll_at,created_at FROM auapi_image_tasks WHERE user_id=$1 AND api_key_id=$2 AND idempotency_key=$3`, u, k, key)
}
func (r *auapiImageTaskRepository) Claim(ctx context.Context, id string, lease time.Duration) (*service.AUAPIImageTaskRecord, error) {
	tok := uuid.NewString()
	until := time.Now().Add(lease)
	res, e := r.db.ExecContext(ctx, `UPDATE auapi_image_tasks SET lease_until=$2,lease_token=$3 WHERE task_id=$1 AND phase<>'done' AND (lease_until IS NULL OR lease_until<NOW())`, id, until, tok)
	if e != nil {
		return nil, e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, nil
	}
	t, e := r.Get(ctx, id)
	if t != nil {
		t.LeaseToken = tok
	}
	return t, e
}
func (r *auapiImageTaskRepository) Save(ctx context.Context, t *service.AUAPIImageTaskRecord) error {
	raw, e := json.Marshal(t)
	if e != nil {
		return e
	}
	if t.LeaseToken == "" {
		return service.ErrAUAPIImageLease
	}
	res, e := r.db.ExecContext(ctx, `UPDATE auapi_image_tasks SET phase=$2,snapshot=$3::jsonb,next_poll_at=$4,updated_at=NOW() WHERE task_id=$1 AND lease_token=$5 AND lease_until>NOW()`, t.TaskID, t.Phase, string(raw), t.NextPollAt, t.LeaseToken)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return service.ErrAUAPIImageLease
	}
	return nil
}
func (r *auapiImageTaskRepository) Settle(ctx context.Context, t *service.AUAPIImageTaskRecord) error {
	return r.Save(ctx, t)
}
func (r *auapiImageTaskRepository) ReleaseLease(ctx context.Context, t *service.AUAPIImageTaskRecord) error {
	_, e := r.db.ExecContext(ctx, `UPDATE auapi_image_tasks SET lease_until=NULL WHERE task_id=$1 AND lease_token=$2`, t.TaskID, t.LeaseToken)
	return e
}
func (r *auapiImageTaskRepository) Due(ctx context.Context, limit int) ([]string, error) {
	rows, e := r.db.QueryContext(ctx, `SELECT task_id FROM auapi_image_tasks WHERE phase<>'done' AND next_poll_at<=NOW() AND (lease_until IS NULL OR lease_until<NOW()) ORDER BY next_poll_at LIMIT $1`, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
