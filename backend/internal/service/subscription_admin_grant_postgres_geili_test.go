//go:build unit && integration

package service

import (
	"context"
	"regexp"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/ent/usersubscriptionentitlement"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionV2PostgresAdminGrant(t *testing.T) {
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
	raw, err = migrations.FS.ReadFile("246_subscription_route_integrity.sql")
	require.NoError(t, err)
	_, err = db.Exec(regexp.MustCompile(`(?s)CREATE OR REPLACE FUNCTION geili_grant_unified_subscription\(\).*?EXECUTE FUNCTION geili_grant_unified_subscription\(\);`).FindString(string(raw)))
	require.NoError(t, err)
	// The trigger inserts into user_subscription_groups without created_at, which
	// production survives because migration 234 declares DEFAULT NOW(). Ent's
	// Schema.Create omits that DB default (it applies the value in Go), so the
	// test schema must restore it before the trigger can run.
	_, err = db.Exec(`ALTER TABLE user_subscription_groups ALTER COLUMN created_at SET DEFAULT NOW()`)
	require.NoError(t, err)
	unifiedGroup, err := c.Group.Create().SetName("全模型订阅").SetPlatform("composite").SetSubscriptionType("subscription").Save(ctx)
	require.NoError(t, err)
	unified := unifiedGroup.ID

	pay, _, plans := v2PaymentFixtureWithClient(t, c)
	svc := &SubscriptionService{entClient: c}
	campaigns := NewBenefitCampaignService(c, nil)
	now := time.Now().UTC().Truncate(time.Microsecond)
	admin, err := c.User.Create().SetEmail("grant-admin@example.invalid").SetPasswordHash("synthetic").SetRole("admin").Save(ctx)
	require.NoError(t, err)
	user := func(label string) *dbent.User {
		u, e := c.User.Create().SetEmail(label + "@example.invalid").SetPasswordHash("synthetic").Save(ctx)
		require.NoError(t, e)
		return u
	}
	ca, err := campaigns.Create(ctx, CreateBenefitCampaignInput{Slug: "grant-gift", Title: "gift", StartsAt: now.Add(-2 * time.Hour), ClaimEndsAt: now.Add(time.Hour), EligibilityStartsAt: now.Add(-48 * time.Hour), EligibilityEndsAt: now.Add(-24 * time.Hour), DurationDays: 7, DailyLimitUSD: 45}, admin.ID)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE benefit_campaigns SET eligibility_snapshot_at=$2,status='active' WHERE id=$1`, ca.ID, now)
	require.NoError(t, err)
	// gifted claims a real 45/day campaign pool an hour ago and spent 30 today.
	gifted := func(label string) (*dbent.User, int64, int64) {
		u := user(label)
		_, e := db.Exec(`INSERT INTO benefit_campaign_eligibility(campaign_id,user_id) VALUES($1,$2)`, ca.ID, u.ID)
		require.NoError(t, e)
		_, e = campaigns.Claim(ctx, u.ID, ca.Slug, "claim-"+label, now.Add(-time.Hour))
		require.NoError(t, e)
		pool, e := c.UserSubscription.Query().Where(usersubscription.UserIDEQ(u.ID)).Only(ctx)
		require.NoError(t, e)
		// Claim ignores its claimAt argument and stamps the pool with the real
		// clock, so backdate it explicitly to model a gift claimed an hour ago.
		// The contract inherits this start, keeping it older than the gift's
		// forced expiry below and so inside the retired-usage window.
		startedAt := now.Add(-time.Hour)
		_, e = db.Exec(`UPDATE user_subscriptions SET starts_at=$2 WHERE id=$1`, pool.ID, startedAt)
		require.NoError(t, e)
		gift, e := c.UserSubscriptionEntitlement.Query().Where(usersubscriptionentitlement.UserSubscriptionIDEQ(pool.ID)).Only(ctx)
		require.NoError(t, e)
		contract, e := geilisub.EnsureContract(ctx, c, pool.ID, now)
		require.NoError(t, e)
		day := geilisub.DayStart(now)
		require.NoError(t, c.UserSubscriptionEntitlement.UpdateOneID(gift.ID).SetDailyUsageUsd(30).SetDailyWindowStart(day).Exec(ctx))
		_, e = db.Exec(`INSERT INTO subscription_daily_usage(subscription_id,term_id,usage_date,used_usd) VALUES($1,$2,$3,30) ON CONFLICT(subscription_id,term_id,usage_date) DO UPDATE SET used_usd=30`, pool.ID, contract.TermID, day.Format("2006-01-02"))
		require.NoError(t, e)
		return u, pool.ID, gift.ID
	}
	grant := func(uid int64, daily float64, days int, key string, apply bool, snapshot string) (*AdminGrantResult, error) {
		return svc.GrantAdminEntitlement(ctx, admin.ID, AdminGrantRequest{UserID: uid, DailyLimitUSD: daily, Days: days, Reason: "offline order", IdempotencyKey: key, Apply: apply, ExpectedSnapshot: snapshot})
	}
	apply := func(t *testing.T, uid int64, daily float64, days int, key string) *AdminGrantResult {
		preview, e := grant(uid, daily, days, "", false, "")
		require.NoError(t, e)
		out, e := grant(uid, daily, days, key, true, preview.Snapshot)
		require.NoError(t, e)
		require.True(t, out.Applied)
		return out
	}

	t.Run("gift-plus-grant-stacks-then-falls-back", func(t *testing.T) {
		u, sid, giftID := gifted("stack")
		preview, err := grant(u.ID, 360, 30, "", false, "")
		require.NoError(t, err)
		require.False(t, preview.Applied)
		require.Equal(t, sid, preview.SubscriptionID)
		require.Equal(t, 45.0, *preview.CurrentDailyLimitUSD)
		require.Equal(t, 405.0, *preview.AfterDailyLimitUSD)
		require.Equal(t, 30.0, preview.DailyUsageUSD)
		require.Len(t, preview.Timeline, 2)
		require.Equal(t, 405.0, *preview.Timeline[0].DailyLimitUSD)
		require.Equal(t, 360.0, *preview.Timeline[1].DailyLimitUSD)
		require.Contains(t, preview.Warnings, "self_service_paused")
		require.NotContains(t, preview.Warnings, "creates_pool")
		lots, err := geilisub.ReadLots(ctx, c, sid)
		require.NoError(t, err)
		require.Len(t, lots, 1, "preview must roll back")

		_, err = grant(u.ID, 360, 30, "", true, preview.Snapshot)
		require.Equal(t, "ADMIN_GRANT_IDEMPOTENCY_KEY_REQUIRED", infraerrors.Reason(err))
		_, err = grant(u.ID, 360, 30, "grant-stack-1", true, "stale")
		require.Equal(t, "ADMIN_GRANT_SNAPSHOT_MISMATCH", infraerrors.Reason(err))
		_, err = grant(u.ID, 360, 31, "grant-stack-1", true, preview.Snapshot)
		require.Equal(t, "ADMIN_GRANT_SNAPSHOT_MISMATCH", infraerrors.Reason(err), "snapshot binds the reviewed terms")

		out, err := grant(u.ID, 360, 30, "grant-stack-1", true, preview.Snapshot)
		require.NoError(t, err)
		require.True(t, out.Applied)
		require.NotZero(t, out.EntitlementID)
		again, err := grant(u.ID, 360, 30, "grant-stack-1", true, "anything")
		require.NoError(t, err)
		require.Equal(t, out.EntitlementID, again.EntitlementID, "replay returns the saved grant")
		_, err = grant(u.ID, 180, 30, "grant-stack-1", true, preview.Snapshot)
		require.Equal(t, "ADMIN_GRANT_IDEMPOTENCY_KEY_REUSED", infraerrors.Reason(err))
		lots, err = geilisub.ReadLots(ctx, c, sid)
		require.NoError(t, err)
		require.Len(t, lots, 2)

		view, err := svc.AdminEntitlements(ctx, sid)
		require.NoError(t, err)
		require.Equal(t, geilisub.ContractModeLegacy, view.Contract.Mode)
		require.Equal(t, 405.0, *view.Summary.DailyLimitUSD)
		require.Equal(t, 375.0, *view.Summary.RemainingUSD, "today's gift usage carries over")
		require.Equal(t, "admin_grant", view.Operations[0].Operation)
		require.Equal(t, "offline order", view.Operations[0].Reason)
		require.Equal(t, admin.Email, view.Operations[0].ActorEmail)
		parent, err := c.UserSubscription.Get(ctx, sid)
		require.NoError(t, err)
		require.True(t, parent.ExpiresAt.Equal(out.ExpiresAt), "pool expiry follows the grant")

		_, err = pay.QuoteSubscription(ctx, SubscriptionQuoteRequest{UserID: u.ID, PlanID: plans[3].ID, Operation: "purchase", Units: 1})
		require.Equal(t, "SUBSCRIPTION_COMPATIBILITY_MODE", infraerrors.Reason(err), "self-service pauses while the grant is live")

		// The gift expires today: its share of today's usage retires with it.
		require.NoError(t, c.UserSubscriptionEntitlement.UpdateOneID(giftID).SetExpiresAt(time.Now().Add(-time.Second)).Exec(ctx))
		view, err = svc.AdminEntitlements(ctx, sid)
		require.NoError(t, err)
		require.Equal(t, 360.0, *view.Summary.DailyLimitUSD)
		require.Equal(t, 360.0, *view.Summary.RemainingUSD, "the gift's retired usage frees the grant")
		require.Equal(t, 0.0, view.Summary.DailyUsageUSD)
	})

	t.Run("no-pool-creates-unified-pool", func(t *testing.T) {
		u := user("fresh")
		preview, err := grant(u.ID, 360, 30, "", false, "")
		require.NoError(t, err)
		require.True(t, preview.CreatedPool)
		require.Zero(t, preview.SubscriptionID)
		require.Equal(t, 0.0, *preview.CurrentDailyLimitUSD)
		require.Equal(t, 360.0, *preview.AfterDailyLimitUSD)
		require.Contains(t, preview.Warnings, "creates_pool")
		out, err := grant(u.ID, 360, 30, "grant-fresh-1", true, preview.Snapshot)
		require.NoError(t, err)
		pool, err := c.UserSubscription.Get(ctx, out.SubscriptionID)
		require.NoError(t, err)
		require.Nil(t, pool.GroupID)
		require.Nil(t, pool.PlanID)
		require.Equal(t, &admin.ID, pool.AssignedBy)
		var linked int64
		require.NoError(t, db.QueryRow(`SELECT group_id FROM user_subscription_groups WHERE user_subscription_id=$1`, pool.ID).Scan(&linked))
		require.Equal(t, unified, linked)
		view, err := svc.AdminEntitlements(ctx, pool.ID)
		require.NoError(t, err)
		require.Equal(t, 360.0, *view.Summary.DailyLimitUSD)
	})

	t.Run("v2-pool-converts-without-losing-paid-quota", func(t *testing.T) {
		paid := v2Fulfill(t, pay, v2CreateOrderForCampaignTest(t, pay, user("v2-owner"), plans[3]))
		parent, err := c.UserSubscription.Get(ctx, paid.SubscriptionID)
		require.NoError(t, err)
		preview, err := grant(parent.UserID, 360, 30, "", false, "")
		require.NoError(t, err)
		require.True(t, preview.ConvertsFromV2)
		require.Equal(t, 90.0, *preview.CurrentDailyLimitUSD)
		require.Equal(t, 450.0, *preview.AfterDailyLimitUSD)
		require.Contains(t, preview.Warnings, "converts_from_v2")
		require.NotContains(t, preview.Warnings, "conversion_changes_quota")
		_, err = grant(parent.UserID, 360, 30, "grant-v2-0001", true, preview.Snapshot)
		require.NoError(t, err)
		view, err := svc.AdminEntitlements(ctx, paid.SubscriptionID)
		require.NoError(t, err)
		require.Equal(t, geilisub.ContractModeLegacy, view.Contract.Mode)
		require.Equal(t, paid.TermID, view.Contract.TermID, "today's ledger continues")
		require.Equal(t, 450.0, *view.Summary.DailyLimitUSD)
	})

	t.Run("rejects-ambiguous-or-busy-pools", func(t *testing.T) {
		u, sid, _ := gifted("two-pools")
		_, err := c.UserSubscription.Create().SetUserID(u.ID).SetStartsAt(now).SetExpiresAt(now.Add(24 * time.Hour)).SetStatus(SubscriptionStatusActive).Save(ctx)
		require.NoError(t, err)
		_, err = grant(u.ID, 360, 30, "", false, "")
		require.Equal(t, "ADMIN_GRANT_MULTIPLE_POOLS", infraerrors.Reason(err))

		u, sid, _ = gifted("suspended")
		require.NoError(t, c.UserSubscription.UpdateOneID(sid).SetStatus(SubscriptionStatusSuspended).Exec(ctx))
		_, err = grant(u.ID, 360, 30, "", false, "")
		require.Equal(t, "ADMIN_GRANT_POOL_SUSPENDED", infraerrors.Reason(err))

		owner := user("pending")
		preview, err := grant(owner.ID, 360, 30, "", false, "")
		require.NoError(t, err)
		_, err = v2Create(t, pay, owner, v2Quote(t, pay, owner.ID, plans[3], "purchase", 1, 0))
		require.NoError(t, err)
		_, err = grant(owner.ID, 360, 30, "grant-pending", true, preview.Snapshot)
		require.Equal(t, "SUBSCRIPTION_ORDER_PENDING", infraerrors.Reason(err))

		_, err = grant(999999, 360, 30, "", false, "")
		require.Equal(t, "ADMIN_GRANT_USER_NOT_FOUND", infraerrors.Reason(err))
		_, err = grant(owner.ID, 360, 30, "", false, "")
		require.NoError(t, err, "preview stays available for review")
		_, err = svc.GrantAdminEntitlement(ctx, admin.ID, AdminGrantRequest{UserID: owner.ID, DailyLimitUSD: 360, Days: 30, Reason: " "})
		require.Equal(t, "ADMIN_GRANT_REASON_REQUIRED", infraerrors.Reason(err))
	})

	t.Run("terminate-ends-only-the-grant", func(t *testing.T) {
		u, sid, giftID := gifted("terminate")
		out := apply(t, u.ID, 360, 30, "grant-terminate")
		_, err := svc.TerminateAdminGrant(ctx, admin.ID, sid, giftID, "mistake", "terminate-gift")
		require.Equal(t, "ADMIN_GRANT_NOT_TERMINABLE", infraerrors.Reason(err))
		_, err = svc.TerminateAdminGrant(ctx, admin.ID, sid, out.EntitlementID, "", "terminate-key-1")
		require.Equal(t, "ADMIN_GRANT_REASON_REQUIRED", infraerrors.Reason(err))
		_, err = svc.TerminateAdminGrant(ctx, admin.ID, sid+1000, out.EntitlementID, "mistake", "terminate-key-1")
		require.ErrorIs(t, err, ErrSubscriptionNotFound)

		done, err := svc.TerminateAdminGrant(ctx, admin.ID, sid, out.EntitlementID, "mistake", "terminate-key-1")
		require.NoError(t, err)
		require.Equal(t, 45.0, *done.DailyLimitUSD)
		again, err := svc.TerminateAdminGrant(ctx, admin.ID, sid, out.EntitlementID, "mistake", "terminate-key-1")
		require.NoError(t, err)
		require.True(t, done.TerminatedAt.Equal(again.TerminatedAt))
		_, err = svc.TerminateAdminGrant(ctx, admin.ID, sid, out.EntitlementID, "mistake", "terminate-key-2")
		require.Equal(t, "ADMIN_GRANT_NOT_TERMINABLE", infraerrors.Reason(err))

		view, err := svc.AdminEntitlements(ctx, sid)
		require.NoError(t, err)
		require.Equal(t, 45.0, *view.Summary.DailyLimitUSD)
		require.Equal(t, 15.0, *view.Summary.RemainingUSD, "the gift keeps its own usage")
		require.Equal(t, "admin_terminate", view.Operations[0].Operation)
		require.Equal(t, "mistake", view.Operations[0].Reason)
		parent, err := c.UserSubscription.Get(ctx, sid)
		require.NoError(t, err)
		gift, err := c.UserSubscriptionEntitlement.Get(ctx, giftID)
		require.NoError(t, err)
		require.True(t, parent.ExpiresAt.Equal(gift.ExpiresAt), "pool expiry falls back to the gift")
	})

	t.Run("campaign-claim-extends-legacy-contract", func(t *testing.T) {
		u := user("grant-then-gift")
		out := apply(t, u.ID, 360, 1, "grant-then-gift")
		before, err := geilisub.LoadContract(ctx, c, out.SubscriptionID)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO benefit_campaign_eligibility(campaign_id,user_id) VALUES($1,$2)`, ca.ID, u.ID)
		require.NoError(t, err)
		claimed, err := campaigns.Claim(ctx, u.ID, ca.Slug, "claim-after-grant", time.Now())
		require.NoError(t, err)
		require.Equal(t, out.SubscriptionID, claimed.SubscriptionID, "the gift lands in the granted pool")
		after, err := geilisub.LoadContract(ctx, c, out.SubscriptionID)
		require.NoError(t, err)
		require.Equal(t, geilisub.ContractModeLegacy, after.Mode)
		require.True(t, after.ExpiresAt.Equal(claimed.ExpiresAt), "contract covers the gift")
		require.Equal(t, before.Revision+1, after.Revision)
		var termExpiry time.Time
		require.NoError(t, db.QueryRow(`SELECT expires_at FROM subscription_contract_terms WHERE term_id=$1`, after.TermID).Scan(&termExpiry))
		require.True(t, termExpiry.Equal(claimed.ExpiresAt))
		_, err = pay.QuoteSubscription(ctx, SubscriptionQuoteRequest{UserID: u.ID, PlanID: plans[3].ID, Operation: "purchase", Units: 1})
		require.Equal(t, "SUBSCRIPTION_COMPATIBILITY_MODE", infraerrors.Reason(err))
	})
}
