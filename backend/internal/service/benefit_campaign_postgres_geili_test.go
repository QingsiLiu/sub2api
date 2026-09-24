//go:build unit && integration

package service

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionV2PostgresBenefitCampaign(t *testing.T) {
	c, db := v2PaymentPostgres(t)
	ctx := context.Background()
	raw, err := migrations.FS.ReadFile("251_subscription_entitlement_consistency.sql")
	require.NoError(t, err)
	_, err = db.Exec(regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS subscription_operations \(.*?;`).FindString(string(raw)))
	require.NoError(t, err)
	raw, err = migrations.FS.ReadFile("258_benefit_campaigns.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(raw))
	require.NoError(t, err)
	s := NewBenefitCampaignService(c, nil)
	now := time.Now().UTC().Truncate(time.Microsecond)
	admin, err := c.User.Create().SetEmail("benefit-admin@example.invalid").SetPasswordHash("synthetic").Save(ctx)
	require.NoError(t, err)
	user := func(label string) *dbent.User {
		u, e := c.User.Create().SetEmail(label + "@example.invalid").SetPasswordHash("synthetic").Save(ctx)
		require.NoError(t, e)
		return u
	}
	campaign := func(label string, cap *int64) *BenefitCampaign {
		out, e := s.Create(ctx, CreateBenefitCampaignInput{Slug: label, Title: label, StartsAt: now.Add(-time.Hour), ClaimEndsAt: now.Add(time.Hour), EligibilityStartsAt: now.Add(-48 * time.Hour), EligibilityEndsAt: now.Add(-24 * time.Hour), DurationDays: 7, DailyLimitUSD: 45, MaxClaims: cap}, admin.ID)
		require.NoError(t, e)
		return out
	}
	eligible := func(ca *BenefitCampaign, u *dbent.User) {
		_, e := db.Exec(`INSERT INTO benefit_campaign_eligibility(campaign_id,user_id) VALUES($1,$2)`, ca.ID, u.ID)
		require.NoError(t, e)
		_, e = db.Exec(`UPDATE benefit_campaigns SET eligibility_snapshot_at=$2,status='active' WHERE id=$1`, ca.ID, now)
		require.NoError(t, e)
	}
	t.Run("concurrent-claim-unique-and-durable", func(t *testing.T) {
		cap := int64(1)
		ca := campaign("durable", &cap)
		u := user("durable-user")
		eligible(ca, u)
		var wg sync.WaitGroup
		claims := make([]*BenefitCampaignClaim, 8)
		errs := make([]error, 8)
		for i := range claims {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				claims[i], errs[i] = s.Claim(ctx, u.ID, ca.Slug, fmt.Sprintf("retry-%d", i), now)
			}(i)
		}
		wg.Wait()
		for i := range claims {
			require.NoError(t, errs[i])
			require.Equal(t, claims[0].ID, claims[i].ID)
		}
		require.Equal(t, 7*24*time.Hour, claims[0].ExpiresAt.Sub(claims[0].StartsAt))
		for _, status := range []string{"paused", "closed", "draft"} {
			require.NoError(t, s.SetStatus(ctx, ca.ID, status, admin.ID))
			again, e := s.Claim(ctx, u.ID, ca.Slug, "another-retry", now.Add(48*time.Hour))
			require.NoError(t, e)
			require.Equal(t, claims[0], again)
		}
		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM benefit_campaign_claims WHERE campaign_id=$1`, ca.ID).Scan(&count))
		require.Equal(t, 1, count)
	})
	t.Run("same-client-key-is-scoped-per-user", func(t *testing.T) {
		ca := campaign("shared-key", nil)
		for i := 0; i < 2; i++ {
			u := user(fmt.Sprintf("shared-key-%d", i))
			eligible(ca, u)
			_, e := s.Claim(ctx, u.ID, ca.Slug, "same-client-key", now)
			require.NoError(t, e)
		}
	})
	t.Run("disabled-user-cannot-claim", func(t *testing.T) {
		ca := campaign("disabled", nil)
		u := user("disabled-user")
		eligible(ca, u)
		require.NoError(t, c.User.UpdateOneID(u.ID).SetStatus("disabled").Exec(ctx))
		_, e := s.Claim(ctx, u.ID, ca.Slug, "", now)
		require.Equal(t, "BENEFIT_CAMPAIGN_INELIGIBLE", infraerrors.Reason(e))
	})
	t.Run("snapshot-freezes-once", func(t *testing.T) {
		ca := campaign("frozen", nil)
		u := user("frozen-user")
		eligible(ca, u)
		require.NoError(t, c.User.UpdateOneID(u.ID).SetStatus("disabled").Exec(ctx))
		count, e := s.Snapshot(ctx, ca.ID, now)
		require.NoError(t, e)
		require.Equal(t, int64(1), count)
		var at time.Time
		require.NoError(t, db.QueryRow(`SELECT eligibility_snapshot_at FROM benefit_campaigns WHERE id=$1`, ca.ID).Scan(&at))
		require.True(t, at.Equal(now))
	})
	t.Run("future-window-cannot-freeze-early", func(t *testing.T) {
		ca := campaign("future-freeze", nil)
		_, e := s.Snapshot(ctx, ca.ID, now.Add(-36*time.Hour))
		require.Equal(t, "BENEFIT_CAMPAIGN_SNAPSHOT_REQUIRED", infraerrors.Reason(e))
	})
	t.Run("frozen-terms-and-explicit-closed-view", func(t *testing.T) {
		ca := campaign("frozen-terms", nil)
		u := user("frozen-terms-user")
		eligible(ca, u)
		_, e := s.Claim(ctx, u.ID, ca.Slug, "", now)
		require.NoError(t, e)
		_, e = s.Update(ctx, ca.ID, CreateBenefitCampaignInput{Slug: "renamed", Title: ca.Title, StartsAt: ca.StartsAt, ClaimEndsAt: ca.ClaimEndsAt, EligibilityStartsAt: ca.EligibilityStartsAt, EligibilityEndsAt: ca.EligibilityEndsAt, DurationDays: 7, DailyLimitUSD: 90}, admin.ID)
		require.Equal(t, "BENEFIT_CAMPAIGN_FROZEN", infraerrors.Reason(e))
		require.NoError(t, s.SetStatus(ctx, ca.ID, "closed", admin.ID))
		view, e := s.Current(ctx, u.ID, now.Add(48*time.Hour), ca.Slug)
		require.NoError(t, e)
		require.True(t, view.Claimed)
		require.Equal(t, ca.Slug, view.Campaign.Slug)
		require.Equal(t, "closed", view.Campaign.Status)
	})
	t.Run("deadline-rechecked-after-campaign-lock", func(t *testing.T) {
		ca := campaign("lock-deadline", nil)
		u := user("lock-deadline-user")
		eligible(ca, u)
		s.now = func() time.Time { return ca.ClaimEndsAt.Add(time.Millisecond) }
		defer func() { s.now = nil }()
		block, e := db.BeginTx(ctx, nil)
		require.NoError(t, e)
		_, e = block.ExecContext(ctx, `SELECT id FROM benefit_campaigns WHERE id=$1 FOR UPDATE`, ca.ID)
		require.NoError(t, e)
		errs := make(chan error, 1)
		go func() { _, e := s.Claim(ctx, u.ID, ca.Slug, "", ca.ClaimEndsAt.Add(-time.Second)); errs <- e }()
		waitPaymentLock(t, db, "benefit_campaigns WHERE slug=")
		require.NoError(t, block.Commit())
		e = <-errs
		require.Equal(t, "BENEFIT_CAMPAIGN_CLOSED", infraerrors.Reason(e))
	})
	t.Run("claim-gates", func(t *testing.T) {
		ca := campaign("gates", nil)
		u := user("gate-user")
		_, e := s.Claim(ctx, u.ID, ca.Slug, "", now)
		require.Equal(t, "BENEFIT_CAMPAIGN_CLOSED", infraerrors.Reason(e))
		require.NoError(t, s.SetStatus(ctx, ca.ID, "active", admin.ID))
		_, e = s.Claim(ctx, u.ID, ca.Slug, "", now)
		require.Equal(t, "BENEFIT_CAMPAIGN_SNAPSHOT_REQUIRED", infraerrors.Reason(e))
		n, e := s.Snapshot(ctx, ca.ID, now)
		require.NoError(t, e)
		require.Zero(t, n)
		_, e = s.Claim(ctx, u.ID, ca.Slug, "", now)
		require.Equal(t, "BENEFIT_CAMPAIGN_INELIGIBLE", infraerrors.Reason(e))
	})
}

