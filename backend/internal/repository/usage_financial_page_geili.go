package repository

import (
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// financialUsagePageQuery paginates scalar identities before loading receipt
// JSON and wide usage rows. Both stages use the same statement/MVCC snapshot.
// Financial amounts, unknown fields and stable receipt/log deduplication remain
// owned by the canonical projection, never reconstructed from current Key data.
func financialUsagePageQuery(params pagination.PaginationParams, filters usagestats.UsageLogFilters, where string, argCount int) string {
	order := strings.ToUpper(params.NormalizedSortOrder(pagination.SortOrderDesc))
	column := "created_at"
	switch params.SortBy {
	case "model":
		column = "COALESCE(NULLIF(TRIM(requested_model), ''), model)"
	case "accounting_date", "completed_at", "created_at", "actual_cost", "id":
		column = params.SortBy
	}
	source := financialStatsSource(filters)
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
	if source == financialUsageTable && filters.UserID == 0 && filters.APIKeyID == 0 && filters.AccountID == 0 && len(filters.AccountIDs) == 0 && filters.SubscriptionID == 0 && filters.GroupID == 0 && filters.Model == "" && filters.RequestID == "" && filters.BillingType == nil && filters.RequestType == nil && filters.Stream == nil && filters.BillingMode == "" && filters.NativeCompactionV2 == nil && filters.UpstreamModelMismatch == nil && column == "created_at" && order == "DESC" &&
		(filters.EndTime == nil || !filters.EndTime.Before(time.Now())) {
		// Recent pages should walk the raw log time index and probe canonical
		// accounting membership only for candidate rows. A full day may contain
		// hundreds of thousands of rows; it must not be sorted just for page one.
		predicate := " WHERE "
		if where != "" {
			predicate = where + " AND "
		}
		// Bound the physical probe. After every new request has a receipt there
		// may be no legacy row today; an unbounded backwards scan would then
		// traverse the entire historical table. If the probe is insufficient,
		// use the exact date-indexed canonical fallback instead.
		need := fmt.Sprintf("($%d::bigint+$%d::bigint)", argCount+1, argCount+2)
		prefix = fmt.Sprintf(`legacy_probe AS MATERIALIZED (
 SELECT f.id,f.financial_receipt_id,u.created_at AS financial_sort
 FROM (SELECT id,created_at FROM usage_logs ORDER BY created_at DESC,id DESC LIMIT GREATEST(1000,%s*4)) u
 CROSS JOIN LATERAL (SELECT id,financial_receipt_id FROM usage_financial_records f %s
 f.financial_receipt_id IS NULL AND f.id=u.id OFFSET 0) f
 ORDER BY u.created_at DESC,u.id DESC LIMIT %s
 ), `, need, predicate, need)
		identities = fmt.Sprintf(`(SELECT id,financial_receipt_id,created_at AS financial_sort FROM usage_financial_records f %s financial_time_source='receipt' ORDER BY financial_sort DESC NULLS LAST,id DESC LIMIT %s)
 UNION ALL (SELECT id,financial_receipt_id,financial_sort FROM legacy_probe WHERE (SELECT COUNT(*) FROM legacy_probe)>=%s)
 UNION ALL (SELECT id,financial_receipt_id,created_at AS financial_sort FROM usage_financial_records f %s financial_receipt_id IS NULL AND (SELECT COUNT(*) FROM legacy_probe)<%s ORDER BY financial_sort DESC NULLS LAST,id DESC LIMIT %s)`, predicate, need, need, predicate, need, need)

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
