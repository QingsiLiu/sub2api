package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

var _ service.AccountRuntimeRepositoryGeili = (*accountRepository)(nil)

func (r *accountRepository) hydrateAccountRuntimePointersGeili(ctx context.Context, accounts []*service.Account) {
	if !service.AccountRuntimePresentationAllowedGeili(ctx) || isAccountModelCatalogContextGeili(ctx) {
		return
	}
	if r.sql == nil {
		return
	} // Legacy read-only Ent projections have no raw executor.
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if account != nil {
			ids = append(ids, account.ID)
		}
	}
	states, err := r.LoadAccountRuntimeBatchGeili(ctx, ids)
	if err != nil {
		// Presentation is best effort. Manual controls persist the scheduling
		// stop in accounts; forwarding does not load this display metadata.
		logger.LegacyPrintf("repository.account", "runtime display hydration failed: %v", err)
		return
	}
	for _, account := range accounts {
		if account != nil {
			account.RuntimeState = states[account.ID]
		}
	}
}

const accountRuntimeColumnsGeili = `account_id,version,balance_blocked,balance_reason,balance_blocked_at,stop_reason,protection_enabled,model_states,last_probe,next_check_at,updated_at`
const accountRuntimeGetSQLGeili = `SELECT ` + accountRuntimeColumnsGeili + ` FROM account_runtime_geili WHERE account_id=$1`

type accountRuntimeRowGeili struct {
	ctx   context.Context
	exec  sqlExecutor
	query string
	args  []any
	stmt  *sql.Stmt
	err   error
}

func accountRuntimeQueryRowGeili(ctx context.Context, exec sqlExecutor, query string, args ...any) accountRuntimeRowGeili {
	return accountRuntimeRowGeili{ctx: ctx, exec: exec, query: query, args: args}
}

func (r accountRuntimeRowGeili) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	var rows *sql.Rows
	var err error
	if r.stmt != nil {
		rows, err = r.stmt.QueryContext(r.ctx, r.args...)
	} else {
		rows, err = r.exec.QueryContext(r.ctx, r.query, r.args...)
	}
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	return rows.Scan(dest...)
}

