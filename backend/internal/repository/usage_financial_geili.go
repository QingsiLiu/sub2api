package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const financialUsageTable = "usage_financial_records"

func financialStatsSource(filters usagestats.UsageLogFilters) string {
	if filters.StartTime == nil && filters.EndTime == nil {
		return "usage_financial_total_records"
	}
	return financialUsageTable
}

var financialBeijing = time.FixedZone("Asia/Shanghai", 8*60*60)

func financialTimeColumn(filters usagestats.UsageLogFilters) string {
	if usagestats.NormalizeFinancialDateBasis(filters.DateBasis) == usagestats.FinancialDateCompleted {
		return "completed_at"
	}
	return "accounting_date"
}

// All financial paths share these predicates. Accounting dates are civil dates
// in Beijing; the caller's session/DB timezone cannot move a charge to another day.
func financialUsageWhere(filters usagestats.UsageLogFilters) (string, []any) {
	conditions := []string{}
	args := []any{}
	add := func(column string, value any) {
		conditions = append(conditions, fmt.Sprintf("%s = $%d", column, len(args)+1))
		args = append(args, value)
	}
	if filters.UserID > 0 {
		add("user_id", filters.UserID)
	}
	if filters.APIKeyID > 0 {
		add("api_key_id", filters.APIKeyID)
	}
	if filters.AccountID > 0 {
		add("account_id", filters.AccountID)
	}
	conditions, args = appendUsageAccountIDsWhereGeili(conditions, args, filters.AccountIDs, "account_id")
	if filters.SubscriptionID > 0 {
		add("subscription_id", filters.SubscriptionID)
	}
	if filters.GroupID > 0 {
		add("group_id", filters.GroupID)
	}
	if requestID := strings.TrimSpace(filters.RequestID); requestID != "" {
		add("request_id", requestID)
	}
	conditions, args = appendUsageLogModelWhereCondition(conditions, args, filters.Model, filters.ModelFilterSource)
	conditions, args = appendRequestTypeOrStreamWhereCondition(conditions, args, filters.RequestType, filters.Stream)
	conditions, args = appendNativeCompactionV2WhereCondition(conditions, args, filters.NativeCompactionV2, "")
	if filters.BillingType != nil {
		add("billing_type", int16(*filters.BillingType))
	}
	conditions, args = appendUsageLogBillingModeWhereCondition(conditions, args, filters.BillingMode)
	if filters.BillingMode == string(service.BillingModeToken) {
		conditions = append(conditions, "(record_completeness='complete' OR billing_mode='token')")
	}

	if filters.UpstreamModelMismatch != nil {
		conditions = append(conditions, upstreamModelMismatchCondition("upstream_model_mismatch", *filters.UpstreamModelMismatch))
	}
	column := financialTimeColumn(filters)
	for _, bound := range []struct {
		value *time.Time
		op    string
	}{{filters.StartTime, ">="}, {filters.EndTime, "<"}} {
		if bound.value == nil {
			continue
		}
		var value any = *bound.value
		cast := ""
		if column == "accounting_date" {
			civil := bound.value.In(financialBeijing)
			if bound.op == "<" && (civil.Hour() != 0 || civil.Minute() != 0 || civil.Second() != 0 || civil.Nanosecond() != 0) {
				civil = civil.AddDate(0, 0, 1)
			}
			value = civil.Format("2006-01-02")
			cast = "::date"
		}
		conditions = append(conditions, fmt.Sprintf("%s %s $%d%s", column, bound.op, len(args)+1, cast))
		args = append(args, value)
		if column == "accounting_date" {
			day, _ := time.ParseInLocation("2006-01-02", value.(string), financialBeijing)
			args = append(args, day)
			pos := len(args)
			conditions = append(conditions, fmt.Sprintf("(financial_time_source <> 'log' OR created_at %s $%d::timestamptz)", bound.op, pos))
			conditions = append(conditions, fmt.Sprintf("(financial_time_source <> 'admitted' OR admitted_at %s $%d::timestamptz)", bound.op, pos))
		}
	}
	return buildWhere(conditions), args
}

func financialRange(filters usagestats.UsageLogFilters, start, end time.Time) usagestats.UsageLogFilters {
	filters.StartTime = &start
	filters.EndTime = &end
	return filters
}

func financialOrder(params pagination.PaginationParams) string {
	order := strings.ToUpper(params.NormalizedSortOrder(pagination.SortOrderDesc))
	column := "created_at"
	switch params.SortBy {
	case "model":
		column = "COALESCE(NULLIF(TRIM(requested_model), ''), model)"
	case "accounting_date", "completed_at", "created_at", "actual_cost", "id":
		column = params.SortBy
	}
	return column + " " + order + " NULLS LAST, id " + order
}

func (r *usageLogRepository) ListFinancialUsage(ctx context.Context, params pagination.PaginationParams, filters usagestats.UsageLogFilters) ([]service.UsageLog, *pagination.PaginationResult, error) {
	where, args := financialUsageWhere(filters)
	// Preserve the existing administrator fast-total contract. A blank admin
	// listing must not add a multi-million-row COUNT on every pagination request.
	fast := shouldUseFastUsageLogTotal(filters)
	var total int64
	if !fast {
		if err := scanSingleRow(ctx, r.sql, "SELECT COUNT(*) FROM "+financialUsageTable+" "+where, args, &total); err != nil {
			return nil, nil, err
		}
	}
	limit := params.Limit()
	if fast {
		limit++
	}
	query := fmt.Sprintf("SELECT to_jsonb(page) FROM (SELECT * FROM %s f %s ORDER BY %s LIMIT $%d OFFSET $%d) page", financialUsageTable, where, financialOrder(params), len(args)+1, len(args)+2)
	logs, err := r.queryFinancialUsage(ctx, query, append(args, limit, params.Offset())...)
	if err != nil {
		return nil, nil, err
	}
	if fast {
		more := len(logs) > params.Limit()
		if more {
			logs = logs[:params.Limit()]
		}
		total = int64(params.Offset() + len(logs))
		if more {
			total++
		}
	}
	if r.client != nil {
		if err = r.hydrateUsageLogAssociations(ctx, logs); err != nil {
			return nil, nil, err
		}
	}
	return logs, paginationResultFromTotal(total, params), nil
}

