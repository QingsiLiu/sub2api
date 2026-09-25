package service

import (
	"context"
	"time"
)

// SettlementReconciliationRepository is optional and read-only. Runtime health
// exposes the bounded sample's coverage; zero mismatches is not an all-history
// audit and does not establish the correctness of mutable aggregate balances.
type SettlementReconciliationRepository interface {
	ReconcileRecentSettlements(context.Context, int) (UsageSettlementReconciliation, error)
}

type UsageSettlementReconciliation struct {
	Coverage                    string     `json:"coverage"`
	Limit                       int        `json:"limit"`
	ScannedCount                int64      `json:"scanned_count"`
	CheckedCount                int64      `json:"checked_count"`
	SkippedCount                int64      `json:"skipped_count"`
	LastID                      int64      `json:"last_id"`
	FirstID                     int64      `json:"first_id"`
	HasMore                     bool       `json:"has_more"`
	WindowStart                 time.Time  `json:"window_start"`
	WindowEnd                   time.Time  `json:"window_end"`
	OldestCheckedAt             *time.Time `json:"oldest_checked_at"`
	NewestCheckedAt             *time.Time `json:"newest_checked_at"`
	AmountMismatchCount         int64      `json:"amount_mismatch_count"`
	DetailMissingCount          int64      `json:"detail_missing_count"`
	IdentityMismatchCount       int64      `json:"identity_mismatch_count"`
	DedupMismatchCount          int64      `json:"dedup_mismatch_count"`
	SubscriptionCheckedCount    int64      `json:"subscription_checked_count"`
	SubscriptionNotCheckedCount int64      `json:"subscription_not_checked_count"`
	SubscriptionMismatchCount   int64      `json:"subscription_mismatch_count"`
	// Exact decimal strings: available/matching log amount minus receipt amount.
	// Missing or identity-mismatched logs are counted separately, never valued0.
	NetDiscrepancyUSD             string `json:"net_discrepancy_usd"`
	AbsoluteDiscrepancyUSD        string `json:"absolute_discrepancy_usd"`
	SubscriptionNetDiscrepancyUSD string `json:"subscription_net_discrepancy_usd"`
}