func scanAccountRuntimeGeili(scan func(...any) error) (*service.AccountRuntimeStateGeili, error) {
	state := &service.AccountRuntimeStateGeili{}
	var models, probe []byte
	if err := scan(&state.AccountID, &state.Version, &state.BalanceBlocked, &state.BalanceReason, &state.BalanceBlockedAt, &state.StopReason, &state.ProtectionEnabled, &models, &probe, &state.NextCheckAt, &state.UpdatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(models, &state.Models); err != nil {
		return nil, err
	}
	if state.Models == nil {
		state.Models = make(map[string]service.AccountModelHealthGeili)
	}
	if len(probe) > 0 && string(probe) != "null" {
		if err := json.Unmarshal(probe, &state.LastProbe); err != nil {
			return nil, err
		}
	}
	return state, nil
}

func (r *accountRepository) GetAccountRuntimeGeili(ctx context.Context, id int64) (*service.AccountRuntimeStateGeili, error) {
	state, err := scanAccountRuntimeGeili(r.accountRuntimeReadRowGeili(ctx, accountRuntimeGetSQLGeili, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return &service.AccountRuntimeStateGeili{AccountID: id, Models: map[string]service.AccountModelHealthGeili{}}, nil
	}
	return state, err
}

func (r *accountRepository) LoadAccountRuntimeBatchGeili(ctx context.Context, ids []int64) (map[int64]*service.AccountRuntimeStateGeili, error) {
	out := make(map[int64]*service.AccountRuntimeStateGeili)
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT `+accountRuntimeColumnsGeili+` FROM account_runtime_geili WHERE account_id=ANY($1)`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		state, err := scanAccountRuntimeGeili(rows.Scan)
		if err != nil {
			return nil, err
		}
		out[state.AccountID] = state
	}
	return out, rows.Err()
}

// Every runtime mutation locks the account first; the legacy account guard and
// activation share that lock, preventing a late probe grant from racing debt.
func (r *accountRepository) withAccountRuntimeGeili(ctx context.Context, id int64, fn func(*accountRepository, *service.AccountRuntimeStateGeili) error) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	exec := tx.Client()
	view := *r
	view.client = exec
	view.sql = exec
	var lockedID int64
	if err := accountRuntimeQueryRowGeili(ctx, exec, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&lockedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrAccountNotFound
		}
		return err
	}
	if _, err := exec.ExecContext(ctx, `INSERT INTO account_runtime_geili(account_id) VALUES($1) ON CONFLICT(account_id) DO NOTHING`, id); err != nil {
		return err
	}
	state, err := view.GetAccountRuntimeGeili(ctx, id)
	if err != nil {
		return err
	}
	if err := fn(&view, state); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.syncSchedulerAccountSnapshot(ctx, id)
	return nil
}

func persistAccountRuntimeGeili(ctx context.Context, r *accountRepository, state *service.AccountRuntimeStateGeili, bump bool) error {
	models, err := json.Marshal(state.Models)
	if err != nil {
		return err
	}
	probe, err := json.Marshal(state.LastProbe)
	if err != nil {
		return err
	}
	if bump {
		state.Version++
	}
	_, err = r.sql.ExecContext(ctx, `UPDATE account_runtime_geili SET version=$2,balance_blocked=$3,balance_reason=$4,balance_blocked_at=$5,stop_reason=$6,protection_enabled=$7,model_states=$8::jsonb,last_probe=$9::jsonb,next_check_at=$10,updated_at=NOW() WHERE account_id=$1`, state.AccountID, state.Version, state.BalanceBlocked, state.BalanceReason, state.BalanceBlockedAt, state.StopReason, state.ProtectionEnabled, string(models), string(probe), state.NextCheckAt)
	if err != nil {
		return err
	}
	return enqueueSchedulerOutbox(ctx, r.sql, service.SchedulerOutboxEventAccountChanged, &state.AccountID, nil, nil)
}

func stopRuntimeAccountGeili(ctx context.Context, r *accountRepository, id int64, reason string) error {
	_, err := r.sql.ExecContext(ctx, `UPDATE accounts SET status='error',schedulable=FALSE,error_message=$2,updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, id, reason)
	return err
}

func (r *accountRepository) MarkAccountBalanceBlockedGeili(ctx context.Context, id int64, reason string) error {
	return r.withAccountRuntimeGeili(ctx, id, func(view *accountRepository, state *service.AccountRuntimeStateGeili) error {
		now := time.Now().UTC()
		state.BalanceBlocked = true
		state.BalanceReason = strings.TrimSpace(reason)
		if state.BalanceReason == "" {
			state.BalanceReason = "insufficient_balance"
		}
		state.BalanceBlockedAt = &now
		state.StopReason = "balance"
		if err := persistAccountRuntimeGeili(ctx, view, state, true); err != nil {
			return err
		}
		return stopRuntimeAccountGeili(ctx, view, id, state.BalanceReason)
	})
}

func (r *accountRepository) RecordModelFailureGeili(ctx context.Context, id int64, model, reason string, nextProbeAt time.Time) error {
	return r.withAccountRuntimeGeili(ctx, id, func(view *accountRepository, state *service.AccountRuntimeStateGeili) error {
		now := time.Now().UTC()
		model = strings.TrimSpace(model)
		health := state.Models[model]
		if health.FailureWindowStartedAt == nil || now.Sub(*health.FailureWindowStartedAt) > 10*time.Minute {
			health.FailureCount = 0
			health.FailureWindowStartedAt = &now
		}
		health.FailureCount++
		health.LastFailureAt = &now
		health.LastError = reason
		health.Health = "unhealthy"
		if model == "" || health.FailureCount >= 3 {
			health.Blocked = true
			if nextProbeAt.IsZero() {
				nextProbeAt = now.Add(time.Minute)
			}
			nextProbeAt = nextProbeAt.UTC()
			health.NextCheckAt = &nextProbeAt
			health.NextProbeAt = &nextProbeAt
		}
		state.Models[model] = health
		if model == "" {
			state.StopReason = "temporary_failure"
			state.NextCheckAt = health.NextCheckAt
		}
		if err := persistAccountRuntimeGeili(ctx, view, state, true); err != nil {
			return err
		}
		if model == "" {
			return stopRuntimeAccountGeili(ctx, view, id, reason)
		}
		return nil
	})
}

func (r *accountRepository) ResetAccountModelSuccessGeili(ctx context.Context, id int64, model string) error {
	// The common healthy path is a single read and creates no runtime row,
	// outbox event or cache write. Re-read under the account lock before clearing.
	current, err := r.GetAccountRuntimeGeili(ctx, id)
	if err != nil {
		return err
	}
	if current == nil || current.Models[model].FailureCount == 0 {
		return nil
	}
	return r.withAccountRuntimeGeili(ctx, id, func(view *accountRepository, state *service.AccountRuntimeStateGeili) error {
		health, ok := state.Models[model]
		if !ok || health.FailureCount == 0 {
			return nil
		}
		now := time.Now().UTC()
		health.FailureCount = 0
		health.FailureWindowStartedAt = nil
		health.LastSuccessAt = &now
		if !health.Blocked {
			health.Health = "healthy"
			health.LastError = ""
		}
		state.Models[model] = health
		return persistAccountRuntimeGeili(ctx, view, state, true)
	})
}

func (r *accountRepository) RecordAccountProbeGeili(ctx context.Context, result service.AccountProbeResultGeili) error {
	if result.CheckedAt.IsZero() {
		result.CheckedAt = time.Now().UTC()
	}
	return r.withAccountRuntimeGeili(ctx, result.Snapshot.AccountID, func(view *accountRepository, state *service.AccountRuntimeStateGeili) error {
		health := state.Models[result.Model]
		if result.Success && result.Complete {
			health.ProbeFailureCount = 0
		} else {
			if health.ProbeFailureCount < 0 {
				health.ProbeFailureCount = 0
			}
			if health.ProbeFailureCount > 2 {
				health.ProbeFailureCount = 2
			}
			health.ProbeFailureCount++
			if result.NextProbeAt == nil {
				delay := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}[health.ProbeFailureCount-1]
				next := result.CheckedAt.Add(delay)
				result.NextProbeAt = &next
			}
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = view.sql.ExecContext(ctx, `INSERT INTO account_probe_logs_geili(account_id,model,runtime_version,fingerprint,source,success,complete,balance_verified,checked_at,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb)`, result.Snapshot.AccountID, result.Model, result.Snapshot.RuntimeVersion, result.Snapshot.Fingerprint, result.Source, result.Success, result.Complete, result.BalanceVerified, result.CheckedAt, string(encoded))
		if err != nil {
			return err
		}
		// Probe bookkeeping must not invalidate the caller's business-state CAS.
		state.LastProbe = &result
		health.LastProbe = &result
		health.LastCheckedAt = &result.CheckedAt
		health.NextCheckAt = result.NextProbeAt
		health.NextProbeAt = result.NextProbeAt
		state.Models[result.Model] = health
		if result.Model == "" || state.StopReason == "temporary_failure" {
			state.NextCheckAt = result.NextProbeAt
		}
		return persistAccountRuntimeGeili(ctx, view, state, false)
	})
}

func (r *accountRepository) ActivateAccountAfterProbeGeili(ctx context.Context, input service.AccountProbeActivationGeili) (*service.AccountRuntimeStateGeili, error) {
	var activated *service.AccountRuntimeStateGeili
	err := r.withAccountRuntimeGeili(ctx, input.Snapshot.AccountID, func(view *accountRepository, state *service.AccountRuntimeStateGeili) error {
		if input.Snapshot.Fingerprint == "" {
			return service.ErrAccountRuntimeConflictGeili
		}
		account, err := view.GetByID(ctx, input.Snapshot.AccountID)
		if err != nil {
			return err
		}
		account.RuntimeState = state
		if account.ParentAccountID != nil {
			// Fault-path only. Parent manual stop updates all children in its
			// transaction. Avoid a child->parent lock inversion with that writer;
			// if it is waiting on this child, its later UPDATE wins the stop.
			var allowed bool
			err := accountRuntimeQueryRowGeili(ctx, view.sql, `SELECT p.deleted_at IS NULL AND p.status='active' AND (p.temp_unschedulable_until IS NULL OR p.temp_unschedulable_until<=NOW()) AND (NOT p.auto_pause_on_expired OR p.expires_at IS NULL OR p.expires_at>NOW()) AND NOT COALESCE(r.balance_blocked,FALSE) FROM accounts p LEFT JOIN account_runtime_geili r ON r.account_id=p.id WHERE p.id=$1`, *account.ParentAccountID).Scan(&allowed)
			if errors.Is(err, sql.ErrNoRows) || err == nil && !allowed {
				return service.ErrAccountRuntimeConflictGeili
			}
			if err != nil {
				return err
			}
		}
		matches, err := lockAndMatchProbeProxyIdentity(ctx, view.client, account)
		if err != nil {
			return err
		}
		if !matches {
			return service.ErrAccountRuntimeConflictGeili
		}
		if current := service.MakeAccountProbeSnapshotGeili(account); current != input.Snapshot {
			return service.ErrAccountRuntimeConflictGeili
		}
		var success, complete, balanceVerified bool
		var checkedAt time.Time
		err = accountRuntimeQueryRowGeili(ctx, view.sql, `SELECT success,complete,balance_verified,checked_at FROM account_probe_logs_geili WHERE account_id=$1 AND model=$2 AND runtime_version=$3 AND fingerprint=$4 ORDER BY id DESC LIMIT 1`, input.Snapshot.AccountID, input.Model, input.Snapshot.RuntimeVersion, input.Snapshot.Fingerprint).Scan(&success, &complete, &balanceVerified, &checkedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrAccountProbeUnhealthyGeili
		}
		if err != nil {
			return err
		}
		if !success || !complete {
			return service.ErrAccountProbeUnhealthyGeili
		}
		if state.BalanceBlocked {
			if !input.TrustedBalanceVerified || !balanceVerified || (state.BalanceBlockedAt != nil && checkedAt.Before(*state.BalanceBlockedAt)) {
				return service.ErrAccountBalanceBlockedGeili
			}
			state.BalanceBlocked = false
			state.BalanceReason = ""
			state.BalanceBlockedAt = nil
		}
		health := state.Models[input.Model]
		health.Blocked = false
		health.Health = "healthy"
		health.FailureCount = 0
		health.FailureWindowStartedAt = nil
		health.LastSuccessAt = &checkedAt
		health.NextCheckAt = nil
		health.NextProbeAt = nil
		health.LastError = ""
		state.Models[input.Model] = health
		// Account transport failures are cleared only by a complete current probe.
		if global, ok := state.Models[""]; ok {
			global.Blocked = false
			global.Health = "healthy"
			global.FailureCount = 0
			global.NextCheckAt = nil
			global.NextProbeAt = nil
			state.Models[""] = global
		}
		state.StopReason = ""
		state.NextCheckAt = nil
		state.ProtectionEnabled = input.EnableProtection
		if err := persistAccountRuntimeGeili(ctx, view, state, true); err != nil {
			return err
		}
		clearGenericTemporary := service.AccountGenericTemporaryStopGeili(account, time.Now())
		if _, err := view.sql.ExecContext(ctx, `UPDATE accounts SET status='active',schedulable=TRUE,error_message='',overload_until=NULL,temp_unschedulable_until=CASE WHEN $2 THEN NULL ELSE temp_unschedulable_until END,temp_unschedulable_reason=CASE WHEN $2 THEN NULL ELSE temp_unschedulable_reason END,updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, state.AccountID, clearGenericTemporary); err != nil {
			return err
		}
		activated = state
		return nil
	})
	return activated, err
}

const accountRuntimeAdmissionSQLGeili = `
SELECT a.deleted_at IS NULL AND a.status='active' AND a.schedulable
 AND NOT COALESCE(r.balance_blocked,FALSE) AND NOT COALESCE(pr.balance_blocked,FALSE)
 AND NOT COALESCE((r.model_states->''->>'blocked')::boolean,FALSE)
 AND NOT COALESCE((r.model_states->$2->>'blocked')::boolean,FALSE)
 AND (a.parent_account_id IS NULL OR (p.id IS NOT NULL AND p.deleted_at IS NULL AND p.status='active'
  AND (p.temp_unschedulable_until IS NULL OR p.temp_unschedulable_until<=NOW())
  AND (NOT p.auto_pause_on_expired OR p.expires_at IS NULL OR p.expires_at>NOW())
  AND NOT COALESCE((pr.model_states->''->>'blocked')::boolean,FALSE))),
 COALESCE(r.balance_blocked,FALSE) OR COALESCE(pr.balance_blocked,FALSE),
 CASE WHEN r.balance_blocked OR pr.balance_blocked THEN 'balance'
 WHEN a.parent_account_id IS NOT NULL AND (p.id IS NULL OR p.deleted_at IS NOT NULL OR p.status<>'active') THEN 'credential_unavailable'
 WHEN COALESCE((r.model_states->''->>'blocked')::boolean,FALSE) THEN 'temporary_failure'
 WHEN COALESCE((r.model_states->$2->>'blocked')::boolean,FALSE) THEN 'model_failure'
 ELSE COALESCE(r.stop_reason,'') END,
 COALESCE(a.parent_account_id,a.id)
FROM accounts a LEFT JOIN account_runtime_geili r ON r.account_id=a.id
LEFT JOIN accounts p ON p.id=a.parent_account_id
LEFT JOIN account_runtime_geili pr ON pr.account_id=p.id
WHERE a.id=$1`

func (r *accountRepository) CheckAccountRuntimeAdmissionGeili(ctx context.Context, id int64, model string) (*service.AccountRuntimeAdmissionGeili, error) {
	out := &service.AccountRuntimeAdmissionGeili{}
	err := r.accountRuntimeReadRowGeili(ctx, accountRuntimeAdmissionSQLGeili, id, model).Scan(&out.Allowed, &out.BalanceBlocked, &out.Reason, &out.CredentialAccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAccountNotFound
	}
	return out, err
}

// A single PostgreSQL advisory transaction lock serializes both the global
// budget and account-exclusive lease acquisition across application replicas.
func (r *accountRepository) AcquireProbeLeaseGeili(ctx context.Context, id int64, model string, ttl time.Duration) (*service.AccountProbeLeaseGeili, error) {
	if ttl <= 0 || ttl > 35*time.Second {
		ttl = 35 * time.Second
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	exec := tx.Client()
	if _, err := exec.ExecContext(ctx, `SELECT pg_advisory_xact_lock(2820142026)`); err != nil {
		return nil, err
	}
	if _, err := exec.ExecContext(ctx, `DELETE FROM account_probe_leases_geili WHERE expires_at<=NOW()`); err != nil {
		return nil, err
	}
	var active int
	var held bool
	if err := accountRuntimeQueryRowGeili(ctx, exec, `SELECT COUNT(*),COALESCE(BOOL_OR(account_id=$1),FALSE) FROM account_probe_leases_geili`, id).Scan(&active, &held); err != nil {
		return nil, err
	}
	if active >= 4 || held {
		return nil, nil
	}
	var tokenBytes [16]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return nil, err
	}
	lease := &service.AccountProbeLeaseGeili{AccountID: id, Model: model, Token: hex.EncodeToString(tokenBytes[:])}
	if err := accountRuntimeQueryRowGeili(ctx, exec, `INSERT INTO account_probe_leases_geili(account_id,model,token,expires_at) SELECT id,$2,$3,NOW()+($4 * INTERVAL '1 millisecond') FROM accounts WHERE id=$1 AND deleted_at IS NULL RETURNING expires_at`, id, model, lease.Token, ttl.Milliseconds()).Scan(&lease.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrAccountNotFound
		}
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return lease, nil
}

func (r *accountRepository) ReleaseProbeLeaseGeili(ctx context.Context, lease *service.AccountProbeLeaseGeili) error {
	if lease == nil {
		return nil
	}
	_, err := r.sql.ExecContext(ctx, `DELETE FROM account_probe_leases_geili WHERE account_id=$1 AND token=$2`, lease.AccountID, lease.Token)
	return err
}

// Compile-time documentation of the transaction executor capability we use.
var _ sqlExecutor = (*dbent.Client)(nil)

// The first page is bounded by both time and rows before inspecting error
// fields. A successful request after the evidence prevents startup reclamation;
// failed streams with partial usage do not count as a successful recharge.
func (r *accountRepository) InitializeAccountBalanceBlocksGeili(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	rows, err := r.sql.QueryContext(ctx, `
WITH recent AS MATERIALIZED (
 SELECT id,account_id,created_at,request_id,upstream_error_message,upstream_errors
 FROM ops_error_logs
 WHERE created_at>=NOW()-INTERVAL '24 hours' AND account_id IS NOT NULL
 ORDER BY created_at DESC,id DESC LIMIT 10000
), evidence AS (
 SELECT id,account_id,created_at,request_id FROM recent e
 WHERE LOWER(COALESCE(upstream_error_message,'')) LIKE ANY(ARRAY[
 '%insufficient balance%','%insufficient account balance%','%credit balance is too low%',
 '%credit balance exhausted%','%账户余额不足%','%余额不足%','%用户额度不足%','%预扣费额度失败%'])
 OR EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(e.upstream_errors)='array' THEN e.upstream_errors ELSE '[]'::jsonb END) ev
 WHERE LOWER(COALESCE(ev->>'message','')) LIKE ANY(ARRAY['%insufficient balance%','%credit balance is too low%','%余额不足%','%预扣费额度失败%']))
), confirmed AS (
 SELECT DISTINCT ON(account_id) account_id,created_at AS evidence_at,request_id
 FROM evidence ORDER BY account_id,created_at DESC,id DESC
)
SELECT c.account_id,c.evidence_at,COALESCE(c.request_id,'') FROM confirmed c JOIN accounts a ON a.id=c.account_id AND a.deleted_at IS NULL
WHERE NOT EXISTS (SELECT 1 FROM account_runtime_geili r WHERE r.account_id=c.account_id AND r.balance_blocked)
AND NOT EXISTS (SELECT 1 FROM usage_logs u WHERE u.account_id=c.account_id AND (u.created_at>c.evidence_at OR (c.request_id<>'' AND u.request_id=c.request_id))
 AND NOT EXISTS (SELECT 1 FROM ops_error_logs failed WHERE failed.request_id=u.request_id AND failed.status_code>=400))
ORDER BY c.evidence_at DESC LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	type evidence struct {
		id        int64
		at        time.Time
		requestID string
	}
	var candidates []evidence
	for rows.Next() {
		var candidate evidence
		if err := rows.Scan(&candidate.id, &candidate.at, &candidate.requestID); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, candidate)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, candidate := range candidates {
		changed := false
		err := r.withAccountRuntimeGeili(ctx, candidate.id, func(view *accountRepository, state *service.AccountRuntimeStateGeili) error {
			if state.BalanceBlocked {
				return nil
			}
			var succeeded bool
			if err := accountRuntimeQueryRowGeili(ctx, view.sql, `SELECT EXISTS(SELECT 1 FROM usage_logs u WHERE u.account_id=$1 AND (u.created_at>$2 OR ($3<>'' AND u.request_id=$3)) AND NOT EXISTS(SELECT 1 FROM ops_error_logs e WHERE e.request_id=u.request_id AND e.status_code>=400))`, candidate.id, candidate.at, candidate.requestID).Scan(&succeeded); err != nil {
				return err
			}
			if succeeded {
				return nil
			}
			state.BalanceBlocked = true
			state.BalanceReason = "supplier_insufficient_balance"
			state.BalanceBlockedAt = &candidate.at
			state.StopReason = "balance"
			if err := persistAccountRuntimeGeili(ctx, view, state, true); err != nil {
				return err
			}
			if err := stopRuntimeAccountGeili(ctx, view, candidate.id, state.BalanceReason); err != nil {
				return err
			}
			changed = true
			return nil
		})
		if err != nil {
			return count, err
		}
		if changed {
			count++
		}
	}
	return count, nil
}

func (r *accountRepository) ListAccountBalanceBlockedGeili(ctx context.Context, afterID int64, limit int) ([]int64, bool, error) {
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT r.account_id FROM account_runtime_geili r JOIN accounts a ON a.id=r.account_id AND a.deleted_at IS NULL WHERE r.balance_blocked AND r.account_id>$1 ORDER BY r.account_id LIMIT $2`, afterID, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	ids := make([]int64, 0, limit+1)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	return ids, more, nil
}
