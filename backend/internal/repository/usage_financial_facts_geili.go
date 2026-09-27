package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/lib/pq"
)

// Readers resolve at most this many queued source events themselves; a larger
// backlog (bulk repair, consumer outage) reads the canonical view instead.
const financialFactPendingLimit = 2000

// financialFactDays is the materialized accounting window, including today.
// Default views are today/yesterday/7 days; longer ranges read older days raw.
func financialFactDays() int {
	days, err := strconv.Atoi(strings.TrimSpace(os.Getenv("GEILI_FINANCIAL_FACT_DAYS")))
	if err != nil || days <= 0 {
		return 8
	}
	if days < 2 {
		return 2
	}
	if days > 62 {
		return 62
	}
	return days
}

func financialFactDate(at time.Time) string { return at.Format("2006-01-02") }

func financialFactKeyCondition(receipts, logs []int64) string {
	return fmt.Sprintf("((fact_kind=1 AND source_id=ANY('%s'::bigint[])) OR (fact_kind=2 AND source_id=ANY('%s'::bigint[])))", financialInt64Literal(receipts), financialInt64Literal(logs))
}

func financialInt64Literal(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// financialFactKeys expands source events to every statistics identity they can
// change, in the caller's snapshot. nil ids means all queued events.
func financialFactKeys(ctx context.Context, q sqlExecutor, ids []int64) (receipts, logs []int64, days []time.Time, err error) {
	filter, args := "", []any{}
	if ids != nil {
		filter, args = " WHERE e.id=ANY($1)", []any{pq.Array(ids)}
	}
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT k.fact_kind,k.source_id FROM usage_financial_rollup_events e CROSS JOIN LATERAL usage_financial_fact_keys(e.source,e.old_identity,e.new_identity) k`+filter, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	for rows.Next() {
		var kind int16
		var id sql.NullInt64
		if err = rows.Scan(&kind, &id); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		if !id.Valid {
			continue
		}
		if kind == 1 {
			receipts = append(receipts, id.Int64)
		} else {
			logs = append(logs, id.Int64)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT DISTINCT (e.new_identity->>'at')::timestamptz FROM usage_financial_rollup_events e WHERE e.source='day'`+strings.Replace(filter, " WHERE", " AND", 1), args...)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var at time.Time
		if err = rows.Scan(&at); err != nil {
			return nil, nil, nil, err
		}
		days = append(days, at)
	}
	return receipts, logs, days, rows.Err()
}

// projectFinancialFacts runs inside the financial rollup consumer transaction,
// after the state row lock. Every fact reflects this snapshot for all keys whose
// events are acknowledged by the same commit; later events stay queued and are
// overlaid by readers. Returns true while seeding/backfill is still incomplete.
func projectFinancialFacts(ctx context.Context, q sqlExecutor, today time.Time, ids []int64) (bool, error) {
	var cov sql.NullTime
	if err := scanSingleRow(ctx, q, `SELECT coverage_start FROM usage_financial_fact_state WHERE id=1`, nil, &cov); err != nil {
		return false, err
	}
	insert := `INSERT INTO usage_financial_facts SELECT * FROM usage_financial_fact_source `
	if !cov.Valid {
		// Seed rows whose accounting or bucket day is today or later. Queued
		// events are all visible to this snapshot and are acknowledged with it.
		where, args := financialUsageWhere(usagestats.UsageLogFilters{StartTime: &today})
		if _, err := q.ExecContext(ctx, `DELETE FROM usage_financial_facts`); err != nil {
			return false, err
		}
		if _, err := q.ExecContext(ctx, insert+where, args...); err != nil {
			return false, err
		}
		if _, err := q.ExecContext(ctx, insert+`WHERE accounting_date < $2::date AND `+financialRollupRangeSQL("$1::timestamptz", ""), today, financialFactDate(today)); err != nil {
			return false, err
		}
		_, err := q.ExecContext(ctx, `UPDATE usage_financial_fact_state SET coverage_start=$1::date WHERE id=1`, financialFactDate(today))
		return true, err
	}
	start := financialRollupDay(cov.Time)
	if len(ids) > 0 {
		receipts, logs, days, err := financialFactKeys(ctx, q, ids)
		if err != nil {
			return false, err
		}
		if len(receipts)+len(logs) > 0 {
			keys := financialFactKeyCondition(receipts, logs)
			if _, err = q.ExecContext(ctx, `DELETE FROM usage_financial_facts WHERE `+keys); err != nil {
				return false, err
			}
			if _, err = q.ExecContext(ctx, insert+`WHERE `+keys+` AND GREATEST(accounting_date,bucket_date) >= $1::date`, financialFactDate(start)); err != nil {
				return false, err
			}
		}
		// Partition removal: logs without receipts are identified only by day.
		for _, at := range days {
			if _, err = q.ExecContext(ctx, `DELETE FROM usage_financial_facts WHERE fact_kind=2 AND created_at >= $1 AND created_at < $1::timestamptz + INTERVAL '1 day'`, at); err != nil {
				return false, err
			}
			if _, err = q.ExecContext(ctx, insert+`WHERE fact_kind=2 AND created_at >= $1 AND created_at < $1::timestamptz + INTERVAL '1 day' AND GREATEST(accounting_date,bucket_date) >= $2::date`, at, financialFactDate(start)); err != nil {
				return false, err
			}
		}
	}
	target := today.AddDate(0, 0, 1-financialFactDays())
	switch {
	case start.After(target):
		// Add rows whose later day is exactly the previous day, then extend.
		day := start.AddDate(0, 0, -1)
		where, args := financialUsageWhere(usagestats.UsageLogFilters{StartTime: &day, EndTime: &start})
		args = append(args, financialFactDate(day))
		if _, err := q.ExecContext(ctx, insert+where+fmt.Sprintf(" AND bucket_date <= $%d::date", len(args)), args...); err != nil {
			return false, err
		}
		if _, err := q.ExecContext(ctx, insert+`WHERE accounting_date < $3::date AND `+financialRollupRangeSQL("$1::timestamptz", "$2::timestamptz"), day, start, financialFactDate(day)); err != nil {
			return false, err
		}
		_, err := q.ExecContext(ctx, `UPDATE usage_financial_fact_state SET coverage_start=$1::date WHERE id=1`, financialFactDate(day))
		return day.After(target), err
	case start.Before(target):
		if _, err := q.ExecContext(ctx, `DELETE FROM usage_financial_facts WHERE accounting_date < $1::date AND bucket_date < $1::date`, financialFactDate(target)); err != nil {
			return false, err
		}
		_, err := q.ExecContext(ctx, `UPDATE usage_financial_fact_state SET coverage_start=$1::date WHERE id=1`, financialFactDate(target))
		return false, err
	}
	return false, nil
}

