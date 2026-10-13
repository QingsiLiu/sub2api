//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGeiliFixedCacheChargeUsesSubscriptionOwner(t *testing.T) {
	now := time.Now()
	daily := 5.0
	group := &Group{ID: 2, SubscriptionType: SubscriptionTypeStandard}
	sub := &UserSubscription{ID: 8, UserID: 1, GroupID: 99, Status: SubscriptionStatusActive, ExpiresAt: now.Add(time.Hour), Plan: &SubscriptionQuotaPlan{DailyLimitUSD: &daily}, DailyUsageUSD: 3}
	repo := &fixedChargeSubscriptionRepoStub{sub: sub}
	cfg := &config.Config{}
	cfg.Billing.InflightReservation.Enabled = true
	cache := &fixedChargeCacheStub{balance: 0}
	svc := NewBillingCacheService(cache, nil, repo, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)
	key := &APIKey{ID: 3, BillingSource: BillingSourceSubscription, Group: group}
	user := &User{ID: 1}
	first, err := svc.ReserveFixedCharge(context.Background(), user, key, group, sub, 2)
	require.NoError(t, err, "subscription charge must not require balance on a standard provider group")
	_, err = svc.ReserveFixedCharge(context.Background(), user, key, group, sub, .01)
	require.ErrorIs(t, err, ErrDailyLimitExceeded, "pending storage charges consume the plan headroom")
	first.Release()
	_, err = svc.ReserveFixedCharge(context.Background(), user, key, group, sub, 2.01)
	require.ErrorIs(t, err, ErrDailyLimitExceeded)
	sub.UserID = 2
	_, err = svc.ReserveFixedCharge(context.Background(), user, key, group, sub, 1)
	require.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestGeiliFixedCacheChargeUsesSharedEntitlements(t *testing.T) {
	now := time.Now()
	daily := 5.0
	sub := &UserSubscription{ID: 8, UserID: 1, Status: SubscriptionStatusActive, ExpiresAt: now.Add(time.Hour), DailyUsageUSD: 3, Entitlements: []SubscriptionEntitlement{{ID: 1, Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), DailyLimitUSD: &daily}}}
	svc := NewBillingCacheService(nil, nil, &fixedChargeSubscriptionRepoStub{sub: sub}, nil, nil, nil, &config.Config{}, nil)
	t.Cleanup(svc.Stop)
	available := sub.AggregateQuotaSummary().AvailableUSD
	require.Positive(t, available)
	require.NoError(t, svc.checkFixedChargeSubscriptionGeili(context.Background(), 1, &Group{ID: 77}, sub, available))
	require.ErrorIs(t, svc.checkFixedChargeSubscriptionGeili(context.Background(), 1, &Group{ID: 77}, sub, available+.01), ErrDailyLimitExceeded)
}

func TestGeiliCacheQuoteUsesExplicitSubscriptionMultiplier(t *testing.T) {
	subRate := .25
	groupID := int64(2)
	svc := &GatewayService{}
	key := &APIKey{GroupID: &groupID, Group: &Group{ID: groupID, RateMultiplier: 2, SubscriptionRateMultiplier: &subRate}, BillingSource: BillingSourceSubscription}
	rate, _, _ := svc.recordUsageMultipliers(context.Background(), key, nil, time.Now())
	require.Equal(t, .25, rate)
	key.BillingSource = BillingSourceBalance
	rate, _, _ = svc.recordUsageMultipliers(context.Background(), key, nil, time.Now())
	require.Equal(t, 2.0, rate)
	subRate = 0
	key.BillingSource = BillingSourceSubscription
	rate, _, _ = svc.recordUsageMultipliers(context.Background(), key, nil, time.Now())
	require.Zero(t, rate)
}