func (r *usageLogRepository) GetFinancialUsageByID(ctx context.Context, id int64) (*service.UsageLog, error) {
	logs, err := r.queryFinancialUsage(ctx, "SELECT to_jsonb(f) FROM "+financialUsageTable+" f WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	if len(logs) == 0 {
		return nil, service.ErrUsageLogNotFound
	}
	if len(logs) != 1 {
		return nil, fmt.Errorf("ambiguous financial usage identity %d", id)
	}
	if r.client != nil {
		if err = r.hydrateUsageLogAssociations(ctx, logs); err != nil {
			return nil, err
		}
	}
	return &logs[0], nil
}

func (r *usageLogRepository) queryFinancialUsage(ctx context.Context, query string, args ...any) ([]service.UsageLog, error) {
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	logs := make([]service.UsageLog, 0)
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		record, e := decodeFinancialUsage(raw)
		if e != nil {
			return nil, e
		}
		logs = append(logs, *record)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return logs, nil
}

func decodeFinancialUsage(raw []byte) (*service.UsageLog, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	log := &service.UsageLog{Financial: &service.UsageFinancialMetadata{UnknownFields: []string{}}}
	targets := map[string]any{
		"id": &log.ID, "user_id": &log.UserID, "api_key_id": &log.APIKeyID, "account_id": &log.AccountID,
		"request_id": &log.RequestID, "model": &log.Model, "requested_model": &log.RequestedModel,
		"upstream_model": &log.UpstreamModel, "upstream_response_model": &log.UpstreamResponseModel, "upstream_model_mismatch": &log.UpstreamModelMismatch,
		"group_id": &log.GroupID, "subscription_id": &log.SubscriptionID,
		"input_tokens": &log.InputTokens, "output_tokens": &log.OutputTokens, "cache_creation_tokens": &log.CacheCreationTokens, "cache_read_tokens": &log.CacheReadTokens,
		"cache_creation_5m_tokens": &log.CacheCreation5mTokens, "cache_creation_1h_tokens": &log.CacheCreation1hTokens,
		"image_input_tokens": &log.ImageInputTokens, "image_output_tokens": &log.ImageOutputTokens, "image_input_cost": &log.ImageInputCost, "image_output_cost": &log.ImageOutputCost,
		"input_cost": &log.InputCost, "output_cost": &log.OutputCost, "cache_creation_cost": &log.CacheCreationCost, "cache_read_cost": &log.CacheReadCost,
		"total_cost": &log.TotalCost, "actual_cost": &log.ActualCost, "rate_multiplier": &log.RateMultiplier, "account_rate_multiplier": &log.AccountRateMultiplier, "account_stats_cost": &log.AccountStatsCost,
		"billing_type": &log.BillingType, "request_type": &log.RequestType, "stream": &log.Stream, "openai_ws_mode": &log.OpenAIWSMode, "native_compaction_v2": &log.NativeCompactionV2,
		"duration_ms": &log.DurationMs, "first_token_ms": &log.FirstTokenMs, "user_agent": &log.UserAgent, "ip_address": &log.IPAddress, "session_id": &log.SessionID, "upstream_request_id": &log.UpstreamRequestID,
		"image_count": &log.ImageCount, "image_size": &log.ImageSize, "image_input_size": &log.ImageInputSize, "image_output_size": &log.ImageOutputSize, "image_size_source": &log.ImageSizeSource, "image_size_breakdown": &log.ImageSizeBreakdown, "media_type": &log.MediaType,
		"video_count": &log.VideoCount, "video_resolution": &log.VideoResolution, "video_duration_seconds": &log.VideoDurationSeconds,
		"service_tier": &log.ServiceTier, "reasoning_effort": &log.ReasoningEffort, "requested_reasoning_effort": &log.RequestedReasoningEffort,
		"inbound_endpoint": &log.InboundEndpoint, "upstream_endpoint": &log.UpstreamEndpoint, "cache_ttl_overridden": &log.CacheTTLOverridden, "long_context_billing_applied": &log.LongContextBillingApplied,
		"channel_id": &log.ChannelID, "model_mapping_chain": &log.ModelMappingChain, "billing_tier": &log.BillingTier, "billing_mode": &log.BillingMode, "route_billing_snapshot": &log.RouteBillingSnapshot,
		"created_at": &log.CreatedAt,
	}
	for name, target := range targets {
		value := fields[name]
		if len(value) == 0 || string(value) == "null" {
			continue
		}
		if err := json.Unmarshal(value, target); err != nil {
			return nil, fmt.Errorf("decode financial usage %s: %w", name, err)
		}
	}
	if value := fields["request_type"]; len(value) > 0 && string(value) != "null" {
		log.RequestType = log.EffectiveRequestType()
		log.Stream, log.OpenAIWSMode = service.ApplyLegacyRequestFields(log.RequestType, log.Stream, log.OpenAIWSMode)
	}
	if err := json.Unmarshal(raw, log.Financial); err != nil {
		return nil, err
	}
	for _, name := range financialNullableFields {
		value := fields[name]
		if len(value) == 0 || string(value) == "null" {
			log.Financial.UnknownFields = append(log.Financial.UnknownFields, name)
		}
	}
	// A known requested model can legitimately differ from the billing model.
	// An unknown requested model must not erase a known historical model fallback.
	if strings.TrimSpace(log.RequestedModel) == "" {
		log.RequestedModel = log.Model
	}
	if log.RequestedModel != "" {
		log.Financial.UnknownFields = removeFinancialUnknown(log.Financial.UnknownFields, "model")
	}
	return log, nil
}

var financialNullableFields = []string{"created_at", "account_id", "model", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "input_cost", "output_cost", "cache_creation_cost", "cache_read_cost", "total_cost", "actual_cost", "rate_multiplier", "image_count", "image_input_tokens", "image_output_tokens", "image_input_cost", "image_output_cost", "video_count", "request_type", "stream", "openai_ws_mode", "native_compaction_v2", "cache_ttl_overridden", "long_context_billing_applied"}