// financialFactView is one read snapshot of the materialized window.
type financialFactView struct {
	coverage time.Time
	receipts []int64
	logs     []int64
}

// financialFactSnapshot returns nil when facts cannot be used exactly in the
// current snapshot (not seeded, partition invalidation or large backlog).
func (r *usageLogRepository) financialFactSnapshot(ctx context.Context) (*financialFactView, error) {
	var cov sql.NullTime
	var pending, days int64
	if err := scanSingleRow(ctx, r.sql, `SELECT (SELECT coverage_start FROM usage_financial_fact_state WHERE id=1),(SELECT COUNT(*) FROM usage_financial_rollup_events),(SELECT COUNT(*) FROM usage_financial_rollup_events WHERE source='day')`, nil, &cov, &pending, &days); err != nil {
		return nil, err
	}
	if !cov.Valid || days > 0 || pending > financialFactPendingLimit {
		return nil, nil
	}
	view := &financialFactView{coverage: financialRollupDay(cov.Time)}
	if pending > 0 {
		var err error
		if view.receipts, view.logs, _, err = financialFactKeys(ctx, r.sql, nil); err != nil {
			return nil, err
		}
	}
	return view, nil
}

// facts returns materialized rows plus canonical rows for queued identities,
// restricted to the given extra condition (column names of the fact source).
func (v *financialFactView) facts(condition string) string {
	facts := "SELECT * FROM usage_financial_facts WHERE " + condition
	if len(v.receipts)+len(v.logs) == 0 {
		return facts
	}
	// Arrays are inlined so the planner sees exact identities, not a generic plan.
	keys := financialFactKeyCondition(v.receipts, v.logs)
	return facts + " AND NOT " + keys + " UNION ALL SELECT * FROM usage_financial_fact_source WHERE " + keys + " AND " + condition
}

