package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/lib/pq"
)

const financialRollupColumnNames = `requests,input_tokens,output_tokens,cache_creation_tokens,cache_read_tokens,total_cost,actual_cost,account_cost,duration_sum,duration_count,balance_cost,subscription_cost,detail_pending,unknown_amount,incomplete_records,unknown_standard,unknown_tokens`
const financialRollupRawColumns = `COUNT(*) AS requests,
 COALESCE(SUM(input_tokens),0) AS input_tokens,COALESCE(SUM(output_tokens),0) AS output_tokens,
 COALESCE(SUM(cache_creation_tokens),0) AS cache_creation_tokens,COALESCE(SUM(cache_read_tokens),0) AS cache_read_tokens,
 COALESCE(SUM(total_cost),0) AS total_cost,COALESCE(SUM(actual_cost),0) AS actual_cost,
 COALESCE(SUM(COALESCE(account_stats_cost,total_cost)*COALESCE(account_rate_multiplier,1)),0) AS account_cost,
 COALESCE(SUM(duration_ms::numeric),0) AS duration_sum,COUNT(duration_ms) AS duration_count,
 COALESCE(SUM(actual_cost) FILTER(WHERE billing_type=0),0) AS balance_cost,
 COALESCE(SUM(actual_cost) FILTER(WHERE billing_type=1),0) AS subscription_cost,
 COUNT(*) FILTER(WHERE detail_pending) AS detail_pending,COUNT(*) FILTER(WHERE actual_cost IS NULL) AS unknown_amount,
 COUNT(*) FILTER(WHERE record_completeness<>'complete') AS incomplete_records,
 COUNT(*) FILTER(WHERE total_cost IS NULL) AS unknown_standard,
 COUNT(*) FILTER(WHERE input_tokens IS NULL OR output_tokens IS NULL OR cache_creation_tokens IS NULL OR cache_read_tokens IS NULL) AS unknown_tokens`
const financialRollupCombineColumns = `COALESCE(SUM(requests),0),
 COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(cache_creation_tokens),0),COALESCE(SUM(cache_read_tokens),0),
 COALESCE(SUM(total_cost),0),COALESCE(SUM(actual_cost),0),COALESCE(SUM(account_cost),0),
 COALESCE(SUM(duration_sum)/NULLIF(SUM(duration_count),0),0),
 COALESCE(SUM(balance_cost),0),COALESCE(SUM(subscription_cost),0),
 COALESCE(SUM(detail_pending),0),COALESCE(SUM(unknown_amount),0),COALESCE(SUM(incomplete_records),0),
 COALESCE(SUM(unknown_standard),0)=0,COALESCE(SUM(unknown_tokens),0)=0`

// Physical aggregation dates are internal only. Financial accounting/completion
// date filters still use the existing canonical financial projection unchanged.
func financialRollupRangeSQL(start, end string) string {
	receipt := "COALESCE(completed_at,settled_at) >= " + start
	log := "created_at >= " + start
	if end != "" {
		receipt += " AND COALESCE(completed_at,settled_at) < " + end
		log += " AND created_at < " + end
	}
	return "((financial_time_source='receipt' AND " + receipt + ") OR (financial_time_source<>'receipt' AND " + log + "))"
}

// financialAdminAllTimeTotals is deliberately restricted to unfiltered admin
// totals. Summary buckets must never satisfy model/user/endpoint predicates.
func (r *usageLogRepository) financialAdminAllTimeTotals(ctx context.Context) (*usagestats.UsageStats, error) {
	if db, ok := r.sql.(*sql.DB); ok {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		out, err := (&usageLogRepository{sql: tx}).financialAdminAllTimeTotals(ctx)
		if err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return out, nil
	}
	var first, closed sql.NullTime
	if err := scanSingleRow(ctx, r.sql, `SELECT start_date,closed_before FROM usage_financial_rollup_state WHERE id=1`, nil, &first, &closed); err != nil {
		return nil, err
	}
	if !first.Valid || !closed.Valid {
		return r.financialTotals(ctx, usagestats.UsageLogFilters{})
	}
	// Resolve exact dirty dates first in the SAME RR snapshot. Supplying known
	// timestamp parameters below avoids huge dirty-CTE cardinality estimates,
	// historical sequential scans and JIT compilation even when the queue is empty.
	dirtyRows, err := r.sql.QueryContext(ctx, `SELECT DISTINCT d.bucket_date FROM usage_financial_rollup_events e
 CROSS JOIN LATERAL usage_financial_event_days(e.source,e.old_identity,e.new_identity) d
 WHERE d.bucket_date < $1::date
 UNION
 SELECT d::date FROM generate_series($2::date::timestamp,($1::date-1)::timestamp,INTERVAL '1 day') d
 WHERE NOT EXISTS(SELECT 1 FROM usage_financial_daily_rollups b WHERE b.bucket_date=d::date)`, closed.Time.Format("2006-01-02"), first.Time.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	dirty := []string{}
	for dirtyRows.Next() {
		var day time.Time
		if err = dirtyRows.Scan(&day); err != nil {
			dirtyRows.Close()
			return nil, err
		}
		dirty = append(dirty, day.Format("2006-01-02"))
	}
	err = dirtyRows.Err()
	dirtyRows.Close()
	if err != nil {
		return nil, err
	}
	query, args, err := financialRollupTotalsQuery(closed.Time, dirty)
	if err != nil {
		return nil, err
	}
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return nil, err
		}
		return nil, sql.ErrNoRows
	}
	out, err := scanFinancialAggregate(rows)
	if err != nil {
		return nil, err
	}
	out.DateBasis = usagestats.FinancialDateAccounting
	return out, rows.Err()
}

