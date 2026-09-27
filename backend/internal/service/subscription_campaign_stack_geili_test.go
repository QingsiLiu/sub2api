//go:build unit

package service

import (
	"testing"
	"time"

	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionCampaignStackOffer(t *testing.T) {
	now := time.Now().UTC()
	expiry := now.Add(3 * 24 * time.Hour)
	daily := 45.0
	sub := UserSubscription{ID: 1, Status: "active", StartsAt: expiry.Add(-7 * 24 * time.Hour), ExpiresAt: expiry, Entitlements: []geilisub.Lot{{ID: 1, SourceType: "campaign", Status: "active", StartsAt: expiry.Add(-7 * 24 * time.Hour), ExpiresAt: expiry, DailyLimitUSD: &daily}}}
	require.NotNil(t, sub.CampaignStackOffer(now), "newly claimed gift without a contract must show its offer")
	sub.Status = "suspended"
	require.Nil(t, sub.CampaignStackOffer(now))
	sub.Status = "active"
	sub.DeletedAt = &now
	require.Nil(t, sub.CampaignStackOffer(now))
	sub.DeletedAt = nil
	require.Nil(t, sub.CampaignStackOffer(expiry))
	sub.Contract = &geilisub.Contract{Mode: "v2", Status: "active", ExpiresAt: now.Add(-time.Second)}
	require.NotNil(t, sub.CampaignStackOffer(now))
	sub.Contract.ExpiresAt = expiry
	require.Nil(t, sub.CampaignStackOffer(now))
}