// financialStatsRelation replaces the canonical statistics view for bounded
// readers. Callers must already be inside one repeatable-read snapshot.
func (r *usageLogRepository) financialStatsRelation(ctx context.Context, filters usagestats.UsageLogFilters) (string, error) {
	fallback := financialStatsSource(filters)
	if filters.StartTime == nil || filters.EndTime == nil || strings.TrimSpace(filters.RequestID) != "" {
		return fallback, nil
	}
	if _, ok := r.sql.(*sql.DB); ok {
		// Queue/state and facts must come from the reader's own snapshot.
		return fallback, nil
	}
	view, err := r.financialFactSnapshot(ctx)
	if err != nil || view == nil {
		return fallback, err
	}
	end, err := time.ParseInLocation("2006-01-02", financialExclusiveDate(*filters.EndTime), financialBeijing)
	if err != nil {
		return "", err
	}
	if !end.After(view.coverage) {
		return fallback, nil
	}
	cov := financialFactDate(view.coverage)
	relation := view.facts("accounting_date >= '" + cov + "'::date")
	startDay := financialRollupDay(filters.StartTime.In(financialBeijing))
	if startDay.Before(view.coverage) {
		at := view.coverage.Format(time.RFC3339)
		relation += " UNION ALL SELECT * FROM usage_financial_fact_source WHERE accounting_date < '" + cov + "'::date AND (financial_time_source <> 'log' OR created_at < '" + at + "'::timestamptz) AND (financial_time_source <> 'admitted' OR admitted_at < '" + at + "'::timestamptz)"
	}
	return "(" + relation + ") financial_rows", nil
}

// financialFactPageRelation is the materialized relation for a bounded usage
// page, or "" to page the canonical projection. Every row matching
// accounting_date >= start is materialized once start is inside coverage.
func (r *usageLogRepository) financialFactPageRelation(ctx context.Context, filters usagestats.UsageLogFilters) (string, error) {
	if filters.StartTime == nil || filters.EndTime == nil || strings.TrimSpace(filters.RequestID) != "" {
		return "", nil
	}
	if _, ok := r.sql.(*sql.DB); ok {
		return "", nil
	}
	view, err := r.financialFactSnapshot(ctx)
	if err != nil || view == nil {
		return "", err
	}
	if financialRollupDay(filters.StartTime.In(financialBeijing)).Before(view.coverage) {
		return "", nil
	}
	return "(" + view.facts("TRUE") + ")", nil
}

// financialAdminTailSQL is the unfiltered all-time tail after the rollup cursor,
// or "" to read it from the canonical view. Receipt rows come from facts; legacy
// rows keep their direct created_at index path in the total view.
func (r *usageLogRepository) financialAdminTailSQL(ctx context.Context, closed time.Time) (string, error) {
	view, err := r.financialFactSnapshot(ctx)
	if err != nil || view == nil || view.coverage.After(closed) {
		return "", err
	}
	// Legacy rows are read raw below, so only queued receipts need an overlay.
	receipts := (&financialFactView{coverage: view.coverage, receipts: view.receipts}).facts("fact_kind=1 AND bucket_date >= '" + financialFactDate(closed) + "'::date")
	return `SELECT ` + financialRollupRawColumns + ` FROM (` + receipts + `) tail_receipts UNION ALL SELECT ` + financialRollupRawColumns + ` FROM usage_financial_total_statistics WHERE financial_time_source<>'receipt' AND created_at >= '` + closed.Format(time.RFC3339) + `'::timestamptz`, nil
}

// Group all-time delta between settled receipts and their linked legacy logs.
// The split lateral lets PostgreSQL probe each identity index directly.
func financialGroupDeltaSQL(receiptRange string) string {
	return `WITH matched AS MATERIALIZED (
 SELECT r.group_id receipt_group,r.charged_amount,r.command,u.group_id log_group,u.actual_cost log_cost
 FROM usage_settlement_receipts r LEFT JOIN LATERAL (
  SELECT linked.group_id,linked.actual_cost FROM usage_logs linked
  WHERE r.usage_log_id=linked.id AND r.user_id=linked.user_id AND r.api_key_id=linked.api_key_id AND (r.usage_request_id IS NULL OR r.usage_request_id=linked.request_id)
  UNION ALL
  SELECT linked.group_id,linked.actual_cost FROM usage_logs linked
  WHERE r.usage_log_id IS NULL AND r.usage_request_id=linked.request_id AND r.user_id=linked.user_id AND r.api_key_id=linked.api_key_id
  LIMIT 1
 ) u ON TRUE WHERE r.state='settled' AND (` + receiptRange + `)
 ), deltas AS (
 SELECT COALESCE(receipt_group,log_group,0) group_id,COALESCE(charged_amount,0) amount FROM matched WHERE COALESCE(command->>'TerminalFailure','false')<>'true'
 UNION ALL SELECT COALESCE(log_group,0),-log_cost FROM matched WHERE log_cost IS NOT NULL
 ) SELECT group_id,SUM(amount) amount FROM deltas GROUP BY group_id`
}

func financialReceiptBucketSQL(start, end string) string {
	if end == "" {
		return "r.completed_at >= " + start + " OR (r.completed_at IS NULL AND (r.settled_at >= " + start + " OR r.settled_at IS NULL))"
	}
	return "(r.completed_at >= " + start + " AND r.completed_at < " + end + ") OR (r.completed_at IS NULL AND r.settled_at >= " + start + " AND r.settled_at < " + end + ")"
}