func financialRollupTotalsQuery(closed time.Time, dirty []string) (string, []any, error) {
	args := []any{closed.Format("2006-01-02"), pq.Array(dirty), financialRollupDay(closed)}
	query := `WITH parts AS (SELECT ` + financialRollupColumnNames + ` FROM usage_financial_daily_rollups WHERE bucket_date<$1::date AND NOT(bucket_date=ANY($2::date[])) UNION ALL SELECT ` + financialRollupRawColumns + ` FROM usage_financial_total_records WHERE ` + financialRollupRangeSQL("$3::timestamptz", "")
	for _, day := range dirty {
		at, err := time.ParseInLocation("2006-01-02", day, financialBeijing)
		if err != nil {
			return "", nil, err
		}
		args = append(args, at, at.AddDate(0, 0, 1))
		query += ` UNION ALL SELECT ` + financialRollupRawColumns + ` FROM usage_financial_total_records WHERE ` + financialRollupRangeSQL(fmt.Sprintf("$%d::timestamptz", len(args)-1), fmt.Sprintf("$%d::timestamptz", len(args)))
	}
	return query + `) SELECT ` + financialRollupCombineColumns + ` FROM parts`, args, nil
}

func financialRollupDay(at time.Time) time.Time {
	// SQL DATE values may be returned in UTC; preserve their calendar components.
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, financialBeijing)
}

// SyncFinancialUsageRollups commits after each historical day and small source
// event batch. Only consumers lock state; writers append unrelated event rows.
func (r *dashboardAggregationRepository) SyncFinancialUsageRollups(ctx context.Context, now time.Time) error {
	todayAt := now.In(financialBeijing)
	today := time.Date(todayAt.Year(), todayAt.Month(), todayAt.Day(), 0, 0, 0, 0, financialBeijing)
	db, ok := r.sql.(*sql.DB)
	if !ok {
		_, err := r.syncFinancialRollupStep(ctx, today)
		return err
	}
	retries := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			return err
		}
		more, err := newDashboardAggregationRepositoryWithSQL(tx).syncFinancialRollupStep(ctx, today)
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			var pg *pq.Error
			if errors.As(err, &pg) && pg.Code == "40001" && retries < 5 {
				retries++
				continue
			}
			return err
		}
		retries = 0
		if !more {
			return nil
		}
	}
}

