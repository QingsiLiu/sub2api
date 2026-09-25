package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// groupUsageRollupSnapshot 是一次汇总水位的快照。
// closedBefore / retainedFrom 在水位无效时退化为 1970-01-01，
// 与此前 SQL 里 CASE WHEN valid 的语义一一对应。
type groupUsageRollupSnapshot struct {
	valid        bool
	closedBefore string    // date，历史日桶的右开边界
	retainedDate string    // retained_from 在服务端时区内的日期，历史日桶的左闭边界
	tailStart    time.Time // 尾段（还没进日桶的那部分 usage_logs）的起点
}

// readGroupUsageRollupSnapshot 读一次汇总水位并在 Go 侧判定有效性。
//
// 判定条件与原先 SQL 里的 state_values CTE 完全一致：
// 恰好一行、时区名与当前服务端配置相同、且水位不在未来。
func (r *usageLogRepository) readGroupUsageRollupSnapshot(ctx context.Context, timezoneName, todayDate string) (groupUsageRollupSnapshot, error) {
	epoch := time.Unix(0, 0).UTC()
	invalid := groupUsageRollupSnapshot{
		closedBefore: "1970-01-01",
		retainedDate: "1970-01-01",
		tailStart:    epoch,
	}

	var rowCount int
	var closedBefore sql.NullString
	var retainedFrom sql.NullTime
	var stateTimezone sql.NullString
	if err := scanSingleRow(ctx, r.sql, `
		SELECT
			COUNT(*),
			MAX(closed_before)::text,
			MAX(retained_from),
			MAX(timezone_name)
		FROM usage_group_rollup_state
		WHERE id = 1
	`, nil, &rowCount, &closedBefore, &retainedFrom, &stateTimezone); err != nil {
		return groupUsageRollupSnapshot{}, fmt.Errorf("读取分组用量汇总水位: %w", err)
	}
	if rowCount != 1 || !closedBefore.Valid || !retainedFrom.Valid ||
		!stateTimezone.Valid || stateTimezone.String != timezoneName ||
		closedBefore.String > todayDate {
		return invalid, nil
	}

	tailStart, err := service.ParseGroupUsageDate(closedBefore.String)
	if err != nil {
		// 水位值解析不出来，按无效处理：宁可多扫一次，也不要算错钱。
		return invalid, nil //nolint:nilerr // 与上面的 valid 判定同语义，均降级为全量重算
	}
	return groupUsageRollupSnapshot{
		valid:        true,
		closedBefore: closedBefore.String,
		retainedDate: service.GroupUsageDate(retainedFrom.Time),
		tailStart:    tailStart.UTC(),
	}, nil
}