func refreshFinancialGroupDeltaDay(ctx context.Context, q sqlExecutor, day string, at time.Time) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM usage_financial_group_daily_deltas WHERE bucket_date=$1::date`, day); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO usage_financial_group_daily_deltas(bucket_date,group_id,amount) SELECT $1::date,group_id,amount FROM (`+financialGroupDeltaSQL(financialReceiptBucketSQL("$2::timestamptz", "$3::timestamptz"))+`) d`, day, at, at.AddDate(0, 0, 1)); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO usage_financial_group_delta_days(bucket_date) VALUES($1::date) ON CONFLICT DO NOTHING`, day)
	return err
}

// fillFinancialGroupDeltaDays materializes a few closed receipt days that have
// no marker yet (first deployment, restored backups). Returns true if it wrote.
func fillFinancialGroupDeltaDays(ctx context.Context, q sqlExecutor, closed time.Time) (bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT d::date FROM generate_series((SELECT (MIN(COALESCE(completed_at,settled_at)) AT TIME ZONE 'Asia/Shanghai')::date FROM usage_settlement_receipts WHERE state='settled')::timestamp,($1::date-1)::timestamp,INTERVAL '1 day') d
 WHERE NOT EXISTS(SELECT 1 FROM usage_financial_group_delta_days x WHERE x.bucket_date=d::date) ORDER BY 1 LIMIT 8`, financialFactDate(closed))
	if err != nil {
		return false, err
	}
	days := []time.Time{}
	for rows.Next() {
		var day time.Time
		if err = rows.Scan(&day); err != nil {
			rows.Close()
			return false, err
		}
		days = append(days, financialRollupDay(day))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, day := range days {
		if err = refreshFinancialGroupDeltaDay(ctx, q, financialFactDate(day), day); err != nil {
			return false, err
		}
	}
	return len(days) > 0, nil
}

// financialGroupDeltas sums materialized closed days that are neither dirty nor
// unmarked, and reads every other receipt day from source rows.
func (r *usageLogRepository) financialGroupDeltas(ctx context.Context) (map[int64]float64, error) {
	out := map[int64]float64{}
	var closed sql.NullTime
	if err := scanSingleRow(ctx, r.sql, `SELECT closed_before FROM usage_financial_rollup_state WHERE id=1`, nil, &closed); err != nil {
		return nil, err
	}
	collect := func(query string, args ...any) error {
		rows, err := r.sql.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var amount float64
			if err = rows.Scan(&id, &amount); err != nil {
				return err
			}
			out[id] += amount
		}
		return rows.Err()
	}
	if !closed.Valid {
		return out, collect(financialGroupDeltaSQL("TRUE"))
	}
	cursor := financialRollupDay(closed.Time)
	rows, err := r.sql.QueryContext(ctx, `SELECT DISTINCT d.bucket_date FROM usage_financial_rollup_events e
 CROSS JOIN LATERAL usage_financial_event_days(e.source,e.old_identity,e.new_identity) d WHERE d.bucket_date < $1::date
 UNION
 SELECT d::date FROM generate_series((SELECT (MIN(COALESCE(completed_at,settled_at)) AT TIME ZONE 'Asia/Shanghai')::date FROM usage_settlement_receipts WHERE state='settled')::timestamp,($1::date-1)::timestamp,INTERVAL '1 day') d
 WHERE NOT EXISTS(SELECT 1 FROM usage_financial_group_delta_days x WHERE x.bucket_date=d::date)`, financialFactDate(cursor))
	if err != nil {
		return nil, err
	}
	raw := []string{}
	for rows.Next() {
		var day time.Time
		if err = rows.Scan(&day); err != nil {
			rows.Close()
			return nil, err
		}
		raw = append(raw, financialFactDate(day))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = collect(`SELECT group_id,SUM(amount) FROM usage_financial_group_daily_deltas WHERE bucket_date < $1::date AND NOT(bucket_date=ANY($2::date[])) GROUP BY group_id`, financialFactDate(cursor), pq.Array(raw)); err != nil {
		return nil, err
	}
	ranges := []string{financialReceiptBucketSQL("$1::timestamptz", "")}
	args := []any{cursor}
	for _, value := range raw {
		day, e := time.ParseInLocation("2006-01-02", value, financialBeijing)
		if e != nil {
			return nil, e
		}
		args = append(args, day, day.AddDate(0, 0, 1))
		ranges = append(ranges, financialReceiptBucketSQL(fmt.Sprintf("$%d::timestamptz", len(args)-1), fmt.Sprintf("$%d::timestamptz", len(args))))
	}
	return out, collect(financialGroupDeltaSQL("("+strings.Join(ranges, ") OR (")+")"), args...)
}
