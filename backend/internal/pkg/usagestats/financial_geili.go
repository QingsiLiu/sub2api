package usagestats

import "strings"

const FinancialDateAccounting = "accounting"
const FinancialDateCompleted = "completed"

func NormalizeFinancialDateBasis(raw string) string {
	if strings.TrimSpace(raw) == FinancialDateCompleted {
		return FinancialDateCompleted
	}
	return FinancialDateAccounting
}

func ValidFinancialDateBasis(raw string) bool {
	switch strings.TrimSpace(raw) {
	case "", FinancialDateAccounting, FinancialDateCompleted:
		return true
	}
	return false
}

type FinancialSummary struct {
	BalanceActualCost      float64 `json:"balance_actual_cost"`
	SubscriptionActualCost float64 `json:"subscription_actual_cost"`
	DetailPendingCount     int64   `json:"detail_pending_count"`
	UnknownAmountCount     int64   `json:"unknown_amount_count"`
	IncompleteRecordCount  int64   `json:"incomplete_record_count"`
	StandardCostComplete   bool    `json:"standard_cost_complete"`
	TokenCountsComplete    bool    `json:"token_counts_complete"`
	DateBasis              string  `json:"date_basis"`
}

type FinancialDashboardSummary struct {
	TotalBalanceActualCost      float64 `json:"total_balance_actual_cost"`
	TotalSubscriptionActualCost float64 `json:"total_subscription_actual_cost"`
	TotalDetailPendingCount     int64   `json:"total_detail_pending_count"`
	TotalUnknownAmountCount     int64   `json:"total_unknown_amount_count"`
	TotalIncompleteRecordCount  int64   `json:"total_incomplete_record_count"`
	TotalStandardCostComplete   bool    `json:"total_standard_cost_complete"`
	TotalTokenCountsComplete    bool    `json:"total_token_counts_complete"`
	TodayBalanceActualCost      float64 `json:"today_balance_actual_cost"`
	TodaySubscriptionActualCost float64 `json:"today_subscription_actual_cost"`
	TodayDetailPendingCount     int64   `json:"today_detail_pending_count"`
	TodayUnknownAmountCount     int64   `json:"today_unknown_amount_count"`
	TodayIncompleteRecordCount  int64   `json:"today_incomplete_record_count"`
	TodayStandardCostComplete   bool    `json:"today_standard_cost_complete"`
	TodayTokenCountsComplete    bool    `json:"today_token_counts_complete"`
	DateBasis                   string  `json:"date_basis"`
}