func TestSubscriptionV2PostgresCampaignPurchaseBoundaries(t *testing.T) {
	c, db := v2PaymentPostgres(t)
	ctx := context.Background()
	s, u, plans := v2PaymentFixtureWithClient(t, c)
	paid := v2Fulfill(t, s, v2CreateOrderForCampaignTest(t, s, u, plans[3]))
	now := time.Now().UTC()
	gift, err := c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(paid.SubscriptionID).SetSourceType("campaign").SetSourceReference("synthetic").SetStartsAt(now.Add(-time.Minute)).SetExpiresAt(now.Add(7 * 24 * time.Hour)).SetDailyLimitUsd(45).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE user_subscriptions SET expires_at=GREATEST(expires_at,$2) WHERE id=$1`, paid.SubscriptionID, gift.ExpiresAt)
	require.NoError(t, err)
	q, err := s.QuoteSubscription(ctx, SubscriptionQuoteRequest{UserID: u.ID, PlanID: plans[3].ID, Operation: "renew", Periods: 1})
	require.NoError(t, err)
	require.Equal(t, 135.0, *q.Current.DailyLimitUSD)
	require.Equal(t, 135.0, *q.Projected.DailyLimitUSD, "projected renewal must retain gift")
	_, err = db.Exec(`UPDATE subscription_contracts SET expires_at=$2 WHERE subscription_id=$1`, paid.SubscriptionID, now.Add(-time.Second))
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE user_subscription_entitlements SET expires_at=$2 WHERE user_subscription_id=$1 AND source_type<>'campaign'`, paid.SubscriptionID, now.Add(-time.Second))
	require.NoError(t, err)
	_, err = s.QuoteSubscription(ctx, SubscriptionQuoteRequest{UserID: u.ID, PlanID: plans[3].ID, Operation: "purchase", Units: 1})
	require.Equal(t, "SUBSCRIPTION_COMPATIBILITY_MODE", infraerrors.Reason(err), "reject before accepting payment while gift pool remains active")
	var mode string
	require.NoError(t, db.QueryRow(`SELECT mode FROM subscription_contracts WHERE subscription_id=$1`, paid.SubscriptionID).Scan(&mode))
	require.Equal(t, "v2", mode, "quote must not persistently downgrade paid contract")
}

func v2CreateOrderForCampaignTest(t *testing.T, s *PaymentService, u *dbent.User, p *dbent.SubscriptionPlan) *dbent.PaymentOrder {
	t.Helper()
	q := v2Quote(t, s, u.ID, p, "purchase", 1, 0)
	o, e := v2Create(t, s, u, q)
	require.NoError(t, e)
	return o
}
