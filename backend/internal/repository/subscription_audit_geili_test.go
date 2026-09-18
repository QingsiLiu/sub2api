//go:build unit && subscriptionaudit && !integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func auditRepoFixture(t *testing.T) (*dbent.Client, *dbent.UserSubscriptionEntitlement) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	c := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	now := time.Now()
	u, err := c.User.Create().SetEmail("audit@example.invalid").SetPasswordHash("synthetic-fixture").Save(ctx)
	require.NoError(t, err)
	plan, err := c.SubscriptionPlan.Create().SetName("audit plan").SetPrice(10).SetValidityDays(30).SetDailyLimitUsd(45).Save(ctx)
	require.NoError(t, err)
	sub, err := c.UserSubscription.Create().SetPlanID(plan.ID).SetUserID(u.ID).SetStartsAt(now.Add(-time.Hour)).SetExpiresAt(now.AddDate(0, 0, 30)).SetDailyUsageUsd(5).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	lot, err := c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(sub.ID).SetStartsAt(sub.StartsAt).SetExpiresAt(sub.ExpiresAt).SetDailyLimitUsd(45).SetDailyUsageUsd(5).SetLifetimeUsageUsd(5).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	return c, lot
}

func TestSubscriptionAuditMappingPreservesPurchaseDate(t *testing.T) {
	c, lot := auditRepoFixture(t)
	sub, err := NewUserSubscriptionRepository(c).GetByID(context.Background(), lot.UserSubscriptionID)
	require.NoError(t, err)
	require.Len(t, sub.Entitlements, 1)
	require.False(t, sub.Entitlements[0].CreatedAt.IsZero(), "purchase time is persisted and must be returned")
}

func TestSubscriptionAuditPurePlanHasNoFictitiousGroup(t *testing.T) {
	c, lot := auditRepoFixture(t)
	sub, err := NewUserSubscriptionRepository(c).GetByID(context.Background(), lot.UserSubscriptionID)
	require.NoError(t, err)
	require.NotContains(t, sub.EntitledGroupIDs, int64(0))
}

func TestSubscriptionAuditAdminResetReachesLots(t *testing.T) {
	c, lot := auditRepoFixture(t)
	ctx := context.Background()
	now := time.Now()
	repo := NewUserSubscriptionRepository(c)
	require.NoError(t, repo.ResetUsageWindows(ctx, lot.UserSubscriptionID, true, false, false, now, now))
	got, err := c.UserSubscriptionEntitlement.Get(ctx, lot.ID)
	require.NoError(t, err)
	require.Zero(t, got.DailyUsageUsd, "reset must reach the authoritative quota owner")
	require.Equal(t, 5.0, got.LifetimeUsageUsd, "reset must not erase refund eligibility history")
}

func TestSubscriptionAuditSingleLotExpiryAdjustmentReachesLot(t *testing.T) {
	c, lot := auditRepoFixture(t)
	ctx := context.Background()
	newExpiry := lot.ExpiresAt.AddDate(0, 0, 7)
	require.NoError(t, NewUserSubscriptionRepository(c).ExtendExpiry(ctx, lot.UserSubscriptionID, newExpiry))
	got, err := c.UserSubscriptionEntitlement.Get(ctx, lot.ID)
	require.NoError(t, err)
	require.WithinDuration(t, newExpiry, got.ExpiresAt, time.Millisecond, "one-lot admin extension must change effective expiry")
}

func TestSubscriptionAuditLegacyBillingFallback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	mock.ExpectQuery("SELECT id FROM user_subscriptions").WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery("SELECT .* FROM user_subscription_entitlements").WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec("UPDATE user_subscriptions").WithArgs(2.0, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	err = incrementUsageBillingSubscription(context.Background(), tx, 1, 2)
	require.NoError(t, err, "existing aggregate without lots must still record completed usage")
	require.NoError(t, mock.ExpectationsWereMet())
}
