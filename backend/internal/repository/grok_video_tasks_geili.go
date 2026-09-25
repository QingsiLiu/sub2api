package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

var _ service.GrokVideoTaskRepository = (*usageBillingRepository)(nil)

func (r *usageBillingRepository) StoreGrokVideoTask(ctx context.Context, s *service.GrokVideoTaskSnapshot) error {
	if s == nil || s.Version != 1 || s.TaskID == "" || s.UserID <= 0 || s.APIKeyID <= 0 || s.AccountID <= 0 || s.FinancialRequestID != service.StableGrokVideoBillingRequestID(s.TaskID) {
		return errors.New("invalid video task identity")
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return errors.New("video snapshot too large")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var same bool
	err = tx.QueryRowContext(ctx, `INSERT INTO grok_video_tasks_geili(task_id,api_key_id,user_id,account_id,financial_request_id,snapshot) VALUES($1,$2,$3,$4,$5,$6::jsonb)
 ON CONFLICT(task_id,api_key_id) DO UPDATE SET task_id=EXCLUDED.task_id
 RETURNING user_id=$3 AND account_id=$4 AND financial_request_id=$5 AND snapshot=$6::jsonb`, s.TaskID, s.APIKeyID, s.UserID, s.AccountID, s.FinancialRequestID, string(raw)).Scan(&same)
	if err != nil {
		return err
	}
	if !same {
		return service.ErrUsageBillingRequestConflict
	}
	if s.Template.SubscriptionID != nil && s.Template.SubscriptionAdmissionKey != "" {
		var admission string
		err = tx.QueryRowContext(ctx, `INSERT INTO subscription_media_tasks(task_id,api_key_id,user_id,subscription_id,admission_key) VALUES($1,$2,$3,$4,$5)
     ON CONFLICT(task_id,api_key_id) DO UPDATE SET task_id=EXCLUDED.task_id
     WHERE subscription_media_tasks.admission_key=EXCLUDED.admission_key AND subscription_media_tasks.user_id=EXCLUDED.user_id AND subscription_media_tasks.subscription_id=EXCLUDED.subscription_id
     RETURNING admission_key`, s.TaskID, s.APIKeyID, s.UserID, *s.Template.SubscriptionID, s.Template.SubscriptionAdmissionKey).Scan(&admission)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (r *usageBillingRepository) LoadGrokVideoTask(ctx context.Context, id string, user, key int64) (*service.GrokVideoTaskSnapshot, error) {
	var raw []byte
	err := r.db.QueryRowContext(ctx, `SELECT snapshot FROM grok_video_tasks_geili WHERE task_id=$1 AND user_id=$2 AND api_key_id=$3`, id, user, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s service.GrokVideoTaskSnapshot
	err = json.Unmarshal(raw, &s)
	return &s, err
}
func (r *usageBillingRepository) LeaseGrokVideoTasks(ctx context.Context, limit int) ([]service.GrokVideoTaskLease, error) {
	if limit < 1 {
		limit = 8
	}
	if limit > 32 {
		limit = 32
	}
	token := uuid.NewString()
	rows, err := r.db.QueryContext(ctx, `WITH selected AS(SELECT task_id,api_key_id FROM grok_video_tasks_geili WHERE state='pending' AND next_poll_at<=NOW() AND(lease_until IS NULL OR lease_until<NOW()) ORDER BY next_poll_at,task_id FOR UPDATE SKIP LOCKED LIMIT $1)
 UPDATE grok_video_tasks_geili t SET lease_token=$2,lease_until=NOW()+INTERVAL '90 seconds',attempts=attempts+1 FROM selected s WHERE t.task_id=s.task_id AND t.api_key_id=s.api_key_id RETURNING t.snapshot`, limit, token)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []service.GrokVideoTaskLease{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var s service.GrokVideoTaskSnapshot
		if err = json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		out = append(out, service.GrokVideoTaskLease{Snapshot: s, Token: token})
	}
	return out, rows.Err()
}
func (r *usageBillingRepository) RetryGrokVideoTask(ctx context.Context, s *service.GrokVideoTaskSnapshot, token, reason string) error {
	if s == nil {
		return errors.New("nil video task")
	}
	if reason != "pending" {
		reason = "retryable_poll_or_prepare_error"
	}
	_, err := r.db.ExecContext(ctx, `UPDATE grok_video_tasks_geili SET lease_token=NULL,lease_until=NULL,next_poll_at=NOW()+INTERVAL '10 seconds',last_error=$4,updated_at=NOW() WHERE task_id=$1 AND api_key_id=$2 AND state='pending' AND lease_token=$3`, s.TaskID, s.APIKeyID, token, reason)
	return err
}
func (r *usageBillingRepository) ObserveGrokVideoTask(ctx context.Context, s *service.GrokVideoTaskSnapshot, token string, o service.GrokVideoObservation, cmd *service.UsageBillingCommand, log *service.UsageLog) error {
	if s == nil {
		return errors.New("nil video task")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	var raw, priorObserved []byte
	var lease sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT state,snapshot,lease_token,observed FROM grok_video_tasks_geili WHERE task_id=$1 AND api_key_id=$2 AND user_id=$3 FOR UPDATE`, s.TaskID, s.APIKeyID, s.UserID).Scan(&state, &raw, &lease, &priorObserved)
	if err != nil {
		return err
	}
	// Full snapshot equality fences callers that lost/repriced the create contract.
	provided, err := json.Marshal(s)
	if err != nil {
		return err
	}
	var equal bool
	if err = tx.QueryRowContext(ctx, `SELECT $1::jsonb=$2::jsonb`, string(raw), string(provided)).Scan(&equal); err != nil {
		return err
	}
	if !equal {
		return service.ErrUsageBillingRequestConflict
	}
	if state != "pending" {
		var prior service.GrokVideoObservation
		if err = json.Unmarshal(priorObserved, &prior); err != nil {
			return err
		}
		if prior.Status != o.Status {
			return service.ErrUsageBillingRequestConflict
		}
		if state == "prepared" {
			old, _, e := service.BuildGrokVideoSettlement(s, prior)
			if e != nil {
				return e
			}
			current, _, e := service.BuildGrokVideoSettlement(s, o)
			if e != nil {
				return e
			}
			if !sameSettlementFinancialCommand(old, current) {
				return service.ErrUsageBillingRequestConflict
			}
		}
		return tx.Commit()
	}
	if token != "" && lease.String != token {
		return errors.New("stale video task lease")
	}
	status := strings.ToLower(o.Status)
	next := "failed"
	if status == "done" {
		if cmd == nil || log == nil || cmd.RequestID != s.FinancialRequestID || cmd.UserID != s.UserID || cmd.APIKeyID != s.APIKeyID || cmd.AccountID != s.AccountID {
			return service.ErrUsageBillingRequestConflict
		}
		expected, _, e := service.BuildGrokVideoSettlement(s, o)
		if e != nil {
			return e
		}
		if !sameSettlementFinancialCommand(expected, cmd) {
			return service.ErrUsageBillingRequestConflict
		}
		if _, err = r.prepareSettlementTx(ctx, tx, cmd, log); err != nil {
			return err
		}
		next = "prepared"
	} else if status == "failed" || status == "expired" || status == "cancelled" {
		// Terminal zero outcome still resolves the original admission through the
		// existing atomic settlement worker, without deducting funds or quota.
		failure := s.Template
		failure.RequestID = s.FinancialRequestID
		failure.RequestFingerprint = ""
		failure.TerminalFailure = true
		failure.CompletedAt = o.CompletedAt
		failure.UsageDetail = nil
		failure.BalanceCost = 0
		failure.SubscriptionCost = 0
		failure.APIKeyQuotaCost = 0
		failure.APIKeyRateLimitCost = 0
		failure.AccountQuotaCost = 0
		failure.PlatformQuotaCost = 0
		if failure.CompletedAt.IsZero() {
			failure.CompletedAt = time.Now().UTC()
		}
		failure.Normalize()
		if _, err = r.prepareSettlementTx(ctx, tx, &failure, nil); err != nil {
			return err
		}
	} else {
		return errors.New("video terminal status required")
	}
	observed, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE grok_video_tasks_geili SET state=$3,observed=$4::jsonb,lease_token=NULL,lease_until=NULL,last_error=NULL,updated_at=NOW() WHERE task_id=$1 AND api_key_id=$2`, s.TaskID, s.APIKeyID, next, string(observed))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *usageBillingRepository) GrokVideoTaskHealth(ctx context.Context) (service.GrokVideoTaskHealth, error) {
	var h service.GrokVideoTaskHealth
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FILTER(WHERE state='pending'),COUNT(*) FILTER(WHERE state='prepared'),COUNT(*) FILTER(WHERE state='failed'),COUNT(*) FILTER(WHERE state='pending' AND last_error IS NOT NULL AND last_error<>'pending'),COALESCE(MAX(EXTRACT(EPOCH FROM NOW()-created_at)) FILTER(WHERE state='pending'),0) FROM grok_video_tasks_geili`).Scan(&h.Pending, &h.Prepared, &h.Failed, &h.Retrying, &h.OldestPendingSeconds)
	return h, err
}