func (r *dashboardAggregationRepository) syncFinancialRollupStep(ctx context.Context, today time.Time) (bool, error) {
	var first, closed sql.NullTime
	if err := scanSingleRow(ctx, r.sql, `SELECT start_date,closed_before FROM usage_financial_rollup_state WHERE id=1 FOR UPDATE`, nil, &first, &closed); err != nil {
		return false, err
	}
	if !closed.Valid {
		var earliest sql.NullTime
		if err := scanSingleRow(ctx, r.sql, `SELECT MIN(at) FROM (SELECT MIN(created_at) at FROM usage_logs UNION ALL SELECT MIN(COALESCE(completed_at,settled_at)) FROM usage_settlement_receipts WHERE state='settled') dates`, nil, &earliest); err != nil {
			return false, err
		}
		at := today
		if earliest.Valid && earliest.Time.Before(today) {
			civil := earliest.Time.In(financialBeijing)
			at = financialRollupDay(civil)
		}
		first = sql.NullTime{Time: at, Valid: true}
		closed = first
	}
	cursor := financialRollupDay(closed.Time)
	if cursor.After(today) {
		return false, fmt.Errorf("financial rollup cursor is in the future: %s", cursor.Format("2006-01-02"))
	}
	days := map[string]bool{}
	// Empty days are materialized too. Repair holes after an older recovery
	// expands the known range, or an interrupted/manual bucket removal.
	var missing sql.NullTime
	if err := scanSingleRow(ctx, r.sql, `SELECT MIN(d::date) FROM generate_series($1::date::timestamp,($2::date-1)::timestamp,INTERVAL '1 day') d WHERE NOT EXISTS(SELECT 1 FROM usage_financial_daily_rollups b WHERE b.bucket_date=d::date)`, []any{first.Time.Format("2006-01-02"), cursor.Format("2006-01-02")}, &missing); err != nil {
		return false, err
	}
	if missing.Valid {
		days[missing.Time.Format("2006-01-02")] = true
	}
	expanded := false
	backfill := cursor.Before(today)
	if backfill {
		days[cursor.Format("2006-01-02")] = true
	}
	// Exact selected IDs, never a sequence watermark. A lower ID belonging to an
	// uncommitted source transaction remains invisible and survives this batch.
	rows, err := r.sql.QueryContext(ctx, `SELECT id FROM usage_financial_rollup_events ORDER BY id LIMIT 32`)
	if err != nil {
		return false, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(ids) > 0 {
		rows, err = r.sql.QueryContext(ctx, `SELECT DISTINCT d.bucket_date FROM usage_financial_rollup_events e CROSS JOIN LATERAL usage_financial_event_days(e.source,e.old_identity,e.new_identity) d WHERE e.id=ANY($1) AND d.bucket_date<$2::date`, pq.Array(ids), today.Format("2006-01-02"))
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var day time.Time
			if err = rows.Scan(&day); err != nil {
				rows.Close()
				return false, err
			}
			days[day.Format("2006-01-02")] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, err
		}
	}
	ordered := make([]string, 0, len(days))
	for day := range days {
		ordered = append(ordered, day)
	}
	sort.Strings(ordered)
	for _, day := range ordered {
		at, err := time.ParseInLocation("2006-01-02", day, financialBeijing)
		if err != nil {
			return false, err
		}
		if at.Before(financialRollupDay(first.Time)) {
			first.Time = at
			expanded = true
		}
		if _, err = r.sql.ExecContext(ctx, `DELETE FROM usage_financial_daily_rollups WHERE bucket_date=$1::date`, day); err != nil {
			return false, err
		}
		query := `INSERT INTO usage_financial_daily_rollups(bucket_date,` + financialRollupColumnNames + `) SELECT $1::date,` + financialRollupRawColumns + ` FROM usage_financial_total_records WHERE ` + financialRollupRangeSQL("$2::timestamptz", "$3::timestamptz")
		if _, err = r.sql.ExecContext(ctx, query, day, at, at.AddDate(0, 0, 1)); err != nil {
			return false, err
		}
	}
	// Tail days need no cached rows: readers always scan the tail. Historical
	// dependencies above have ALL been published before acknowledging each event.
	if len(ids) > 0 {
		if _, err = r.sql.ExecContext(ctx, `DELETE FROM usage_financial_rollup_events WHERE id=ANY($1)`, pq.Array(ids)); err != nil {
			return false, err
		}
	}
	if backfill {
		cursor = cursor.AddDate(0, 0, 1)
	}
	if _, err = r.sql.ExecContext(ctx, `UPDATE usage_financial_rollup_state SET start_date=$1::date,closed_before=$2::date WHERE id=1`, first.Time.Format("2006-01-02"), cursor.Format("2006-01-02")); err != nil {
		return false, err
	}
	return cursor.Before(today) || len(ids) == 32 || missing.Valid || expanded, nil
}

// Partition removal bypasses row triggers. Capture receipt dependencies BEFORE
// deleting the logs, and publish these independent events in the same transaction.
func invalidateFinancialRollupsRange(ctx context.Context, tx *sql.Tx, start, end time.Time) error {
	if !end.After(start) {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO usage_financial_rollup_events(source,new_identity)
 SELECT 'day',jsonb_build_object('at',day::timestamp AT TIME ZONE 'Asia/Shanghai') FROM generate_series(($1::timestamptz AT TIME ZONE 'Asia/Shanghai')::date::timestamp,(($2::timestamptz-INTERVAL '1 microsecond') AT TIME ZONE 'Asia/Shanghai')::date::timestamp,INTERVAL '1 day') day
 UNION ALL
 SELECT 'log',jsonb_build_object('id',u.id,'user',u.user_id,'key',u.api_key_id,'request',u.request_id,'at',u.created_at)
 FROM usage_logs u WHERE u.created_at >= $1 AND u.created_at < $2
 AND EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.state='settled' AND r.user_id=u.user_id AND r.api_key_id=u.api_key_id AND ((r.usage_request_id=u.request_id AND (r.usage_log_id IS NULL OR r.usage_log_id=u.id)) OR (r.usage_request_id IS NULL AND r.usage_log_id=u.id)))`, start, end)
	return err
}
