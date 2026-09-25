package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.SettlementReconciliationRepository = (*usageBillingRepository)(nil)

const settlementReconciliationMaxRows = 1000
const settlementReconciliationWindow = 5 * time.Minute

// ReconcileRecentSettlements checks a bounded tail of delivered live receipts.
// The primary-key tail is limited BEFORE time/status filters or joins, so old
// history and a delivery outage cannot make monitoring scan the entire table.
func (r *usageBillingRepository) ReconcileRecentSettlements(ctx context.Context, limit int) (service.UsageSettlementReconciliation, error) {
	if r == nil || r.db == nil {
		return service.UsageSettlementReconciliation{}, errors.New("settlement reconciliation database unavailable")
	}
	return reconcileRecentSettlements(ctx, r.db, limit, time.Now().UTC())
}

type settlementReconciliationQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func reconcileRecentSettlements(ctx context.Context, db settlementReconciliationQuerier, limit int, now time.Time) (service.UsageSettlementReconciliation, error) {
	if limit <= 0 || limit > settlementReconciliationMaxRows {
		limit = settlementReconciliationMaxRows
	}
	end := now.UTC().Truncate(time.Microsecond)
	start := end.Add(-settlementReconciliationWindow)
	out := service.UsageSettlementReconciliation{Coverage: "recent_delivered_sample", Limit: limit, WindowStart: start, WindowEnd: end, NetDiscrepancyUSD: "0", AbsoluteDiscrepancyUSD: "0", SubscriptionNetDiscrepancyUSD: "0"}
	var oldest, newest sql.NullTime
	err := db.QueryRowContext(ctx, settlementReconciliationSQL, limit, start, end).Scan(
		&out.ScannedCount, &out.CheckedCount, &out.LastID, &out.FirstID, &out.HasMore, &oldest, &newest,
		&out.AmountMismatchCount, &out.DetailMissingCount, &out.IdentityMismatchCount, &out.DedupMismatchCount,
		&out.SubscriptionCheckedCount, &out.SubscriptionNotCheckedCount, &out.SubscriptionMismatchCount,
		&out.NetDiscrepancyUSD, &out.AbsoluteDiscrepancyUSD, &out.SubscriptionNetDiscrepancyUSD,
	)
	if err != nil {
		return out, err
	}
	out.SkippedCount = out.ScannedCount - out.CheckedCount
	if oldest.Valid {
		out.OldestCheckedAt = &oldest.Time
	}
	if newest.Valid {
		out.NewestCheckedAt = &newest.Time
	}
	return out, nil
}

const settlementReconciliationSQL = `
WITH tail AS MATERIALIZED (
 SELECT id,user_id,api_key_id,account_id,subscription_id,request_id,usage_request_id,usage_log_id,
 request_fingerprint,charged_amount,accounting_date,state,record_source,record_completeness,delivered_at,
 command->>'SubscriptionAdmissionKey' admission_key,
 COALESCE(command->>'TerminalFailure','false') terminal_failure
 FROM usage_settlement_receipts ORDER BY id DESC LIMIT ($1+1)
), sampled AS MATERIALIZED (
 SELECT * FROM tail ORDER BY id DESC LIMIT $1
), eligible AS MATERIALIZED (
 SELECT * FROM sampled WHERE state='settled' AND record_source='live'
 AND record_completeness='complete' AND charged_amount IS NOT NULL
 AND delivered_at >= $2::timestamptz AND delivered_at <= $3::timestamptz
 AND terminal_failure<>'true'
), checked AS (
 SELECT r.*,u.id log_id,u.actual_cost log_amount,
 (u.user_id=r.user_id AND u.api_key_id=r.api_key_id AND u.account_id IS NOT DISTINCT FROM r.account_id
  AND u.subscription_id IS NOT DISTINCT FROM r.subscription_id AND u.request_id=r.usage_request_id) log_identity_ok,
 (d.n>0 AND d.matches) dedup_ok,
 (r.subscription_id IS NOT NULL AND NULLIF(r.admission_key,'') IS NOT NULL) check_subscription,
 (sr.request_key IS NOT NULL AND sr.subscription_id=r.subscription_id AND sr.api_key_id=r.api_key_id
  AND sr.status='settled' AND sr.billing_request_id=r.request_id
  AND sr.cost_usd=r.charged_amount AND allocation.amount=r.charged_amount
  AND NOT allocation.wrong_parent
  AND COALESCE(binding.usage_date,(sr.admitted_at AT TIME ZONE 'Asia/Shanghai')::date)=r.accounting_date) subscription_ok,
 allocation.amount allocation_amount
 FROM eligible r
 LEFT JOIN usage_logs u ON u.id=r.usage_log_id
 LEFT JOIN LATERAL (
  SELECT COUNT(*) n,BOOL_AND(e.request_fingerprint=r.request_fingerprint) matches
  FROM (
   SELECT request_fingerprint FROM usage_billing_dedup WHERE request_id=r.request_id AND api_key_id=r.api_key_id
   UNION ALL
   SELECT request_fingerprint FROM usage_billing_dedup_archive WHERE request_id=r.request_id AND api_key_id=r.api_key_id
  ) e
 ) d ON TRUE
 LEFT JOIN subscription_requests sr ON sr.request_key=NULLIF(r.admission_key,'') AND r.subscription_id IS NOT NULL
 LEFT JOIN subscription_request_contracts binding ON binding.request_key=sr.request_key
 LEFT JOIN LATERAL (
  SELECT COALESCE(SUM(a.cost_usd),0) amount,
   COALESCE(BOOL_OR(e.id IS NULL OR e.user_subscription_id IS DISTINCT FROM r.subscription_id),FALSE) wrong_parent
  FROM subscription_usage_allocations a LEFT JOIN user_subscription_entitlements e ON e.id=a.entitlement_id
  WHERE a.request_key=sr.request_key
 ) allocation ON sr.request_key IS NOT NULL
)
SELECT
 (SELECT COUNT(*) FROM sampled),COUNT(*),
 COALESCE((SELECT MIN(id) FROM sampled),0),COALESCE((SELECT MAX(id) FROM sampled),0),
 (SELECT COUNT(*)>$1 FROM tail),MIN(delivered_at),MAX(delivered_at),
 COUNT(*) FILTER(WHERE log_id IS NOT NULL AND log_identity_ok AND log_amount<>charged_amount),
 COUNT(*) FILTER(WHERE log_id IS NULL),
 COUNT(*) FILTER(WHERE log_id IS NOT NULL AND log_identity_ok IS NOT TRUE),
 COUNT(*) FILTER(WHERE dedup_ok IS NOT TRUE),
 COUNT(*) FILTER(WHERE check_subscription),
 COUNT(*) FILTER(WHERE subscription_id IS NOT NULL AND NOT check_subscription),
 COUNT(*) FILTER(WHERE check_subscription AND subscription_ok IS NOT TRUE),
 COALESCE(SUM(log_amount-charged_amount) FILTER(WHERE log_id IS NOT NULL AND log_identity_ok),0)::text,
 COALESCE(SUM(ABS(log_amount-charged_amount)) FILTER(WHERE log_id IS NOT NULL AND log_identity_ok),0)::text,
 COALESCE(SUM(allocation_amount-charged_amount) FILTER(WHERE check_subscription AND allocation_amount IS NOT NULL),0)::text
FROM checked`
