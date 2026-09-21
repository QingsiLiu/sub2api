package service

import (
	"strconv"
	"time"

	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
)

type SubscriptionContract = geilisub.Contract

// QuotaSummaryAt projects a fixed-date ledger read without altering historical
// entitlement evidence. Crossing midnight changes the visible bucket, not usage.
func (s *UserSubscription) QuotaSummaryAt(now time.Time) *geilisub.Summary {
	if s == nil {
		return nil
	}
	if s.Contract == nil {
		a := geilisub.Aggregate(s.Entitlements, now)
		return &a
	}
	used := s.DailyUsageUSD
	if s.LedgerDailyUsageUSD != nil {
		used = *s.LedgerDailyUsageUSD
	}
	if !s.QuotaUsageDate.IsZero() && !s.QuotaUsageDate.Equal(geilisub.DayStart(now)) {
		used = 0
	}
	lots := s.Entitlements
	// A migrated pre-entitlement subscription keeps its persisted parent rights
	// until admission materializes a real lot under the subscription lock.
	if len(lots) == 0 && s.Contract.Mode == geilisub.ContractModeLegacy {
		var daily *float64
		if s.Plan != nil {
			daily = s.Plan.DailyLimitUSD
		} else if s.Group != nil {
			daily = s.Group.DailyLimitUSD
		}
		lots = []geilisub.Lot{{Status: s.Status, StartsAt: s.StartsAt, ExpiresAt: s.ExpiresAt, DailyLimitUSD: daily}}
	}
	a := geilisub.ContractSummary(s.Contract, lots, used, now)
	return &a
}

// DailyQuotaExceeded keeps the stable public error identity while explaining
// the authoritative shared quota and the next Beijing midnight to API clients.
func DailyQuotaExceeded(s *UserSubscription, now time.Time) error {
	reset := geilisub.DayStart(now).AddDate(0, 0, 1)
	metadata := map[string]string{"window_resets_at": reset.Format(time.RFC3339), "remaining_usd": "0"}
	if s != nil {
		if s.Contract != nil || len(s.Entitlements) > 0 {
			a := s.QuotaSummaryAt(now)
			if a.DailyLimitUSD != nil {
				metadata["daily_limit_usd"] = strconv.FormatFloat(*a.DailyLimitUSD, 'f', -1, 64)
			}
			metadata["daily_usage_usd"] = strconv.FormatFloat(a.DailyUsageUSD, 'f', -1, 64)
			if a.RemainingUSD != nil {
				metadata["remaining_usd"] = strconv.FormatFloat(*a.RemainingUSD, 'f', -1, 64)
			}
			if a.DailyResetAt != nil {
				metadata["window_resets_at"] = a.DailyResetAt.Format(time.RFC3339)
			}
		} else {
			daily, _, _ := s.QuotaLimits(s.Group)
			if daily != nil {
				metadata["daily_limit_usd"] = strconv.FormatFloat(*daily, 'f', -1, 64)
			}
			metadata["daily_usage_usd"] = strconv.FormatFloat(s.DailyUsageUSD, 'f', -1, 64)
		}
		if s.Contract != nil {
			metadata["quota_model"] = s.Contract.Mode
		}
	}
	return ErrDailyLimitExceeded.WithMetadata(metadata)
}
