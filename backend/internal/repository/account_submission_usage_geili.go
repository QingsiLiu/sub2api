package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *accountSubmissionRepository) RevokeSubmissionView(ctx context.Context, id int64) error {
	return submissionViewWrite(r.db.ExecContext(ctx, `UPDATE account_submission_invites_geili SET view_revoked_at=COALESCE(view_revoked_at,clock_timestamp()) WHERE id=$1 AND submitted_at IS NOT NULL`, id))
}
func (r *accountSubmissionRepository) ResetSubmissionView(ctx context.Context, id int64, hash string) error {
	return submissionViewWrite(r.db.ExecContext(ctx, `UPDATE account_submission_invites_geili i SET token_hash=$2,view_revoked_at=NULL WHERE i.id=$1 AND i.submitted_at IS NOT NULL AND EXISTS (SELECT 1 FROM account_submission_invites_geili m JOIN accounts a ON a.id=m.account_id WHERE (m.id=i.id OR m.parent_invite_id=i.id) AND a.deleted_at IS NULL)`, id, hash))
}
func submissionViewWrite(result sql.Result, err error) error {
	if err != nil {
		return service.ErrSubmissionUnavailable
	}
	n, err := result.RowsAffected()
	if err != nil {
		return service.ErrSubmissionUnavailable
	}
	if n != 1 {
		return service.ErrSubmissionInactive
	}
	return nil
}

// Capability validation and all aggregations share one read-only snapshot. No arbitrary IDs,
// credentials or customer metadata are selected. An ongoing read may finish before revocation;
// every subsequent request revalidates the capability directly against PostgreSQL.
func (r *accountSubmissionRepository) SubmissionUsage(ctx context.Context, hash string, from, to time.Time) (*service.SubmissionUsage, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout='4s'`); err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	result := &service.SubmissionUsage{StartDate: from.Format("2006-01-02"), EndDate: to.AddDate(0, 0, -1).Format("2006-01-02"), Timezone: "Asia/Shanghai", Currency: "USD", UpdatedAt: time.Now().UTC(), Daily: []service.SubmissionUsageBucket{}, Models: []service.SubmissionUsageBucket{}}
	var id int64
	var rootID int64
	var submitted time.Time
	// Preserve the established projection; an active member keeps the root view usable
	// even when another member is soft-deleted. Root revocation covers the whole batch.
	err = tx.QueryRowContext(ctx, `SELECT a.id,a.name,a.platform,a.status='active' AND a.schedulable,i.submitted_at,i.id FROM account_submission_invites_geili i JOIN account_submission_invites_geili m ON m.id=i.id OR m.parent_invite_id=i.id JOIN accounts a ON a.id=m.account_id WHERE i.token_hash=$1 AND i.parent_invite_id IS NULL AND i.submitted_at IS NOT NULL AND i.view_revoked_at IS NULL AND a.deleted_at IS NULL ORDER BY m.id LIMIT 1`, hash).Scan(&id, &result.Name, &result.Platform, &result.Enabled, &submitted, &rootID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrSubmissionInactive
	}
	if err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	if from.Before(submitted) {
		from = submitted
	}
	members, err := tx.QueryContext(ctx, `SELECT a.id,m.account_ref::text,a.name,a.platform,a.status='active' AND a.schedulable FROM account_submission_invites_geili m JOIN accounts a ON a.id=m.account_id WHERE (m.id=$1 OR m.parent_invite_id=$1) AND a.deleted_at IS NULL ORDER BY m.id`, rootID)
	if err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	ids := []int64{}
	positions := map[int64]int{}
	result.Accounts = []service.SubmissionAccountUsage{}
	for members.Next() {
		var account service.SubmissionAccountUsage
		if members.Scan(&id, &account.AccountRef, &account.Name, &account.Platform, &account.Enabled) != nil {
			_ = members.Close()
			return nil, service.ErrSubmissionUnavailable
		}
		positions[id] = len(result.Accounts)
		ids = append(ids, id)
		result.Accounts = append(result.Accounts, account)
	}
	err = members.Err()
	closeErr := members.Close()
	if err != nil || closeErr != nil || len(ids) == 0 {
		return nil, service.ErrSubmissionUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT account_id,(created_at AT TIME ZONE 'Asia/Shanghai')::date::text,COALESCE(NULLIF(upstream_model,''),model),COUNT(*),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(cache_creation_tokens),0),COALESCE(SUM(cache_read_tokens),0),COALESCE(SUM(total_cost),0) FROM usage_logs WHERE account_id=ANY($1) AND created_at >= $2 AND created_at < $3 GROUP BY 1,2,3 ORDER BY 2,3,1`, pq.Array(ids), from, to)
	if err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	daily, models := map[string]int{}, map[string]int{}
	for rows.Next() {
		var b service.SubmissionUsageBucket
		if rows.Scan(&id, &b.Date, &b.Model, &b.Requests, &b.InputTokens, &b.OutputTokens, &b.CacheCreationTokens, &b.CacheReadTokens, &b.ReferenceCost) != nil {
			_ = rows.Close()
			return nil, service.ErrSubmissionUnavailable
		}
		addSubmissionUsage(&result.Summary, b)
		addSubmissionUsage(&result.Accounts[positions[id]].Summary, b)
		if _, ok := daily[b.Date]; !ok {
			daily[b.Date] = len(result.Daily)
			result.Daily = append(result.Daily, service.SubmissionUsageBucket{Date: b.Date})
		}
		addSubmissionUsage(&result.Daily[daily[b.Date]], b)
		if _, ok := models[b.Model]; !ok {
			models[b.Model] = len(result.Models)
			result.Models = append(result.Models, service.SubmissionUsageBucket{Model: b.Model})
		}
		addSubmissionUsage(&result.Models[models[b.Model]], b)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	if err = tx.Commit(); err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	return result, nil
}
func addSubmissionUsage(to *service.SubmissionUsageBucket, from service.SubmissionUsageBucket) {
	to.Requests += from.Requests
	to.InputTokens += from.InputTokens
	to.OutputTokens += from.OutputTokens
	to.CacheCreationTokens += from.CacheCreationTokens
	to.CacheReadTokens += from.CacheReadTokens
	to.ReferenceCost += from.ReferenceCost
}