func removeFinancialUnknown(fields []string, name string) []string {
	out := fields[:0]
	for _, field := range fields {
		if field != name {
			out = append(out, field)
		}
	}
	return out
}

const financialAggregateColumns = `COUNT(*),
 COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),
 COALESCE(SUM(cache_creation_tokens),0),COALESCE(SUM(cache_read_tokens),0),
 COALESCE(SUM(total_cost),0),COALESCE(SUM(actual_cost),0),
 COALESCE(SUM(COALESCE(account_stats_cost,total_cost)*COALESCE(account_rate_multiplier,1)),0),
 COALESCE(AVG(duration_ms),0),
 COALESCE(SUM(actual_cost) FILTER(WHERE billing_type=0),0),
 COALESCE(SUM(actual_cost) FILTER(WHERE billing_type=1),0),
 COUNT(*) FILTER(WHERE detail_pending),COUNT(*) FILTER(WHERE actual_cost IS NULL),
 COUNT(*) FILTER(WHERE record_completeness<>'complete'),
 COUNT(*) FILTER(WHERE total_cost IS NULL)=0,
 COUNT(*) FILTER(WHERE input_tokens IS NULL OR output_tokens IS NULL OR cache_creation_tokens IS NULL OR cache_read_tokens IS NULL)=0`

func scanFinancialAggregate(scanner interface{ Scan(...any) error }, prefix ...any) (*usagestats.UsageStats, error) {
	stats := &usagestats.UsageStats{}
	var accountCost float64
	dest := append(prefix, &stats.TotalRequests, &stats.TotalInputTokens, &stats.TotalOutputTokens, &stats.TotalCacheCreationTokens, &stats.TotalCacheReadTokens, &stats.TotalCost, &stats.TotalActualCost, &accountCost, &stats.AverageDurationMs, &stats.BalanceActualCost, &stats.SubscriptionActualCost, &stats.DetailPendingCount, &stats.UnknownAmountCount, &stats.IncompleteRecordCount, &stats.StandardCostComplete, &stats.TokenCountsComplete)
	if err := scanner.Scan(dest...); err != nil {
		return nil, err
	}
	stats.TotalCacheTokens = stats.TotalCacheCreationTokens + stats.TotalCacheReadTokens
	stats.TotalTokens = stats.TotalInputTokens + stats.TotalOutputTokens + stats.TotalCacheTokens
	stats.TotalAccountCost = &accountCost
	return stats, nil
}

// financialTotals reads only a single aggregate, avoiding endpoint grouping work
// for summary cards and preserving index-only opportunities on old usage rows.
func (r *usageLogRepository) financialTotals(ctx context.Context, filters usagestats.UsageLogFilters) (*usagestats.UsageStats, error) {
	where, args := financialUsageWhere(filters)
	rows, err := r.sql.QueryContext(ctx, "SELECT "+financialAggregateColumns+" FROM "+financialStatsSource(filters)+" "+where, args...)
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
	stats, err := scanFinancialAggregate(rows)
	if err != nil {
		return nil, err
	}
	stats.DateBasis = usagestats.NormalizeFinancialDateBasis(filters.DateBasis)
	return stats, rows.Err()
}

