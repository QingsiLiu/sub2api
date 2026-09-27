package repository

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// financialUsagePageQuery paginates scalar identities before loading receipt
// JSON and wide usage rows. Both stages use the same statement/MVCC snapshot.
// Financial amounts, unknown fields and stable receipt/log deduplication remain
// owned by the canonical projection, never reconstructed from current Key data.
// facts, when set, is a materialized relation covering every row the filters
// can match (see financialFactPageRelation); page identities then come from it.
func financialUsagePageQuery(params pagination.PaginationParams, filters usagestats.UsageLogFilters, where string, argCount int, facts string) string {
	order := strings.ToUpper(params.NormalizedSortOrder(pagination.SortOrderDesc))
	column := "created_at"
	switch params.SortBy {
	case "model":
		column = "COALESCE(NULLIF(TRIM(requested_model), ''), model)"
	case "accounting_date", "completed_at", "created_at", "actual_cost", "id":
		column = params.SortBy
	}
	source := financialPageSource(filters)
	if params.SortBy == "accounting_date" {
		source = financialUsageTable
	}
	prefix := ""
	identities := fmt.Sprintf("SELECT id,financial_receipt_id,%s AS financial_sort FROM %s f %s", column, source, where)
	if source == "usage_financial_total_records" {
		// For unbounded history, independently limit each disjoint source before
		// merging. Legacy rows can walk the existing created_at index; sorting
		// the whole union would otherwise read millions of wide historic rows.
		predicate := " WHERE "
		if where != "" {
			predicate = where + " AND "
		}
		legacyNulls := " NULLS LAST"
		if column == "created_at" && order == "DESC" {
			legacyNulls = ""
		} // raw created_at is NOT NULL
		identities = fmt.Sprintf(`(SELECT id,financial_receipt_id,%s AS financial_sort FROM %s f %s financial_time_source='receipt' ORDER BY financial_sort %s NULLS LAST,id %s LIMIT ($%d::bigint+$%d::bigint))
 UNION ALL (SELECT id,financial_receipt_id,%s AS financial_sort FROM %s f %s financial_time_source='log' ORDER BY financial_sort %s%s,id %s LIMIT ($%d::bigint+$%d::bigint))`, column, source, predicate, order, order, argCount+1, argCount+2, column, source, predicate, order, legacyNulls, order, argCount+1, argCount+2)
	}
	if facts != "" {
		// Facts carry the same row identities; only receipt rows (kind 1) have
		// financial_receipt_id, and it is their source_id.
		identities = fmt.Sprintf("SELECT id,CASE WHEN fact_kind=1 THEN source_id END AS financial_receipt_id,%s AS financial_sort FROM %s f %s", column, facts, where)
	} else if column == "created_at" && order == "DESC" {
		predicate := " WHERE "
		if where != "" {
			predicate = where + " AND "
		}
		need := fmt.Sprintf("($%d::bigint+$%d::bigint)", argCount+1, argCount+2)
		rawScope := ""
		if filters.UserID > 0 {
			rawScope += fmt.Sprintf(" AND u.user_id=%d", filters.UserID)
		}
		if filters.APIKeyID > 0 {
			rawScope += fmt.Sprintf(" AND u.api_key_id=%d", filters.APIKeyID)
		}
		// If K timestamped receipts already qualify, older legacy rows cannot
		// enter the first K of the union. Probe the raw time index only above
		// that exact cutoff (inclusive for ties), then apply canonical filters.
		// Unlike a fixed lookback/sample, this cannot omit a qualifying record.
		// Null timestamps or fewer receipts take the full exact fallback.
		prefix = fmt.Sprintf(`receipt_page AS MATERIALIZED (
 SELECT id,financial_receipt_id,created_at AS financial_sort FROM %s f %s financial_time_source='receipt'
 ORDER BY financial_sort DESC NULLS LAST,id DESC LIMIT %s
 ), receipt_cutoff AS MATERIALIZED (
 SELECT COUNT(financial_sort)>=%s AS bounded,MIN(financial_sort) AS oldest FROM receipt_page
 ), `, source, predicate, need, need)
		identities = fmt.Sprintf(`(SELECT id,financial_receipt_id,financial_sort FROM receipt_page)
 UNION ALL (SELECT f.id,f.financial_receipt_id,u.created_at AS financial_sort
 FROM usage_logs u CROSS JOIN LATERAL (
 SELECT id,financial_receipt_id FROM usage_financial_records f %s
 f.financial_receipt_id IS NULL AND f.id=u.id OFFSET 0) f
 WHERE (SELECT bounded FROM receipt_cutoff) AND u.created_at >= (SELECT oldest FROM receipt_cutoff) %s
 ORDER BY u.created_at DESC,u.id DESC LIMIT %s)
 UNION ALL (SELECT id,financial_receipt_id,created_at AS financial_sort FROM %s f %s
 financial_receipt_id IS NULL AND NOT (SELECT bounded FROM receipt_cutoff)
 ORDER BY financial_sort DESC NULLS LAST,id DESC LIMIT %s)`, predicate, rawScope, need, source, predicate, need)
	}
	return fmt.Sprintf(`WITH %sfinancial_page AS MATERIALIZED (
 %s ORDER BY financial_sort %s NULLS LAST,id %s LIMIT $%d OFFSET $%d
 ), hydrated AS (
 SELECT p.financial_sort,p.id AS financial_id,to_jsonb(f) AS record
 FROM financial_page p CROSS JOIN LATERAL (
 SELECT * FROM usage_financial_records f WHERE p.financial_receipt_id IS NOT NULL
 AND f.financial_receipt_id=p.financial_receipt_id OFFSET 0
 ) f
 UNION ALL
 SELECT p.financial_sort,p.id AS financial_id,to_jsonb(f) AS record
 FROM financial_page p CROSS JOIN LATERAL (
 SELECT * FROM usage_financial_records f WHERE p.financial_receipt_id IS NULL
 AND f.financial_receipt_id IS NULL AND f.id=p.id OFFSET 0
 ) f
 ) SELECT record FROM hydrated ORDER BY financial_sort %s NULLS LAST,financial_id %s`, prefix, identities, order, order, argCount+1, argCount+2, order, order)
}
