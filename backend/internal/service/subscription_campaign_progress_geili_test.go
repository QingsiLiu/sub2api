package service

import (
	"context"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestSubscriptionCampaignProgressWithoutPlanOrGroup(t *testing.T) {
	now := time.Now()
	// Unmigrated lots use the configured zone; the contract ledger uses Beijing.
	day := timezone.StartOfDay(now)
	limit := 45.0
	repo := newSubscriptionUserSubRepoStub()
	sub := &UserSubscription{ID: 101, UserID: 7, Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(7 * 24 * time.Hour), Entitlements: []geilisub.Lot{{ID: 1, SourceType: "campaign", Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(7 * 24 * time.Hour), DailyLimitUSD: &limit, DailyWindowStart: &day, DailyUsageUSD: 3}}}
	repo.seed(sub)
	svc := &SubscriptionService{userSubRepo: repo} // nil group repo proves no group lookup
	progress, err := svc.GetSubscriptionProgress(context.Background(), sub.ID)
	require.NoError(t, err)
	require.Equal(t, "活动赠礼", progress.GroupName)
	require.NotNil(t, progress.Daily)
	require.Equal(t, 45.0, progress.Daily.LimitUSD)
	require.Equal(t, 42.0, progress.Daily.RemainingUSD)
	sub.Contract = &geilisub.Contract{Mode: geilisub.ContractModeLegacy, StartsAt: sub.StartsAt, ExpiresAt: sub.ExpiresAt, Status: "active"}
	sub.DailyUsageUSD = 3
	sub.QuotaUsageDate = geilisub.DayStart(now)
	repo.seed(sub)
	progress, err = svc.GetSubscriptionProgress(context.Background(), sub.ID)
	require.NoError(t, err)
	require.Equal(t, 42.0, progress.Daily.RemainingUSD)
}