func (r *usageLogRepository) GetFinancialUsageStats(ctx context.Context, filters usagestats.UsageLogFilters) (*usagestats.UsageStats, error) {
	where, args := financialUsageWhere(filters)
	// GROUPING SETS supplies the totals and endpoint breakdown from one snapshot.
	query := `WITH financial_scope AS (SELECT *, COALESCE(NULLIF(TRIM(inbound_endpoint),''),'unknown') AS fin_in, COALESCE(NULLIF(TRIM(upstream_endpoint),''),'unknown') AS fin_up FROM ` + financialStatsSource(filters) + ` ` + where + `) SELECT GROUPING(fin_in),GROUPING(fin_up),fin_in,fin_up,` + financialAggregateColumns + ` FROM financial_scope GROUP BY GROUPING SETS((),(fin_in),(fin_up),(fin_in,fin_up))`
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := &usagestats.UsageStats{}
	for rows.Next() {
		var gi, gu int
		var inbound, upstream sql.NullString
		stats, e := scanFinancialAggregate(rows, &gi, &gu, &inbound, &upstream)
		if e != nil {
			return nil, e
		}
		stats.DateBasis = usagestats.NormalizeFinancialDateBasis(filters.DateBasis)
		cost := stats.TotalActualCost
		if (filters.AccountID > 0 || len(filters.AccountIDs) > 0) && filters.UserID == 0 && filters.APIKeyID == 0 {
			cost = *stats.TotalAccountCost
		}
		endpoint := usagestats.EndpointStat{FinancialSummary: stats.FinancialSummary, Requests: stats.TotalRequests, TotalTokens: stats.TotalTokens, Cost: stats.TotalCost, ActualCost: cost}
		switch {
		case gi == 1 && gu == 1:
			stats.Endpoints = result.Endpoints
			stats.UpstreamEndpoints = result.UpstreamEndpoints
			stats.EndpointPaths = result.EndpointPaths
			result = stats
		case gi == 0 && gu == 1:
			endpoint.Endpoint = inbound.String
			result.Endpoints = append(result.Endpoints, endpoint)
		case gi == 1 && gu == 0:
			endpoint.Endpoint = upstream.String
			result.UpstreamEndpoints = append(result.UpstreamEndpoints, endpoint)
		default:
			endpoint.Endpoint = inbound.String + " -> " + upstream.String
			result.EndpointPaths = append(result.EndpointPaths, endpoint)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, items := range [][]usagestats.EndpointStat{result.Endpoints, result.UpstreamEndpoints, result.EndpointPaths} {
		sort.Slice(items, func(i, j int) bool {
			if items[i].Requests != items[j].Requests {
				return items[i].Requests > items[j].Requests
			}
			return items[i].Endpoint < items[j].Endpoint
		})
	}
	result.DateBasis = usagestats.NormalizeFinancialDateBasis(filters.DateBasis)
	return result, nil
}

func financialDashboardMetadata(total, today *usagestats.UsageStats) usagestats.FinancialDashboardSummary {
	return usagestats.FinancialDashboardSummary{
		TotalBalanceActualCost: total.BalanceActualCost, TotalSubscriptionActualCost: total.SubscriptionActualCost, TotalDetailPendingCount: total.DetailPendingCount, TotalUnknownAmountCount: total.UnknownAmountCount, TotalIncompleteRecordCount: total.IncompleteRecordCount, TotalStandardCostComplete: total.StandardCostComplete, TotalTokenCountsComplete: total.TokenCountsComplete,
		TodayBalanceActualCost: today.BalanceActualCost, TodaySubscriptionActualCost: today.SubscriptionActualCost, TodayDetailPendingCount: today.DetailPendingCount, TodayUnknownAmountCount: today.UnknownAmountCount, TodayIncompleteRecordCount: today.IncompleteRecordCount, TodayStandardCostComplete: today.StandardCostComplete, TodayTokenCountsComplete: today.TokenCountsComplete, DateBasis: usagestats.FinancialDateAccounting}
}
func financialTodayRange() (time.Time, time.Time) {
	now := time.Now().In(financialBeijing)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, financialBeijing)
	return start, start.AddDate(0, 0, 1)
}

func (r *usageLogRepository) GetFinancialDashboardStats(ctx context.Context, userID, keyID int64) (*usagestats.UserDashboardStats, error) {
	return r.GetFinancialDashboardStatsWithBasis(ctx, userID, keyID, usagestats.FinancialDateAccounting, "Asia/Shanghai")
}

func (r *usageLogRepository) GetFinancialDashboardStatsWithBasis(ctx context.Context, userID, keyID int64, dateBasis, userTimezone string) (*usagestats.UserDashboardStats, error) {
	filters := usagestats.UsageLogFilters{UserID: userID, APIKeyID: keyID, DateBasis: usagestats.NormalizeFinancialDateBasis(dateBasis)}
	total, err := r.financialTotals(ctx, filters)
	if err != nil {
		return nil, err
	}
	start, end := financialTodayRange()
	if filters.DateBasis == usagestats.FinancialDateCompleted {
		loc, locErr := time.LoadLocation(userTimezone)
		if locErr != nil {
			loc = financialBeijing
		}
		now := time.Now().In(loc)
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		end = start.AddDate(0, 0, 1)
	}
	today, err := r.financialTotals(ctx, financialRange(filters, start, end))
	if err != nil {
		return nil, err
	}
	out := &usagestats.UserDashboardStats{FinancialDashboardSummary: financialDashboardMetadata(total, today), TotalRequests: total.TotalRequests, TotalInputTokens: total.TotalInputTokens, TotalOutputTokens: total.TotalOutputTokens, TotalCacheCreationTokens: total.TotalCacheCreationTokens, TotalCacheReadTokens: total.TotalCacheReadTokens, TotalTokens: total.TotalTokens, TotalCost: total.TotalCost, TotalActualCost: total.TotalActualCost, AverageDurationMs: total.AverageDurationMs, TodayRequests: today.TotalRequests, TodayInputTokens: today.TotalInputTokens, TodayOutputTokens: today.TotalOutputTokens, TodayCacheCreationTokens: today.TotalCacheCreationTokens, TodayCacheReadTokens: today.TotalCacheReadTokens, TodayTokens: today.TotalTokens, TodayCost: today.TotalCost, TodayActualCost: today.TotalActualCost}
	if keyID > 0 {
		out.TotalAPIKeys = 1
		out.ActiveAPIKeys = 1
		out.Rpm, out.Tpm, err = r.getPerformanceStatsByAPIKey(ctx, keyID)
	} else {
		err = scanSingleRow(ctx, r.sql, "SELECT COUNT(*),COUNT(*) FILTER(WHERE status='active') FROM api_keys WHERE user_id=$1 AND deleted_at IS NULL", []any{userID}, &out.TotalAPIKeys, &out.ActiveAPIKeys)
		if err == nil {
			out.Rpm, out.Tpm, err = r.getPerformanceStats(ctx, userID)
		}
	}
	if err != nil {
		return nil, err
	}
	where, args := financialUsageWhere(filters)
	args = append(args, start, end)
	startPos, endPos := len(args)-1, len(args)
	todayCondition := fmt.Sprintf("completed_at >= $%d::timestamptz AND completed_at < $%d::timestamptz", startPos, endPos)
	if filters.DateBasis == usagestats.FinancialDateAccounting {
		args[startPos-1] = start.Format("2006-01-02")
		args[endPos-1] = end.Format("2006-01-02")
		todayCondition = fmt.Sprintf("accounting_date >= $%d::date AND accounting_date < $%d::date", startPos, endPos)
	}
	out.DateBasis = filters.DateBasis
	query := fmt.Sprintf(`SELECT COALESCE(NULLIF(route_billing_snapshot->>'resolved_platform',''),NULLIF(g.platform,'composite'),a.platform,'unknown'),COUNT(*),COALESCE(SUM(input_tokens+output_tokens+cache_creation_tokens+cache_read_tokens),0),COALESCE(SUM(actual_cost),0),COUNT(*) FILTER(WHERE %s),COALESCE(SUM(input_tokens+output_tokens+cache_creation_tokens+cache_read_tokens) FILTER(WHERE %s),0),COALESCE(SUM(actual_cost) FILTER(WHERE %s),0) FROM (SELECT * FROM %s %s) f LEFT JOIN groups g ON g.id=f.group_id LEFT JOIN accounts a ON a.id=f.account_id GROUP BY 1 ORDER BY 4 DESC`, todayCondition, todayCondition, todayCondition, financialUsageTable, where)
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p usagestats.PlatformDashboardStats
		if err = rows.Scan(&p.Platform, &p.TotalRequests, &p.TotalTokens, &p.TotalActualCost, &p.TodayRequests, &p.TodayTokens, &p.TodayActualCost); err != nil {
			return nil, err
		}
		out.ByPlatform = append(out.ByPlatform, p)
	}
	return out, rows.Err()
}

