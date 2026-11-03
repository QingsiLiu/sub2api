package handler

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// subscriptionUsageResponseGeili projects existing quota evidence without
// selecting a route, admitting consumption or changing cached subscription data.
func (h *GatewayHandler) subscriptionUsageResponseGeili(key *service.APIKey, sub *service.UserSubscription, now time.Time) gin.H {
	name := "订阅额度"
	if key.BillingSource == "" && key.Group != nil {
		name = key.Group.Name
	}
	resp := gin.H{"mode": "unrestricted", "isValid": true, "planName": name, "unit": "USD"}
	// Auth intentionally allows usage reads when no active subscription can be
	// loaded. Unknown quota stays absent; it must never become wallet balance.
	if sub == nil {
		return resp
	}
	if name := sub.QuotaName(); name != "" {
		resp["planName"] = name
	}

	usedDaily, usedWeekly, usedMonthly := sub.DailyUsageUSD, sub.WeeklyUsageUSD, sub.MonthlyUsageUSD
	weeklyStart, expiresAt := sub.WeeklyWindowStart, sub.ExpiresAt
	var daily, weekly, monthly *float64
	remaining := -1.0
	if sub.Contract != nil || len(sub.Entitlements) > 0 {
		summary := sub.QuotaSummaryAt(now)
		daily, weekly, monthly = summary.DailyLimitUSD, summary.WeeklyLimitUSD, summary.MonthlyLimitUSD
		usedDaily, usedWeekly, usedMonthly = summary.DailyUsageUSD, summary.WeeklyUsageUSD, summary.MonthlyUsageUSD
		if summary.RemainingUSD != nil {
			remaining = *summary.RemainingUSD
		}
		if summary.ExpiresAt != nil {
			expiresAt = *summary.ExpiresAt
		}
		if sub.Contract != nil {
			weeklyStart = nil // V2 has no weekly gating window, including migrated contracts.
		}
	} else {
		legacyGroup := sub.Group
		if legacyGroup == nil && key.BillingSource == "" {
			legacyGroup = key.Group
		}
		daily, weekly, monthly = sub.QuotaLimits(legacyGroup)
		limits := &service.Group{DailyLimitUSD: daily, WeeklyLimitUSD: weekly, MonthlyLimitUSD: monthly}
		remaining = h.calculateSubscriptionRemaining(limits, sub)
	}
	resp["remaining"] = remaining
	resp["subscription"] = gin.H{
		"daily_usage_usd": usedDaily, "weekly_usage_usd": usedWeekly, "monthly_usage_usd": usedMonthly,
		"daily_limit_usd": daily, "weekly_limit_usd": weekly, "monthly_limit_usd": monthly,
		"weekly_window_start": weeklyStart, "expires_at": expiresAt,
	}
	return resp
}
