package service

import (
	"math"
	"time"

	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
)

// entitlementSubscriptionProgress projects detached lot data only. Several lots
// may have different windows: expose their earliest active start and next reset,
// never invent a shared window or write one back to the parent subscription.
func entitlementSubscriptionProgress(sub *UserSubscription, group *Group, now time.Time) *SubscriptionProgress {
	copy := *sub
	sub = &copy
	effectiveSubscriptionSummary(sub, now)
	summary := sub.QuotaSummary
	name := sub.QuotaName()
	if name == "" && group != nil {
		name = group.Name
	}
	progress := &SubscriptionProgress{ID: sub.ID, GroupName: name, ExpiresAt: sub.ExpiresAt, ExpiresInDays: sub.daysRemainingAt(now)}
	var starts [3]*time.Time
	if sub.Contract != nil {
		day := geilisub.DayStart(now)
		starts[0] = &day
	}
	for _, lot := range sub.Entitlements {
		if sub.Contract != nil {
			break
		}
		if !lot.Active(now) {
			continue
		}
		lot.Normalize(now, false)
		for i, start := range []*time.Time{lot.DailyWindowStart, lot.WeeklyWindowStart, lot.MonthlyWindowStart} {
			if start != nil && (starts[i] == nil || start.Before(*starts[i])) {
				starts[i] = start
			}
		}
	}
	window := func(limit *float64, used float64, start, reset *time.Time) *UsageWindowProgress {
		if limit == nil || *limit <= 0 || start == nil || reset == nil {
			return nil
		}
		return &UsageWindowProgress{
			LimitUSD: *limit, UsedUSD: used, RemainingUSD: math.Max(0, *limit-used), Percentage: math.Min(100, used / *limit * 100),
			WindowStart: *start, ResetsAt: *reset, ResetsInSeconds: int64(math.Max(0, reset.Sub(now).Seconds())),
		}
	}
	progress.Daily = window(summary.DailyLimitUSD, summary.DailyUsageUSD, starts[0], summary.DailyResetAt)
	progress.Weekly = window(summary.WeeklyLimitUSD, summary.WeeklyUsageUSD, starts[1], summary.WeeklyResetAt)
	progress.Monthly = window(summary.MonthlyLimitUSD, summary.MonthlyUsageUSD, starts[2], summary.MonthlyResetAt)
	return progress
}