func (r *usageLogRepository) GetFinancialTrend(ctx context.Context, start, end time.Time, granularity string, filters usagestats.UsageLogFilters) ([]usagestats.TrendDataPoint, error) {
	filters = financialRange(filters, start, end)
	where, args := financialUsageWhere(filters)
	dateExpr := "accounting_date::timestamp"
	if financialTimeColumn(filters) == "completed_at" {
		zone := start.Location().String()
		if _, zoneErr := time.LoadLocation(zone); zoneErr != nil {
			zone = "Asia/Shanghai"
		}
		args = append(args, zone)
		dateExpr = fmt.Sprintf("completed_at AT TIME ZONE $%d", len(args))
	}
	query := fmt.Sprintf("SELECT TO_CHAR(%s,'%s'),%s FROM %s %s GROUP BY 1 ORDER BY 1", dateExpr, safeDateFormat(granularity), financialAggregateColumns, financialUsageTable, where)
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]usagestats.TrendDataPoint, 0)
	for rows.Next() {
		var date string
		stats, e := scanFinancialAggregate(rows, &date)
		if e != nil {
			return nil, e
		}
		stats.DateBasis = usagestats.NormalizeFinancialDateBasis(filters.DateBasis)
		out = append(out, usagestats.TrendDataPoint{FinancialSummary: stats.FinancialSummary, Date: date, Requests: stats.TotalRequests, InputTokens: stats.TotalInputTokens, OutputTokens: stats.TotalOutputTokens, CacheCreationTokens: stats.TotalCacheCreationTokens, CacheReadTokens: stats.TotalCacheReadTokens, TotalTokens: stats.TotalTokens, Cost: stats.TotalCost, ActualCost: stats.TotalActualCost})
	}
	return out, rows.Err()
}

func (r *usageLogRepository) GetFinancialModels(ctx context.Context, start, end time.Time, filters usagestats.UsageLogFilters, source string) ([]usagestats.ModelStat, error) {
	where, args := financialUsageWhere(financialRange(filters, start, end))
	expr := "COALESCE(" + resolveModelDimensionExpression(source) + ",'unknown')"
	query := "SELECT " + expr + "," + financialAggregateColumns + " FROM " + financialUsageTable + " " + where + " GROUP BY 1 ORDER BY 8 DESC"
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]usagestats.ModelStat, 0)
	for rows.Next() {
		var model string
		stats, e := scanFinancialAggregate(rows, &model)
		if e != nil {
			return nil, e
		}
		actual := stats.TotalActualCost
		if (filters.AccountID > 0 || len(filters.AccountIDs) > 0) && filters.UserID == 0 && filters.APIKeyID == 0 {
			actual = *stats.TotalAccountCost
		}
		stats.DateBasis = usagestats.NormalizeFinancialDateBasis(filters.DateBasis)
		out = append(out, usagestats.ModelStat{FinancialSummary: stats.FinancialSummary, Model: model, Requests: stats.TotalRequests, InputTokens: stats.TotalInputTokens, OutputTokens: stats.TotalOutputTokens, CacheCreationTokens: stats.TotalCacheCreationTokens, CacheReadTokens: stats.TotalCacheReadTokens, TotalTokens: stats.TotalTokens, Cost: stats.TotalCost, ActualCost: actual, AccountCost: *stats.TotalAccountCost})
	}
	return out, rows.Err()
}

func (r *usageLogRepository) GetFinancialGroups(ctx context.Context, start, end time.Time, filters usagestats.UsageLogFilters) ([]usagestats.GroupStat, error) {
	where, args := financialUsageWhere(financialRange(filters, start, end))
	query := `WITH grouped AS (SELECT group_id,` + financialAggregateColumns + ` FROM ` + financialUsageTable + ` ` + where + ` GROUP BY group_id) SELECT * FROM grouped ORDER BY 8 DESC`
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]usagestats.GroupStat, 0)
	for rows.Next() {
		var group sql.NullInt64
		stats, e := scanFinancialAggregate(rows, &group)
		if e != nil {
			return nil, e
		}
		stats.DateBasis = usagestats.NormalizeFinancialDateBasis(filters.DateBasis)
		out = append(out, usagestats.GroupStat{FinancialSummary: stats.FinancialSummary, GroupID: group.Int64, GroupName: "unknown", Requests: stats.TotalRequests, TotalTokens: stats.TotalTokens, Cost: stats.TotalCost, ActualCost: stats.TotalActualCost, AccountCost: *stats.TotalAccountCost})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if r.client != nil {
		ids := make([]int64, 0, len(out))
		for _, row := range out {
			if row.GroupID > 0 {
				ids = append(ids, row.GroupID)
			}
		}
		groups, e := r.loadGroups(ctx, ids)
		if e != nil {
			return nil, e
		}
		for i := range out {
			if group := groups[out[i].GroupID]; group != nil {
				out[i].GroupName = group.Name
			}
		}
	}
	return out, nil
}

func (r *usageLogRepository) GetFinancialBatchAPIKeyStats(ctx context.Context, ids []int64, start, end time.Time) (map[int64]*usagestats.BatchAPIKeyUsageStats, error) {
	out := map[int64]*usagestats.BatchAPIKeyUsageStats{}
	ids = normalizePositiveInt64IDs(ids)
	if len(ids) == 0 {
		return out, nil
	}
	for _, id := range ids {
		out[id] = &usagestats.BatchAPIKeyUsageStats{APIKeyID: id}
	}
	if start.IsZero() {
		start = time.Now().AddDate(0, 0, -30)
	}
	if end.IsZero() {
		_, end = financialTodayRange()
	}
	today, _ := financialTodayRange()
	lower := start
	if today.Before(lower) {
		lower = today
	}
	upper := end
	tomorrow := today.AddDate(0, 0, 1)
	if tomorrow.After(upper) {
		upper = tomorrow
	}
	where, scopeArgs := financialUsageWhere(usagestats.UsageLogFilters{StartTime: &lower, EndTime: &upper})
	source := "(SELECT * FROM " + financialUsageTable + " " + where + ")"
	scopeCount := len(scopeArgs)

	query := fmt.Sprintf(`SELECT api_key_id,COALESCE(SUM(actual_cost) FILTER(WHERE accounting_date >= $%d::date AND accounting_date < $%d::date),0),COALESCE(SUM(actual_cost) FILTER(WHERE accounting_date=$%d::date),0) FROM %s f WHERE api_key_id=ANY($%d) GROUP BY api_key_id`, scopeCount+2, scopeCount+3, scopeCount+4, source, scopeCount+1)
	rows, err := r.sql.QueryContext(ctx, query, append(scopeArgs, pq.Array(ids), start.In(financialBeijing).Format("2006-01-02"), financialExclusiveDate(end), today.Format("2006-01-02"))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var total, today float64
		if err = rows.Scan(&id, &total, &today); err != nil {
			return nil, err
		}
		out[id] = &usagestats.BatchAPIKeyUsageStats{APIKeyID: id, TotalActualCost: total, TodayActualCost: today}
	}
	return out, rows.Err()
}

