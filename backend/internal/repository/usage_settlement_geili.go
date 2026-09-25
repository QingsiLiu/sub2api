package repository

// Geili: financial settlement is independent of usage-log delivery. No worker
// goroutines are started by a repository; the service owns lifecycle and retries.
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

var _ service.UsageSettlementRepository = (*usageBillingRepository)(nil)

const settlementLease = 2 * time.Minute
const settlementTaskTimeout = 15 * time.Second

func (r *usageBillingRepository) PrepareSettlement(ctx context.Context, cmd *service.UsageBillingCommand, log *service.UsageLog) error {
	if r == nil || r.db == nil {
		return errors.New("usage settlement repository db is nil")
	}
	if cmd == nil {
		return errors.New("nil usage settlement command")
	}
	cmd.Normalize()
	if err := service.ValidateUsageSettlementCommand(cmd); err != nil {
		return err
	}
	if log == nil {
		log = cmd.UsageDetail
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = r.prepareSettlementTx(ctx, tx, cmd, log); err != nil {
		return err
	}
	return tx.Commit()
}

type settlementReceipt struct {
	id      int64
	state   string
	command *service.UsageBillingCommand
}

func (r *usageBillingRepository) prepareSettlementTx(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand, log *service.UsageLog) (settlementReceipt, error) {
	var out settlementReceipt
	cmd.Normalize()
	if err := service.ValidateUsageSettlementCommand(cmd); err != nil {
		return out, err
	}
	if cmd.CompletedAt.IsZero() {
		cmd.CompletedAt = time.Now().UTC().Truncate(time.Microsecond)
	}
	completed := cmd.CompletedAt.UTC().Truncate(time.Microsecond)
	if cmd.TerminalFailure && log != nil {
		return out, errors.New("terminal failure cannot carry usage detail")
	}
	detail, err := service.NewUsageSettlementDetail(log)
	if err != nil {
		return out, err
	}
	usageID := ""
	complete := "partial"
	var detailJSON any
	var groupID any
	charge := service.QuantizeUsageBillingAmount(cmd.BalanceCost + cmd.SubscriptionCost)
	if detail != nil {
		if detail.UserID != cmd.UserID || detail.APIKeyID != cmd.APIKeyID || detail.AccountID != cmd.AccountID || !sameOptionalID(detail.SubscriptionID, cmd.SubscriptionID) {
			return out, service.ErrUsageBillingRequestConflict
		}
		usageID = strings.TrimSpace(detail.RequestID)
		if usageID == "" {
			return out, service.ErrUsageBillingRequestIDRequired
		}
		// Only canonical successful settlement evidence can determine the charged sum.
		detail.ActualCost = charge
		if detail.RouteBillingSnapshot != nil {
			detail.RouteBillingSnapshot.ActualCost = charge
		}
		if detail.CreatedAt.IsZero() {
			detail.CreatedAt = completed
		}
		if detail.GroupID != nil {
			groupID = *detail.GroupID
		}
		b, e := json.Marshal(detail)
		if e != nil {
			return out, e
		}
		detailJSON = string(b)
		if detail.AccountID > 0 && detail.Model != "" {
			complete = "complete"
		}
	}
	var admitted any
	accounting := time.Now().UTC()
	if cmd.SubscriptionAdmissionKey != "" && cmd.SubscriptionID != nil {
		var at time.Time
		if err = tx.QueryRowContext(ctx, `SELECT admitted_at FROM subscription_requests WHERE request_key=$1 AND subscription_id=$2 AND api_key_id=$3`, cmd.SubscriptionAdmissionKey, *cmd.SubscriptionID, cmd.APIKeyID).Scan(&at); err != nil {
			return out, err
		}
		admitted = at
		accounting = at
	}
	billingType := cmd.BillingType
	if cmd.SubscriptionID != nil {
		billingType = service.BillingTypeSubscription
	}
	raw, err := json.Marshal(cmd)
	if err != nil {
		return out, err
	}
	var accountID any
	if cmd.AccountID > 0 {
		accountID = cmd.AccountID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO usage_settlement_receipts
 (request_id,api_key_id,usage_request_id,request_fingerprint,user_id,account_id,group_id,subscription_id,billing_type,charged_amount,accounting_date,admitted_at,completed_at,command,detail,record_completeness)
 VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,($11::timestamptz AT TIME ZONE 'Asia/Shanghai')::date,$12,$13,$14::jsonb,$15::jsonb,$16)
 ON CONFLICT(request_id,api_key_id) DO NOTHING`, cmd.RequestID, cmd.APIKeyID, usageID, cmd.RequestFingerprint, cmd.UserID, accountID, groupID, cmd.SubscriptionID, billingType, charge, accounting, admitted, completed, string(raw), detailJSON, complete)
	if err != nil {
		return out, err
	}
	var priorRaw []byte
	var priorFingerprint, source string
	var priorUser int64
	var priorAccount, priorSub sql.NullInt64
	var priorUsage sql.NullString
	var priorAmount sql.NullFloat64
	err = tx.QueryRowContext(ctx, `SELECT id,state,request_fingerprint,user_id,account_id,subscription_id,usage_request_id,charged_amount,command,record_source FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2 FOR UPDATE`, cmd.RequestID, cmd.APIKeyID).Scan(&out.id, &out.state, &priorFingerprint, &priorUser, &priorAccount, &priorSub, &priorUsage, &priorAmount, &priorRaw, &source)
	if err != nil {
		return out, err
	}
	if priorFingerprint != cmd.RequestFingerprint || priorUser != cmd.UserID || priorAccount.Int64 != cmd.AccountID || priorSub.Int64 != idOrZero(cmd.SubscriptionID) || !priorAmount.Valid || priorAmount.Float64 != charge || (priorUsage.Valid && usageID != "" && priorUsage.String != usageID) {
		return out, service.ErrUsageBillingRequestConflict
	}
	if len(priorRaw) > 0 {
		var prior service.UsageBillingCommand
		if err = json.Unmarshal(priorRaw, &prior); err != nil {
			return out, err
		}
		if !sameSettlementFinancialCommand(&prior, cmd) {
			return out, service.ErrUsageBillingRequestConflict
		}
		out.command = &prior
	}
	// An early Apply caller may have stored amount-only evidence. Upgrade only
	// missing metadata, never overwrite the first complete snapshot or its prices.
	if detail != nil && source == "live" {
		_, err = tx.ExecContext(ctx, `UPDATE usage_settlement_receipts SET detail=COALESCE(detail,$2::jsonb),usage_request_id=COALESCE(usage_request_id,NULLIF($3,'')),group_id=COALESCE(group_id,$4),record_completeness=CASE WHEN detail IS NULL THEN $5 ELSE record_completeness END,updated_at=NOW() WHERE id=$1`, out.id, detailJSON, usageID, groupID, complete)
	}
	return out, err
}

func sameOptionalID(a, b *int64) bool { return idOrZero(a) == idOrZero(b) }
func idOrZero(a *int64) int64 {
	if a == nil {
		return 0
	}
	return *a
}
func sameSettlementFinancialCommand(a, b *service.UsageBillingCommand) bool {
	a.Normalize()
	b.Normalize()
	return a.UserID == b.UserID && a.APIKeyID == b.APIKeyID && a.AccountID == b.AccountID && sameOptionalID(a.SubscriptionID, b.SubscriptionID) && a.BalanceCost == b.BalanceCost && a.SubscriptionCost == b.SubscriptionCost && a.APIKeyQuotaCost == b.APIKeyQuotaCost && a.APIKeyRateLimitCost == b.APIKeyRateLimitCost && a.AccountQuotaCost == b.AccountQuotaCost && a.Platform == b.Platform && a.PlatformQuotaCost == b.PlatformQuotaCost && a.AccountType == b.AccountType && a.Model == b.Model && a.ServiceTier == b.ServiceTier && a.ReasoningEffort == b.ReasoningEffort && a.BillingType == b.BillingType && a.InputTokens == b.InputTokens && a.OutputTokens == b.OutputTokens && a.CacheCreationTokens == b.CacheCreationTokens && a.CacheReadTokens == b.CacheReadTokens && a.ImageCount == b.ImageCount && a.MediaType == b.MediaType && a.RequestPayloadHash == b.RequestPayloadHash && a.TerminalFailure == b.TerminalFailure
}

func markSettlementSettledTx(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE usage_settlement_receipts SET state='settled',settled_at=COALESCE(settled_at,NOW()),delivered_at=CASE WHEN command->>'TerminalFailure'='true' THEN COALESCE(delivered_at,NOW()) ELSE delivered_at END,accounting_date=CASE WHEN admitted_at IS NULL THEN (COALESCE(settled_at,NOW()) AT TIME ZONE 'Asia/Shanghai')::date ELSE accounting_date END,settlement_lease_token=NULL,settlement_lease_until=NULL,last_settlement_error=NULL,updated_at=NOW() WHERE id=$1`, id)
	return err
}

