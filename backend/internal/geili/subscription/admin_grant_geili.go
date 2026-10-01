package subscription

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

// SourceAdminGrant marks an administrator-issued daily entitlement. It is both
// the lot source_type and purchase_mode, so it never resembles a paid tier.
const SourceAdminGrant = "admin_grant"

const (
	AdminGrantMaxDailyUSD = 10000
	AdminGrantMaxDays     = 3650
	AdminGrantMaxReason   = 500
)

var (
	ErrAdminGrantDaily         = infraerrors.BadRequest("ADMIN_GRANT_INVALID_DAILY", "daily limit must be greater than 0 and at most 10000 USD")
	ErrAdminGrantDays          = infraerrors.BadRequest("ADMIN_GRANT_INVALID_DAYS", "validity days must be between 1 and 3650")
	ErrAdminGrantReason        = infraerrors.BadRequest("ADMIN_GRANT_REASON_REQUIRED", "a reason of at most 500 characters is required")
	ErrAdminGrantKey           = infraerrors.BadRequest("ADMIN_GRANT_IDEMPOTENCY_KEY_REQUIRED", "an Idempotency-Key of 8 to 128 characters is required")
	ErrAdminGrantMultiplePools = infraerrors.Conflict("ADMIN_GRANT_MULTIPLE_POOLS", "the user has several live subscription pools; resolve them manually first")
	ErrAdminGrantPoolSuspended = infraerrors.Conflict("ADMIN_GRANT_POOL_SUSPENDED", "the user's subscription pool is suspended")
	ErrAdminGrantRefundPending = infraerrors.Conflict("ADMIN_GRANT_REFUND_PENDING", "the subscription pool has a refund in progress")
	ErrAdminGrantKeyReused     = infraerrors.Conflict("ADMIN_GRANT_IDEMPOTENCY_KEY_REUSED", "this Idempotency-Key was already used with different parameters")
	ErrAdminGrantNotTerminable = infraerrors.Conflict("ADMIN_GRANT_NOT_TERMINABLE", "only a live administrator grant can be terminated early")
)

// LimitSegment is one interval of a constant compatibility daily limit.
// A nil limit means at least one unlimited lot is live in the interval.
type LimitSegment struct {
	StartsAt      time.Time `json:"starts_at"`
	EndsAt        time.Time `json:"ends_at"`
	DailyLimitUSD *float64  `json:"daily_limit_usd"`
	LotCount      int       `json:"lot_count"`
}

// ValidateGrant normalizes the request terms and returns the rounded daily
// limit and the reason with surrounding whitespace removed.
func ValidateGrant(daily float64, days int, reason string, now time.Time) (float64, string, error) {
	if math.IsNaN(daily) || math.IsInf(daily, 0) {
		return 0, "", ErrAdminGrantDaily
	}
	daily = decimal.NewFromFloat(daily).Round(2).InexactFloat64()
	if daily <= 0 || daily > AdminGrantMaxDailyUSD {
		return 0, "", ErrAdminGrantDaily
	}
	if days < 1 || days > AdminGrantMaxDays || now.AddDate(0, 0, days).After(MaxExpiry) {
		return 0, "", ErrAdminGrantDays
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > AdminGrantMaxReason {
		return 0, "", ErrAdminGrantReason
	}
	return daily, reason, nil
}

// ValidGrantKey bounds operator-provided idempotency keys.
func ValidGrantKey(key string) bool {
	return len(key) >= 8 && len(key) <= 128
}

func timelineLot(l Lot, now time.Time) bool {
	return l.Status == "active" && l.ExpiresAt.After(now)
}

// GrantTimeline projects the compatibility (legacy_daily) daily limit from now
// until the last live lot expires. Adjacent intervals with the same limit are
// merged; gaps without a live lot are omitted.
func GrantTimeline(lots []Lot, now time.Time) []LimitSegment {
	points := []time.Time{now}
	for _, l := range lots {
		if !timelineLot(l, now) {
			continue
		}
		if l.StartsAt.After(now) {
			points = append(points, l.StartsAt)
		}
		points = append(points, l.ExpiresAt)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Before(points[j]) })
	var out []LimitSegment
	for i := 0; i+1 < len(points); i++ {
		from, to := points[i], points[i+1]
		if !to.After(from) {
			continue
		}
		total := decimal.Zero
		count := 0
		unlimited := false
		for _, l := range lots {
			if !timelineLot(l, now) || l.StartsAt.After(from) || l.ExpiresAt.Before(to) {
				continue
			}
			count++
			if finite(l.DailyLimitUSD) {
				total = total.Add(decimal.NewFromFloat(*l.DailyLimitUSD))
			} else {
				unlimited = true
			}
		}
		if count == 0 {
			continue
		}
		var limit *float64
		if !unlimited {
			v := total.InexactFloat64()
			limit = &v
		}
		if n := len(out); n > 0 && out[n-1].EndsAt.Equal(from) && out[n-1].LotCount == count && sameLimit(out[n-1].DailyLimitUSD, limit) {
			out[n-1].EndsAt = to
			continue
		}
		out = append(out, LimitSegment{StartsAt: from, EndsAt: to, DailyLimitUSD: limit, LotCount: count})
	}
	return out
}

func sameLimit(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