func (r *usageLogRepository) GetFinancialAdminDashboardStats(ctx context.Context) (*usagestats.DashboardStats, error) {
	out := &usagestats.DashboardStats{}
	start, end := financialTodayRange()
	now := time.Now()
	if err := r.fillDashboardEntityStats(ctx, out, start, now); err != nil {
		return nil, err
	}
	total, err := r.financialTotals(ctx, usagestats.UsageLogFilters{})
	if err != nil {
		return nil, err
	}
	today, err := r.financialTotals(ctx, usagestats.UsageLogFilters{StartTime: &start, EndTime: &end})
	if err != nil {
		return nil, err
	}
	out.FinancialDashboardSummary = financialDashboardMetadata(total, today)
	out.TotalRequests = total.TotalRequests
	out.TotalInputTokens = total.TotalInputTokens
	out.TotalOutputTokens = total.TotalOutputTokens
	out.TotalCacheCreationTokens = total.TotalCacheCreationTokens
	out.TotalCacheReadTokens = total.TotalCacheReadTokens
	out.TotalTokens = total.TotalTokens
	out.TotalCost = total.TotalCost
	out.TotalActualCost = total.TotalActualCost
	out.TotalAccountCost = *total.TotalAccountCost
	out.AverageDurationMs = total.AverageDurationMs
	out.TodayRequests = today.TotalRequests
	out.TodayInputTokens = today.TotalInputTokens
	out.TodayOutputTokens = today.TotalOutputTokens
	out.TodayCacheCreationTokens = today.TotalCacheCreationTokens
	out.TodayCacheReadTokens = today.TotalCacheReadTokens
	out.TodayTokens = today.TotalTokens
	out.TodayCost = today.TotalCost
	out.TodayActualCost = today.TotalActualCost
	out.TodayAccountCost = *today.TotalAccountCost
	where, args := financialUsageWhere(usagestats.UsageLogFilters{StartTime: &start, EndTime: &end})
	if err = scanSingleRow(ctx, r.sql, "SELECT COUNT(DISTINCT user_id) FROM "+financialUsageTable+" "+where, args, &out.ActiveUsers); err != nil {
		return nil, err
	}
	// Operational activity remains request-completion based, independently of billing dates.
	if err = scanSingleRow(ctx, r.sql, "SELECT COUNT(DISTINCT user_id) FROM usage_logs WHERE created_at >= $1", []any{now.Truncate(time.Hour)}, &out.HourlyActiveUsers); err != nil {
		return nil, err
	}
	out.Rpm, out.Tpm, err = r.getPerformanceStats(ctx, 0)
	return out, err
}

func (r *usageLogRepository) GetFinancialBatchUserStats(ctx context.Context, ids []int64, start, end time.Time) (map[int64]*usagestats.BatchUserUsageStats, error) {
	out := map[int64]*usagestats.BatchUserUsageStats{}
	ids = normalizePositiveInt64IDs(ids)
	if len(ids) == 0 {
		return out, nil
	}
	for _, id := range ids {
		out[id] = &usagestats.BatchUserUsageStats{UserID: id}
	}
	if start.IsZero() {
		start = time.Now().AddDate(0, 0, -30)
	}
	if end.IsZero() {
		_, end = financialTodayRange()
	}
	today, _ := financialTodayRange()
	lower := start
	if today.Before(lower) {
		lower = today
	}
	upper := end
	tomorrow := today.AddDate(0, 0, 1)
	if tomorrow.After(upper) {
		upper = tomorrow
	}
	where, scopeArgs := financialUsageWhere(usagestats.UsageLogFilters{StartTime: &lower, EndTime: &upper})
	source := "(SELECT * FROM " + financialUsageTable + " " + where + ")"
	scopeCount := len(scopeArgs)

	query := fmt.Sprintf(`SELECT user_id,COALESCE(SUM(actual_cost) FILTER(WHERE accounting_date >= $%d::date AND accounting_date < $%d::date),0),COALESCE(SUM(actual_cost) FILTER(WHERE accounting_date=$%d::date),0) FROM %s f WHERE user_id=ANY($%d) GROUP BY user_id`, scopeCount+2, scopeCount+3, scopeCount+4, source, scopeCount+1)
	rows, err := r.sql.QueryContext(ctx, query, append(scopeArgs, pq.Array(ids), start.In(financialBeijing).Format("2006-01-02"), financialExclusiveDate(end), today.Format("2006-01-02"))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var total, today float64
		if err = rows.Scan(&id, &total, &today); err != nil {
			return nil, err
		}
		out[id] = &usagestats.BatchUserUsageStats{UserID: id, TotalActualCost: total, TodayActualCost: today}
	}
	return out, rows.Err()
}

