package subscription

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

func TestLotAuditAllocationBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	base := Lot{ID: 1, Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(0, 0, 30), DailyLimitUSD: ptr(5), WeeklyLimitUSD: ptr(10), MonthlyLimitUSD: ptr(20)}
	t.Run("tie_uses_id_and_does_not_mutate_input", func(t *testing.T) {
		a, b := base, base
		a.ID = 2
		in := []Lot{a, b}
		got, err := Allocate(in, 7, now)
		require.NoError(t, err)
		require.Equal(t, int64(1), got[0].ID)
		require.Equal(t, 5.0, got[0].LifetimeUsageUSD)
		require.Equal(t, 2.0, got[1].LifetimeUsageUSD)
		require.Zero(t, in[0].LifetimeUsageUSD)
		require.Zero(t, in[1].LifetimeUsageUSD)
	})
	t.Run("overrun_is_recorded", func(t *testing.T) {
		got, err := Allocate([]Lot{base}, 8, now)
		require.NoError(t, err)
		require.Equal(t, 8.0, got[0].LifetimeUsageUSD)
		require.Zero(t, got[0].Capacity())
	})
	t.Run("invalid_cost", func(t *testing.T) {
		for _, cost := range []float64{-1, math.NaN(), math.Inf(1)} {
			_, err := Allocate([]Lot{base}, cost, now)
			require.Error(t, err)
		}
	})
	t.Run("small_charges_conserve_total", func(t *testing.T) {
		lots := []Lot{base}
		var err error
		for i := 0; i < 1000; i++ {
			lots, err = Allocate(lots, 0.00000001, now)
			require.NoError(t, err)
		}
		require.Equal(t, 0.00001, lots[0].LifetimeUsageUSD)
	})
	t.Run("expired_refunded_pending_future_are_excluded", func(t *testing.T) {
		expired, refunded, pending, future := base, base, base, base
		expired.ExpiresAt = now
		refunded.Status = "refunded"
		pending.Status = "refund_pending"
		future.StartsAt = now.Add(time.Hour)
		got := Aggregate([]Lot{base, expired, refunded, pending, future}, now)
		require.Equal(t, 1, got.ActiveLotCount)
		require.Equal(t, 5.0, *got.DailyLimitUSD)
	})
}

func TestLotAuditWindowBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	t.Run("daily_rolls_without_erasing_lifetime", func(t *testing.T) {
		yesterday := timezone.StartOfDay(now).AddDate(0, 0, -1)
		e := Lot{Status: "active", StartsAt: now.AddDate(0, 0, -2), ExpiresAt: now.AddDate(0, 0, 30), DailyWindowStart: &yesterday, DailyUsageUSD: 5, LifetimeUsageUSD: 9}
		e.Normalize(now, false)
		require.Zero(t, e.DailyUsageUSD)
		require.Equal(t, 9.0, e.LifetimeUsageUSD)
		require.True(t, e.DailyWindowStart.Equal(timezone.StartOfDay(now)))
	})
	t.Run("one_day_card_has_no_midnight_bonus", func(t *testing.T) {
		start := now.Add(-20 * time.Hour)
		anchor := timezone.StartOfDay(start)
		e := Lot{Status: "active", StartsAt: start, ExpiresAt: start.AddDate(0, 0, 1), DailyWindowStart: &anchor, DailyUsageUSD: 5}
		e.Normalize(now, false)
		require.Equal(t, 5.0, e.DailyUsageUSD)
	})
	for _, span := range []int{7, 30} {
		t.Run(fmt.Sprintf("%d_day_window", span), func(t *testing.T) {
			anchor := now.Add(-time.Duration(span) * 24 * time.Hour)
			e := Lot{Status: "active", StartsAt: anchor, ExpiresAt: now.AddDate(0, 0, 30), WeeklyWindowStart: &anchor, MonthlyWindowStart: &anchor, WeeklyUsageUSD: 5, MonthlyUsageUSD: 5, LifetimeUsageUSD: 9}
			e.Normalize(now, false)
			if span == 7 {
				require.Zero(t, e.WeeklyUsageUSD)
				require.Equal(t, 5.0, e.MonthlyUsageUSD)
			} else {
				require.Zero(t, e.MonthlyUsageUSD)
			}
			require.Equal(t, 9.0, e.LifetimeUsageUSD)
		})
	}
	t.Run("aggregate_normalizes_a_copy", func(t *testing.T) {
		yesterday := timezone.StartOfDay(now).AddDate(0, 0, -1)
		lots := []Lot{{Status: "active", StartsAt: now.AddDate(0, 0, -2), ExpiresAt: now.AddDate(0, 0, 30), DailyWindowStart: &yesterday, DailyUsageUSD: 5, DailyLimitUSD: ptr(5)}}
		require.Zero(t, Aggregate(lots, now).DailyUsageUSD)
		require.Equal(t, 5.0, lots[0].DailyUsageUSD)
	})
}