func (r *usageLogRepository) getAllGroupUsageSummaryFromRollups(ctx context.Context, todayStart time.Time) (results []usagestats.GroupUsageSummary, err error) {
	// geili: both the separately parameterized watermark and the dirty-day/raw
	// query must observe one snapshot. A consumer can commit between two RC reads.
	if db, ok := r.sql.(*sql.DB); ok {
		tx, beginErr := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if beginErr != nil {
			return nil, beginErr
		}
		defer func() { _ = tx.Rollback() }()
		txRepo := &usageLogRepository{sql: tx}
		results, err = txRepo.getAllGroupUsageSummaryFromRollups(ctx, todayStart)
		if err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return results, nil
	}
	todayStart = service.GroupUsageTodayStart(todayStart)
	yesterdayStart := service.GroupUsageYesterdayStart(todayStart)
	timezoneName := service.GroupUsageTimezoneName()
	todayDate := service.GroupUsageDate(todayStart)
	yesterdayDate := service.GroupUsageDate(yesterdayStart)

	state, err := r.readGroupUsageRollupSnapshot(ctx, timezoneName, todayDate)
	if err != nil {
		return nil, err
	}

	// 尾段起点必须以**查询参数**的形式给进来。
	//
	// 此前它是在同一条 SQL 里由 state CTE 算出、再 CROSS JOIN 给 tail 用的，
	// 于是 created_at 的下界对 planner 来说是个运行期才知道的值：既进不了
	// index cond，也估不准选择性，只能退化成 usage_logs 全表扫。
	// 生产实测（1721 万行 / PostgreSQL 17）：
	//   Seq Scan on usage_logs … rows=17212870, Rows Removed by Join Filter: 17178082
	//   Execution Time: 48720 ms
	// 换成参数之后走 idx_usage_logs_created_at：
	//   Index Scan … Index Cond: (created_at >= $7)
	//   Execution Time: 34 ms

	rows, err := r.sql.QueryContext(
		ctx,
		groupUsageRollupSummarySQLGeili,
		todayStart,
		yesterdayStart,
		yesterdayDate,
		state.valid,
		state.retainedDate,
		state.closedBefore,
		state.tailStart,
		timezoneName,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results = make([]usagestats.GroupUsageSummary, 0)
	for rows.Next() {
		var row usagestats.GroupUsageSummary
		if err := rows.Scan(&row.GroupID, &row.TotalCost, &row.TodayCost, &row.YesterdayCost); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

const groupUsageRollupSummarySQLGeili = `
		WITH dirty_days AS MATERIALIZED (
			SELECT DISTINCT (affected_at AT TIME ZONE $8::text)::date AS bucket_date
			FROM usage_group_rollup_invalidations
			WHERE $4::boolean AND affected_at < $7
		),
		historical AS (
			SELECT
				rollup.group_id,
				COALESCE(SUM(rollup.actual_cost), 0) AS actual_cost,
				COALESCE(SUM(rollup.actual_cost) FILTER (
					WHERE rollup.bucket_date = $3::date
				), 0) AS yesterday_cost
			FROM usage_group_daily_rollups rollup
			WHERE $4::boolean
				AND rollup.bucket_date >= $5::date
				AND rollup.bucket_date < $6::date
				AND NOT EXISTS (SELECT 1 FROM dirty_days d WHERE d.bucket_date = rollup.bucket_date)
			GROUP BY rollup.group_id
		),
		dirty_raw AS (
			SELECT ul.group_id,
				COALESCE(SUM(ul.actual_cost), 0) AS actual_cost,
				COALESCE(SUM(ul.actual_cost) FILTER (WHERE d.bucket_date = $3::date), 0) AS yesterday_cost
			FROM dirty_days d
			CROSS JOIN LATERAL (
				SELECT group_id, SUM(actual_cost) AS actual_cost FROM usage_logs
				WHERE created_at >= (d.bucket_date::timestamp AT TIME ZONE $8::text)
					AND created_at < ((d.bucket_date + 1)::timestamp AT TIME ZONE $8::text)
				GROUP BY group_id
			) ul
			GROUP BY ul.group_id
		),
		tail AS (
			SELECT
				ul.group_id,
				COALESCE(SUM(ul.actual_cost), 0) AS actual_cost,
				COALESCE(SUM(ul.actual_cost) FILTER (WHERE ul.created_at >= $1), 0) AS today_cost,
				COALESCE(SUM(ul.actual_cost) FILTER (
					WHERE ul.created_at >= $2
						AND ul.created_at < $1
				), 0) AS yesterday_cost
			FROM usage_logs ul
			WHERE ul.created_at >= $7
			GROUP BY ul.group_id
		)
		SELECT
			g.id AS group_id,
			COALESCE(historical.actual_cost, 0) + COALESCE(dirty_raw.actual_cost, 0) + COALESCE(tail.actual_cost, 0) AS total_cost,
			COALESCE(tail.today_cost, 0) AS today_cost,
			COALESCE(historical.yesterday_cost, 0) + COALESCE(dirty_raw.yesterday_cost, 0) + COALESCE(tail.yesterday_cost, 0) AS yesterday_cost
		FROM groups g
		LEFT JOIN historical ON historical.group_id = g.id
		LEFT JOIN dirty_raw ON dirty_raw.group_id = g.id
		LEFT JOIN tail ON tail.group_id = g.id
		ORDER BY g.id
	`

// SyncGroupUsageRollups publishes at most one calendar day per transaction.
// Only consumers lock the singleton; source writers append independent events.
func (r *dashboardAggregationRepository) SyncGroupUsageRollups(ctx context.Context, todayStart time.Time) error {
	if r == nil || r.sql == nil {
		return nil
	}
	todayStart = service.GroupUsageTodayStart(todayStart)
	if db, ok := r.sql.(*sql.DB); ok {
		serializationRetries := 0
		for {
			tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				return err
			}
			txRepo := newDashboardAggregationRepositoryWithSQL(tx)
			more, err := txRepo.syncGroupUsageRollupDayInTx(ctx, todayStart)
			if err != nil {
				_ = tx.Rollback()
			} else {
				err = tx.Commit()
			}
			if err != nil {
				// Two instances can take their RR snapshots before either gets
				// the consumer lock. Retry from a new snapshot; no event is lost.
				var pgErr *pq.Error
				if errors.As(err, &pgErr) && pgErr.Code == "40001" && serializationRetries < 5 && ctx.Err() == nil {
					serializationRetries++
					continue
				}
				return err
			}
			serializationRetries = 0
			if !more {
				return nil
			}
		}
	}
	// A supplied transaction is a single bounded step; its owner provides RR
	// isolation and commits. Never silently perform a multi-day transaction.
	_, err := r.syncGroupUsageRollupDayInTx(ctx, todayStart)
	return err
}

func (r *dashboardAggregationRepository) syncGroupUsageRollupDayInTx(ctx context.Context, todayStart time.Time) (bool, error) {
	var closedBefore, stateTimezoneName string
	var retainedFrom time.Time
	if err := scanSingleRow(ctx, r.sql, `
		SELECT closed_before::text, retained_from, timezone_name
		FROM usage_group_rollup_state WHERE id = 1 FOR UPDATE
	`, nil, &closedBefore, &retainedFrom, &stateTimezoneName); err != nil {
		return false, fmt.Errorf("读取分组用量汇总水位: %w", err)
	}
	todayDate := service.GroupUsageDate(todayStart)
	timezoneName := service.GroupUsageTimezoneName()
	timezoneChanged := stateTimezoneName != timezoneName
	if !timezoneChanged && closedBefore > todayDate {
		return false, fmt.Errorf("分组用量汇总水位位于未来: %s", closedBefore)
	}

	// MIN(created_at) is indexed. Initial and timezone rebuilds start at the
	// earliest retained record, not at the epoch, and commit after every day.
	if timezoneChanged || closedBefore == "1970-01-01" {
		var earliest sql.NullTime
		if err := scanSingleRow(ctx, r.sql, `SELECT MIN(created_at) FROM usage_logs`, nil, &earliest); err != nil {
			return false, err
		}
		retainedFrom = todayStart
		if earliest.Valid && earliest.Time.Before(todayStart) {
			retainedFrom = earliest.Time.UTC()
		}
		closedBefore = service.GroupUsageDate(retainedFrom)
		if _, err := r.sql.ExecContext(ctx, `DELETE FROM usage_group_daily_rollups`); err != nil {
			return false, err
		}
	}

	var dirtyAt sql.NullTime
	if err := scanSingleRow(ctx, r.sql, `
		SELECT MIN(affected_at) FROM usage_group_rollup_invalidations WHERE affected_at < $1
	`, []any{todayStart}, &dirtyAt); err != nil {
		return false, err
	}
	day := closedBefore
	if dirtyAt.Valid {
		dirtyDay := service.GroupUsageDate(dirtyAt.Time)
		if dirtyDay < day {
			day = dirtyDay
		}
	}
	if day < todayDate {
		dayStart, err := service.ParseGroupUsageDate(day)
		if err != nil {
			return false, err
		}
		dayEnd := dayStart.AddDate(0, 0, 1)
		if _, err := r.sql.ExecContext(ctx, `DELETE FROM usage_group_daily_rollups WHERE bucket_date = $1::date`, day); err != nil {
			return false, err
		}
		if _, err := r.sql.ExecContext(ctx, `
			INSERT INTO usage_group_daily_rollups (bucket_date, group_id, actual_cost, computed_at)
			SELECT $1::date, group_id, COALESCE(SUM(actual_cost), 0), NOW()
			FROM usage_logs
			WHERE group_id IS NOT NULL AND created_at >= $2 AND created_at < $3
			GROUP BY group_id
		`, day, dayStart.UTC(), dayEnd.UTC()); err != nil {
			return false, err
		}
		// Exact MVCC-visible IDs only. A lower sequence ID allocated by an
		// uncommitted writer remains invisible, survives and dirties the bucket.
		if _, err := r.sql.ExecContext(ctx, `
			WITH consumed AS MATERIALIZED (
				SELECT id FROM usage_group_rollup_invalidations WHERE affected_at >= $1 AND affected_at < $2
			)
			DELETE FROM usage_group_rollup_invalidations event USING consumed
			WHERE event.id = consumed.id
		`, dayStart.UTC(), dayEnd.UTC()); err != nil {
			return false, err
		}
		if day == closedBefore {
			closedBefore = service.GroupUsageDate(dayEnd)
		}
		if dayStart.Before(retainedFrom) {
			retainedFrom = dayStart.UTC()
		}
	}
	// Tail is always raw, so visible tail events need no cached-day rebuild.
	// Still delete by a captured ID set, not a sequence cutoff.
	if _, err := r.sql.ExecContext(ctx, `
		WITH consumed AS MATERIALIZED (
			SELECT id FROM usage_group_rollup_invalidations WHERE affected_at >= $1
		)
		DELETE FROM usage_group_rollup_invalidations event USING consumed
		WHERE event.id = consumed.id
	`, todayStart); err != nil {
		return false, err
	}
	if _, err := r.sql.ExecContext(ctx, `
		UPDATE usage_group_rollup_state SET closed_before = $1::date,
			retained_from = $2, timezone_name = $3, updated_at = NOW() WHERE id = 1
	`, closedBefore, retainedFrom, timezoneName); err != nil {
		return false, err
	}
	// Any historical day work gets a fresh transaction for the next check.
	return day < todayDate, nil
}

// Explicit range events cover operations (notably DROP PARTITION) that do not
// execute usage row triggers. Calendar stepping preserves DST day boundaries.
func invalidateGroupUsageRollupsRange(ctx context.Context, tx *sql.Tx, start, end time.Time) error {
	if !end.After(start) {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO usage_group_rollup_invalidations (affected_at)
		SELECT day::timestamp AT TIME ZONE $3::text
		FROM generate_series(
			($1::timestamptz AT TIME ZONE $3::text)::date::timestamp,
			(($2::timestamptz - INTERVAL '1 microsecond') AT TIME ZONE $3::text)::date::timestamp,
			INTERVAL '1 day'
		) AS day
	`, start.UTC(), end.UTC(), service.GroupUsageTimezoneName())
	return err
}