func (r *usageLogRepository) GetFinancialUserRanking(ctx context.Context, start, end time.Time, limit int) (*usagestats.UserSpendingRankingResponse, error) {
	if limit <= 0 {
		limit = 12
	}
	where, args := financialUsageWhere(financialRange(usagestats.UsageLogFilters{}, start, end))
	query := `WITH scoped AS (SELECT * FROM ` + financialUsageTable + ` ` + where + `) SELECT GROUPING(f.user_id),f.user_id,u.email,u.username,` + financialAggregateColumns + ` FROM scoped f LEFT JOIN users u ON u.id=f.user_id GROUP BY GROUPING SETS((),(f.user_id,u.email,u.username))`
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &usagestats.UserSpendingRankingResponse{Ranking: []usagestats.UserSpendingRankingItem{}}
	for rows.Next() {
		var grouped int
		var id sql.NullInt64
		var email, username sql.NullString
		stats, e := scanFinancialAggregate(rows, &grouped, &id, &email, &username)
		if e != nil {
			return nil, e
		}
		stats.DateBasis = usagestats.FinancialDateAccounting
		if grouped == 1 {
			out.FinancialSummary = stats.FinancialSummary
			out.TotalActualCost = stats.TotalActualCost
			out.TotalRequests = stats.TotalRequests
			out.TotalTokens = stats.TotalTokens
			continue
		}
		out.Ranking = append(out.Ranking, usagestats.UserSpendingRankingItem{FinancialSummary: stats.FinancialSummary, UserID: id.Int64, Email: email.String, Username: username.String, ActualCost: stats.TotalActualCost, Requests: stats.TotalRequests, Tokens: stats.TotalTokens})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out.Ranking, func(i, j int) bool {
		if out.Ranking[i].ActualCost != out.Ranking[j].ActualCost {
			return out.Ranking[i].ActualCost > out.Ranking[j].ActualCost
		}
		return out.Ranking[i].UserID < out.Ranking[j].UserID
	})
	if len(out.Ranking) > limit {
		out.Ranking = out.Ranking[:limit]
	}
	return out, nil
}

func (r *usageLogRepository) GetFinancialUserTrend(ctx context.Context, start, end time.Time, granularity string, limit int) ([]usagestats.UserUsageTrendPoint, error) {
	if limit <= 0 {
		limit = 12
	}
	where, args := financialUsageWhere(financialRange(usagestats.UsageLogFilters{}, start, end))
	args = append(args, limit)
	query := fmt.Sprintf(`WITH scoped AS (SELECT * FROM %s %s),top_users AS (SELECT user_id FROM scoped GROUP BY user_id ORDER BY SUM(actual_cost) DESC LIMIT $%d) SELECT TO_CHAR(f.accounting_date,'%s'),f.user_id,COALESCE(u.email,''),COALESCE(u.username,''),%s FROM scoped f LEFT JOIN users u ON u.id=f.user_id WHERE f.user_id IN(SELECT user_id FROM top_users) GROUP BY 1,2,3,4 ORDER BY 1,11 DESC`, financialUsageTable, where, len(args), safeDateFormat(granularity), financialAggregateColumns)
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]usagestats.UserUsageTrendPoint, 0)
	for rows.Next() {
		var row usagestats.UserUsageTrendPoint
		stats, e := scanFinancialAggregate(rows, &row.Date, &row.UserID, &row.Email, &row.Username)
		if e != nil {
			return nil, e
		}
		stats.DateBasis = usagestats.FinancialDateAccounting
		row.FinancialSummary = stats.FinancialSummary
		row.Requests = stats.TotalRequests
		row.Tokens = stats.TotalTokens
		row.Cost = stats.TotalCost
		row.ActualCost = stats.TotalActualCost
		out = append(out, row)
	}
	return out, rows.Err()
}

func (r *usageLogRepository) GetFinancialKeyTrend(ctx context.Context, start, end time.Time, granularity string, limit int) ([]usagestats.APIKeyUsageTrendPoint, error) {
	if limit <= 0 {
		limit = 12
	}
	where, args := financialUsageWhere(financialRange(usagestats.UsageLogFilters{}, start, end))
	args = append(args, limit)
	query := fmt.Sprintf(`WITH scoped AS (SELECT * FROM %s %s),top_keys AS (SELECT api_key_id FROM scoped GROUP BY api_key_id ORDER BY SUM(actual_cost) DESC LIMIT $%d) SELECT TO_CHAR(f.accounting_date,'%s'),f.api_key_id,COALESCE(k.name,''),%s FROM scoped f LEFT JOIN api_keys k ON k.id=f.api_key_id WHERE f.api_key_id IN(SELECT api_key_id FROM top_keys) GROUP BY 1,2,3 ORDER BY 1,10 DESC`, financialUsageTable, where, len(args), safeDateFormat(granularity), financialAggregateColumns)
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]usagestats.APIKeyUsageTrendPoint, 0)
	for rows.Next() {
		var row usagestats.APIKeyUsageTrendPoint
		stats, e := scanFinancialAggregate(rows, &row.Date, &row.APIKeyID, &row.KeyName)
		if e != nil {
			return nil, e
		}
		stats.DateBasis = usagestats.FinancialDateAccounting
		row.FinancialSummary = stats.FinancialSummary
		row.Requests = stats.TotalRequests
		row.Tokens = stats.TotalTokens
		out = append(out, row)
	}
	return out, rows.Err()
}

