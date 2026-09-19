//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestEntitlementDisplayGeiliAfterMidnight(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	today := timezone.StartOfDay(time.Now())
	yesterday := today.AddDate(0, 0, -1)
	lot := f.sub.Entitlements[0]
	_, err := f.c.UserSubscription.UpdateOneID(f.sub.ID).SetStartsAt(yesterday).SetDailyWindowStart(yesterday).SetDailyUsageUsd(90.21295472).Save(ctx)
	require.NoError(t, err)
	_, err = f.c.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetStartsAt(yesterday).SetDailyWindowStart(today).SetDailyLimitUsd(90).SetDailyUsageUsd(90.21295472).Save(ctx)
	require.NoError(t, err)
	subs, err := f.svc.ListActiveUserSubscriptions(ctx, f.user.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	require.Len(t, subs[0].Entitlements, 1, "active query must preload authoritative quota lots")
	require.InDelta(t, 90.21295472, subs[0].DailyUsageUSD, 1e-8)
	require.NotNil(t, subs[0].QuotaSummary)
	require.Equal(t, 90.0, *subs[0].QuotaSummary.DailyLimitUSD)
	progress, err := f.svc.GetSubscriptionProgress(ctx, f.sub.ID)
	require.NoError(t, err)
	require.NotNil(t, progress.Daily)
	require.True(t, today.AddDate(0, 0, 1).Equal(progress.Daily.ResetsAt))
	_, err = f.svc.ValidateAndCheckLimits(&subs[0], nil)
	require.ErrorIs(t, err, service.ErrDailyLimitExceeded)
	persisted, err := f.c.UserSubscription.Get(ctx, f.sub.ID)
	require.NoError(t, err)
	require.True(t, yesterday.Equal(*persisted.DailyWindowStart), "display reads must not mutate the ledger")
	require.InDelta(t, 90.21295472, persisted.DailyUsageUsd, 1e-8)
}