type leasedSettlement struct {
	id      int64
	command []byte
}

func clampSettlementBatch(n int) int {
	if n <= 0 {
		return 4
	}
	if n > 4 {
		return 4
	}
	return n
}
func (r *usageBillingRepository) ProcessPendingSettlements(ctx context.Context, limit int) ([]service.UsageSettlementApplied, error) {
	token := uuid.NewString()
	rows, err := r.db.QueryContext(ctx, `WITH selected AS (
 SELECT id FROM usage_settlement_receipts WHERE state='pending' AND command IS NOT NULL AND record_source='live' AND next_settlement_at<=NOW() AND (settlement_lease_until IS NULL OR settlement_lease_until<NOW()) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1)
 UPDATE usage_settlement_receipts r SET settlement_lease_token=$2,settlement_lease_until=NOW()+($3*INTERVAL '1 second'),settlement_attempts=settlement_attempts+1 FROM selected s WHERE r.id=s.id RETURNING r.id,r.command`, clampSettlementBatch(limit), token, settlementLease.Seconds())
	if err != nil {
		return nil, err
	}
	pending := []leasedSettlement{}
	for rows.Next() {
		var p leasedSettlement
		if err = rows.Scan(&p.id, &p.command); err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	applied := []service.UsageSettlementApplied{}
	var firstErr error
	for _, p := range pending {
		// Shutdown only finishes the current bounded attempt. Unvisited durable
		// rows keep their leases and are reclaimed after expiry by a new worker.
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		taskCtx, cancel := context.WithTimeout(ctx, settlementTaskTimeout)
		var cmd service.UsageBillingCommand
		e := json.Unmarshal(p.command, &cmd)
		var result *service.UsageBillingApplyResult
		if e == nil {
			result, e = r.Apply(taskCtx, &cmd)
		}
		cancel()
		if e == nil {
			if result != nil && result.Applied {
				applied = append(applied, service.UsageSettlementApplied{Command: &cmd, Result: result})
			}
			continue
		}
		if firstErr == nil {
			firstErr = e
		}
		// Error details can contain DB values. Persist only classified error codes.
		retryCtx, retryCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, retryErr := r.db.ExecContext(retryCtx, `UPDATE usage_settlement_receipts SET settlement_lease_token=NULL,settlement_lease_until=NULL,last_settlement_error=$3,next_settlement_at=NOW()+INTERVAL '5 seconds',updated_at=NOW() WHERE id=$1 AND settlement_lease_token=$2 AND state='pending'`, p.id, token, settlementErrorCode(e))
		retryCancel()
		if retryErr != nil && firstErr == nil {
			firstErr = retryErr
		}
	}
	return applied, firstErr
}

func settlementErrorCode(err error) string {
	if errors.Is(err, service.ErrUsageBillingRequestConflict) {
		return "identity_or_amount_conflict"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "retryable_storage_or_validation_error"
}

func (r *usageBillingRepository) DeliverSettledUsage(ctx context.Context, limit int) (service.UsageSettlementDeliveryStats, error) {
	var stats service.UsageSettlementDeliveryStats
	token := uuid.NewString()
	rows, err := r.db.QueryContext(ctx, `WITH selected AS (
 SELECT id FROM usage_settlement_receipts WHERE state='settled' AND delivered_at IS NULL AND record_source='live' AND record_completeness='complete' AND detail IS NOT NULL AND charged_amount IS NOT NULL AND next_delivery_at<=NOW() AND (delivery_lease_until IS NULL OR delivery_lease_until<NOW()) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1)
 UPDATE usage_settlement_receipts r SET delivery_lease_token=$2,delivery_lease_until=NOW()+($3*INTERVAL '1 second'),delivery_attempts=delivery_attempts+1 FROM selected s WHERE r.id=s.id RETURNING r.id`, clampSettlementBatch(limit), token, settlementLease.Seconds())
	if err != nil {
		return stats, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return stats, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return stats, err
	}
	stats.Claimed = len(ids)
	var firstErr error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		taskCtx, cancel := context.WithTimeout(ctx, settlementTaskTimeout)
		e := r.deliverSettlementUsage(taskCtx, id, token)
		cancel()
		if e == nil {
			stats.Delivered++
			continue
		}
		stats.Failed++
		if firstErr == nil {
			firstErr = e
		}
		retryCtx, retryCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, _ = r.db.ExecContext(retryCtx, `UPDATE usage_settlement_receipts SET delivery_lease_token=NULL,delivery_lease_until=NULL,last_delivery_error=$3,next_delivery_at=NOW()+INTERVAL '5 seconds',updated_at=NOW() WHERE id=$1 AND delivery_lease_token=$2 AND delivered_at IS NULL`, id, token, settlementErrorCode(e))
		retryCancel()
	}
	return stats, firstErr
}

func (r *usageBillingRepository) deliverSettlementUsage(ctx context.Context, id int64, token string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	var amount float64
	err = tx.QueryRowContext(ctx, `SELECT detail,charged_amount FROM usage_settlement_receipts WHERE id=$1 AND delivery_lease_token=$2 AND state='settled' AND delivered_at IS NULL FOR UPDATE`, id, token).Scan(&raw, &amount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var detail service.UsageSettlementDetail
	if err = json.Unmarshal(raw, &detail); err != nil {
		return err
	}
	log := detail.UsageLog()
	log.ActualCost = amount
	inserted, err := (&usageLogRepository{}).createSingle(ctx, tx, log)
	if err != nil {
		return err
	}
	if !inserted {
		var user, account int64
		var sub sql.NullInt64
		var priorAmount float64
		if err = tx.QueryRowContext(ctx, `SELECT user_id,account_id,subscription_id,actual_cost FROM usage_logs WHERE id=$1 FOR UPDATE`, log.ID).Scan(&user, &account, &sub, &priorAmount); err != nil {
			return err
		}
		if user != log.UserID || account != log.AccountID || sub.Int64 != idOrZero(log.SubscriptionID) {
			return service.ErrUsageBillingRequestConflict
		}
		if priorAmount != amount && priorAmount != 0 {
			return service.ErrUsageBillingRequestConflict
		}
		if priorAmount == 0 {
			// Free successful usage is authoritative too: restore its observed
			// model/tokens even when a failed placeholder also has zero cost.
			// Failed log placeholders never overwrite a committed financial receipt.
			// Populate every observed field from the frozen whitelist, not today's key.
			if err = replaceZeroUsagePlaceholder(ctx, tx, log, raw); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE usage_settlement_receipts SET delivered_at=NOW(),usage_log_id=$3,delivery_lease_token=NULL,delivery_lease_until=NULL,last_delivery_error=NULL,updated_at=NOW() WHERE id=$1 AND delivery_lease_token=$2`, id, token, log.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// All identifiers below are static database columns, not user input.
var settlementUsageReplacementColumns = []string{
	"model", "requested_model", "upstream_model", "upstream_response_model", "upstream_model_mismatch", "group_id",
	"input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "image_output_tokens", "image_output_cost", "image_input_tokens", "image_input_cost",
	"input_cost", "output_cost", "cache_creation_cost", "cache_read_cost", "total_cost", "actual_cost", "rate_multiplier", "account_rate_multiplier", "billing_type", "request_type", "stream", "openai_ws_mode", "duration_ms", "first_token_ms", "user_agent", "ip_address",
	"image_count", "image_size", "image_input_size", "image_output_size", "image_size_source", "image_size_breakdown", "video_count", "video_resolution", "video_duration_seconds", "service_tier", "reasoning_effort", "requested_reasoning_effort", "inbound_endpoint", "upstream_endpoint", "cache_ttl_overridden", "long_context_billing_applied", "channel_id", "model_mapping_chain", "billing_tier", "billing_mode", "account_stats_cost", "route_billing_snapshot", "upstream_request_id", "session_id", "native_compaction_v2", "created_at",
}

func replaceZeroUsagePlaceholder(ctx context.Context, tx *sql.Tx, log *service.UsageLog, raw []byte) error {
	sets := make([]string, 0, len(settlementUsageReplacementColumns))
	for _, c := range settlementUsageReplacementColumns {
		sets = append(sets, c+"=replacement."+c)
	}
	_, err := tx.ExecContext(ctx, `WITH replacement AS(SELECT * FROM jsonb_populate_record(NULL::usage_logs,$2::jsonb)) UPDATE usage_logs u SET `+strings.Join(sets, ",")+` FROM replacement WHERE u.id=$1 AND u.actual_cost=0`, log.ID, string(raw))
	return err
}

func (r *usageBillingRepository) SettlementHealth(ctx context.Context) (service.UsageSettlementHealth, error) {
	var h service.UsageSettlementHealth
	err := r.db.QueryRowContext(ctx, `SELECT
 COUNT(*) FILTER(WHERE state='pending'),
 COUNT(*) FILTER(WHERE state='settled' AND delivered_at IS NULL AND record_source='live' AND record_completeness='complete'),
 COUNT(*) FILTER(WHERE state='settled' AND record_source='live' AND record_completeness<>'complete' AND delivered_at IS NULL),
 COALESCE(SUM(settlement_attempts) FILTER(WHERE last_settlement_error IS NOT NULL),0),
 COALESCE(SUM(delivery_attempts) FILTER(WHERE last_delivery_error IS NOT NULL),0),
 COALESCE(MAX(EXTRACT(EPOCH FROM NOW()-created_at)) FILTER(WHERE state='pending' OR (state='settled' AND delivered_at IS NULL AND record_source='live')),0),
 COUNT(*) FILTER(WHERE (state='pending' OR(state='settled' AND delivered_at IS NULL AND record_source='live')) AND created_at<NOW()-INTERVAL '30 seconds'),
 COUNT(*) FILTER(WHERE (state='pending' OR(state='settled' AND delivered_at IS NULL AND record_source='live')) AND created_at<NOW()-INTERVAL '120 seconds'),
 COUNT(*) FILTER(WHERE last_settlement_error='identity_or_amount_conflict' OR last_delivery_error='identity_or_amount_conflict')
 FROM usage_settlement_receipts WHERE state='pending' OR (state='settled' AND delivered_at IS NULL AND record_source='live')`).Scan(&h.PendingSettlements, &h.PendingDeliveries, &h.PartialLiveSettlements, &h.FailedSettlements, &h.FailedDeliveries, &h.OldestPendingSeconds, &h.Warning30Seconds, &h.Critical120Seconds, &h.AmountMismatchCount)
	return h, err
}

func incrementSettlementPlatformQuota(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) error {
	if cmd.Platform == "" || cmd.PlatformQuotaCost <= 0 || cmd.SubscriptionID != nil {
		return nil
	}
	now := time.Now().UTC()
	daily := timezone.StartOfDay(now)
	weekly := timezone.StartOfWeek(now)
	_, err := tx.ExecContext(ctx, `UPDATE user_platform_quotas SET
 daily_usage_usd=CASE WHEN daily_window_start IS DISTINCT FROM $4 THEN $3 ELSE daily_usage_usd+$3 END,
 weekly_usage_usd=CASE WHEN weekly_window_start IS DISTINCT FROM $5 THEN $3 ELSE weekly_usage_usd+$3 END,
 monthly_usage_usd=CASE WHEN monthly_window_start IS NULL OR monthly_window_start<=$6::timestamptz-INTERVAL '30 days' THEN $3 ELSE monthly_usage_usd+$3 END,
 daily_window_start=$4,weekly_window_start=$5,
 monthly_window_start=CASE WHEN monthly_window_start IS NULL OR monthly_window_start<=$6::timestamptz-INTERVAL '30 days' THEN $6 ELSE monthly_window_start END,updated_at=$6
 WHERE user_id=$1 AND platform=$2 AND deleted_at IS NULL AND(daily_limit_usd IS NOT NULL OR weekly_limit_usd IS NOT NULL OR monthly_limit_usd IS NOT NULL)`, cmd.UserID, cmd.Platform, cmd.PlatformQuotaCost, daily, weekly, now)
	if err != nil {
		return fmt.Errorf("settlement platform quota: %w", err)
	}
	return nil
}

// Legacy fingerprints prove identity, not a recoverable amount. A rollout retry
// may hit an old dedup key with no new receipt: require independently persisted
// money evidence, preserve its original date, and never reprice historical usage.
func (r *usageBillingRepository) confirmLegacySettlementTx(ctx context.Context, tx *sql.Tx, receiptID int64, cmd *service.UsageBillingCommand) error {
	var settled time.Time
	if err := tx.QueryRowContext(ctx, `SELECT created_at FROM usage_billing_dedup WHERE request_id=$1 AND api_key_id=$2 UNION ALL SELECT created_at FROM usage_billing_dedup_archive WHERE request_id=$1 AND api_key_id=$2 ORDER BY created_at LIMIT 1`, cmd.RequestID, cmd.APIKeyID).Scan(&settled); err != nil {
		return err
	}
	charge := service.QuantizeUsageBillingAmount(cmd.BalanceCost + cmd.SubscriptionCost)
	var usageID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT usage_request_id FROM usage_settlement_receipts WHERE id=$1`, receiptID).Scan(&usageID); err != nil {
		return err
	}
	if usageID.Valid {
		var raw []byte
		var logID int64
		err := tx.QueryRowContext(ctx, `SELECT to_jsonb(u),u.id FROM usage_logs u WHERE u.request_id=$1 AND u.api_key_id=$2`, usageID.String, cmd.APIKeyID).Scan(&raw, &logID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			var old service.UsageSettlementDetail
			if err = json.Unmarshal(raw, &old); err != nil {
				return err
			}
			if old.UserID != cmd.UserID || old.AccountID != cmd.AccountID || !sameOptionalID(old.SubscriptionID, cmd.SubscriptionID) || old.ActualCost != charge {
				return service.ErrUsageBillingRequestConflict
			}
			frozen, err := json.Marshal(&old)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE usage_settlement_receipts SET detail=$2::jsonb,record_completeness='complete',settled_at=$3,completed_at=$4,accounting_date=CASE WHEN admitted_at IS NOT NULL THEN (admitted_at AT TIME ZONE 'Asia/Shanghai')::date ELSE ($3::timestamptz AT TIME ZONE 'Asia/Shanghai')::date END,usage_log_id=$5,delivered_at=NOW(),updated_at=NOW() WHERE id=$1`, receiptID, string(frozen), settled, old.CreatedAt, logID)
			return err
		}
	}
	if cmd.SubscriptionID != nil {
		var admitted, at time.Time
		var cost, allocated float64
		err := tx.QueryRowContext(ctx, `SELECT r.admitted_at,r.settled_at,r.cost_usd,COALESCE((SELECT SUM(a.cost_usd) FROM subscription_usage_allocations a WHERE a.request_key=r.request_key),0) FROM subscription_requests r JOIN user_subscriptions s ON s.id=r.subscription_id WHERE r.billing_request_id=$1 AND r.api_key_id=$2 AND r.subscription_id=$3 AND s.user_id=$4 AND r.status='settled' ORDER BY r.settled_at LIMIT 1`, cmd.RequestID, cmd.APIKeyID, *cmd.SubscriptionID, cmd.UserID).Scan(&admitted, &at, &cost, &allocated)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if cost != charge || allocated != cost {
				return service.ErrUsageBillingRequestConflict
			}
			// Amount is proven; unavailable tokens/model/latency remain unknown. This
			// partial recovery must not emit a fabricated full usage row.
			_, err = tx.ExecContext(ctx, `UPDATE usage_settlement_receipts SET charged_amount=$2,settled_at=$3,admitted_at=$4,accounting_date=($4::timestamptz AT TIME ZONE 'Asia/Shanghai')::date,completed_at=NULL,account_id=NULL,group_id=NULL,detail='{}'::jsonb,command=NULL,record_source='historical_recovery',record_completeness='partial',updated_at=NOW() WHERE id=$1`, receiptID, cost, at, admitted)
			return err
		}
	}
	return errors.New("legacy billing key has no independently verified monetary evidence; historical reconciliation required")
}