func (r *usageLogRepository) GetFinancialUserBreakdown(ctx context.Context, start, end time.Time, dim usagestats.UserBreakdownDimension, limit int) ([]usagestats.UserBreakdownItem, error) {
	filters := usagestats.UsageLogFilters{UserID: dim.UserID, APIKeyID: dim.APIKeyID, AccountID: dim.AccountID, AccountIDs: dim.AccountIDs, GroupID: dim.GroupID, Model: dim.Model, ModelFilterSource: dim.ModelType, RequestType: dim.RequestType, Stream: dim.Stream, NativeCompactionV2: dim.NativeCompactionV2, BillingType: dim.BillingType}
	where, args := financialUsageWhere(financialRange(filters, start, end))
	if dim.Endpoint != "" {
		expr := "inbound_endpoint"
		switch dim.EndpointType {
		case "upstream":
			expr = "upstream_endpoint"
		case "path":
			expr = "inbound_endpoint || ' -> ' || upstream_endpoint"
		}
		where += fmt.Sprintf(" AND %s=$%d", expr, len(args)+1)
		args = append(args, dim.Endpoint)
	}
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit)
	order := "COALESCE(SUM(actual_cost),0)"
	switch dim.SortBy {
	case "requests":
		order = "COUNT(*)"
	case "tokens":
		order = "COALESCE(SUM(input_tokens+output_tokens+cache_creation_tokens+cache_read_tokens),0)"
	case "cost":
		order = "COALESCE(SUM(total_cost),0)"
	case "account_cost":
		order = "COALESCE(SUM(COALESCE(account_stats_cost,total_cost)*COALESCE(account_rate_multiplier,1)),0)"
	}
	query := fmt.Sprintf(`WITH scoped AS (SELECT * FROM %s %s) SELECT f.user_id,COALESCE(u.email,''),%s FROM scoped f LEFT JOIN users u ON u.id=f.user_id GROUP BY f.user_id,u.email ORDER BY %s DESC,f.user_id LIMIT $%d`, financialUsageTable, where, financialAggregateColumns, order, len(args))
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]usagestats.UserBreakdownItem, 0)
	for rows.Next() {
		var row usagestats.UserBreakdownItem
		stats, e := scanFinancialAggregate(rows, &row.UserID, &row.Email)
		if e != nil {
			return nil, e
		}
		stats.DateBasis = usagestats.FinancialDateAccounting
		row.FinancialSummary = stats.FinancialSummary
		row.Requests = stats.TotalRequests
		row.InputTokens = stats.TotalInputTokens
		row.OutputTokens = stats.TotalOutputTokens
		row.CacheTokens = stats.TotalCacheTokens
		row.TotalTokens = stats.TotalTokens
		row.Cost = stats.TotalCost
		row.ActualCost = stats.TotalActualCost
		row.AccountCost = *stats.TotalAccountCost
		out = append(out, row)
	}
	return out, rows.Err()
}

func (r *usageLogRepository) GetFinancialGroupSummary(ctx context.Context, today time.Time) ([]usagestats.GroupUsageSummary, error) {
	if db, ok := r.sql.(*sql.DB); ok {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		result, err := (&usageLogRepository{sql: tx}).GetFinancialGroupSummary(ctx, today)
		if err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return result, nil
	}

	day := today.In(financialBeijing)
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, financialBeijing)
	values := map[int64]*usagestats.GroupUsageSummary{}
	// The existing dirty-bucket-aware rollup supplies only all-time raw totals;
	// date-specific financial buckets below remain direct indexed evidence.
	baseline, err := r.GetAllGroupUsageSummary(ctx, today)
	if err != nil {
		return nil, err
	}
	for _, row := range baseline {
		values[row.GroupID] = &usagestats.GroupUsageSummary{GroupID: row.GroupID, TotalCost: row.TotalCost}
	}
	var unknownGroupCost float64
	if err = scanSingleRow(ctx, r.sql, "SELECT COALESCE(SUM(actual_cost),0) FROM usage_logs WHERE group_id IS NULL", nil, &unknownGroupCost); err != nil {
		return nil, err
	}
	values[0] = &usagestats.GroupUsageSummary{GroupID: 0, TotalCost: unknownGroupCost}

	deltaSQL := `WITH matched AS MATERIALIZED (
 SELECT r.group_id receipt_group,r.charged_amount,r.command,u.group_id log_group,u.actual_cost log_cost
 FROM usage_settlement_receipts r LEFT JOIN LATERAL (
  SELECT linked.group_id,linked.actual_cost FROM usage_logs linked
  WHERE r.user_id=linked.user_id AND r.api_key_id=linked.api_key_id
  AND ((r.usage_log_id=linked.id AND (r.usage_request_id IS NULL OR r.usage_request_id=linked.request_id))
   OR (r.usage_log_id IS NULL AND r.usage_request_id IS NOT NULL AND r.usage_request_id=linked.request_id)) LIMIT 1
 ) u ON TRUE WHERE r.state='settled'
 ), deltas AS (
 SELECT COALESCE(receipt_group,log_group,0) group_id,COALESCE(charged_amount,0) amount FROM matched WHERE COALESCE(command->>'TerminalFailure','false')<>'true'
 UNION ALL SELECT COALESCE(log_group,0),-log_cost FROM matched WHERE log_cost IS NOT NULL
 ) SELECT group_id,SUM(amount) FROM deltas GROUP BY group_id`
	deltaRows, err := r.sql.QueryContext(ctx, deltaSQL)
	if err != nil {
		return nil, err
	}
	for deltaRows.Next() {
		var groupID int64
		var delta float64
		if err = deltaRows.Scan(&groupID, &delta); err != nil {
			deltaRows.Close()
			return nil, err
		}
		if values[groupID] == nil {
			values[groupID] = &usagestats.GroupUsageSummary{GroupID: groupID}
		}
		values[groupID].TotalCost += delta
	}
	if err = deltaRows.Err(); err != nil {
		deltaRows.Close()
		return nil, err
	}
	if err = deltaRows.Close(); err != nil {
		return nil, err
	}

	for i := 1; i < 3; i++ {
		start := day
		if i == 2 {
			start = day.AddDate(0, 0, -1)
		}
		end := start.AddDate(0, 0, 1)
		where, args := financialUsageWhere(usagestats.UsageLogFilters{StartTime: &start, EndTime: &end})
		source := financialUsageTable
		rows, err := r.sql.QueryContext(ctx, "SELECT COALESCE(group_id,0),COALESCE(SUM(actual_cost),0) FROM "+source+" "+where+" GROUP BY group_id", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var cost float64
			if err = rows.Scan(&id, &cost); err != nil {
				rows.Close()
				return nil, err
			}
			entry := values[id]
			if entry == nil {
				entry = &usagestats.GroupUsageSummary{GroupID: id}
				values[id] = entry
			}
			switch i {
			case 1:
				entry.TodayCost = cost
			case 2:
				entry.YesterdayCost = cost
			}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err = rows.Close(); err != nil {
			return nil, err
		}
	}
	out := make([]usagestats.GroupUsageSummary, 0, len(values))
	for _, value := range values {
		out = append(out, *value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GroupID < out[j].GroupID })
	return out, nil
}

func financialExclusiveDate(end time.Time) string {
	day := end.In(financialBeijing)
	if day.Hour() != 0 || day.Minute() != 0 || day.Second() != 0 || day.Nanosecond() != 0 {
		day = day.AddDate(0, 0, 1)
	}
	return day.Format("2006-01-02")
}
