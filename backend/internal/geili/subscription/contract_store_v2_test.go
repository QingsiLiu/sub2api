package subscription

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func v2Store(t *testing.T) (*dbent.Client, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:v2-"+uuid.NewString()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	c := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = c.Close() })
	schema, err := migrations.FS.ReadFile("253_subscription_contract_v2.sql")
	require.NoError(t, err)
	ddl := strings.Split(string(schema), "DO $$")[0]
	ddl = strings.NewReplacer("TIMESTAMPTZ", "DATETIME", "BIGSERIAL PRIMARY KEY", "INTEGER PRIMARY KEY AUTOINCREMENT", "NOW()", "CURRENT_TIMESTAMP").Replace(ddl)
	_, err = db.Exec(ddl)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE subscription_usage_allocations(id INTEGER PRIMARY KEY AUTOINCREMENT,request_key TEXT NOT NULL,entitlement_id BIGINT NOT NULL,cost_usd NUMERIC NOT NULL,daily_window_start DATETIME,weekly_window_start DATETIME,monthly_window_start DATETIME,UNIQUE(request_key,entitlement_id))`)
	require.NoError(t, err)
	return c, db
}
func v2Parent(t *testing.T, c *dbent.Client, daily float64, days int, now time.Time) (*dbent.UserSubscription, *dbent.SubscriptionPlan) {
	t.Helper()
	ctx := context.Background()
	owner, err := c.User.Create().SetEmail(uuid.NewString() + "@test.invalid").SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	p, err := c.SubscriptionPlan.Create().SetName("test").SetPrice(30).SetDailyLimitUsd(daily).SetValidityDays(days).Save(ctx)
	require.NoError(t, err)
	parent, err := c.UserSubscription.Create().SetUserID(owner.ID).SetPlanID(p.ID).SetStartsAt(now.Add(-24 * time.Hour)).SetExpiresAt(now.Add(72 * time.Hour)).Save(ctx)
	require.NoError(t, err)
	return parent, p
}
func v2LegacyLot(t *testing.T, c *dbent.Client, s *dbent.UserSubscription, p *dbent.SubscriptionPlan, used float64, now time.Time) *dbent.UserSubscriptionEntitlement {
	t.Helper()
	day := DayStart(now)
	lot, err := c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(s.ID).SetPlanID(p.ID).SetStartsAt(s.StartsAt).SetExpiresAt(s.ExpiresAt).SetDailyLimitUsd(*p.DailyLimitUsd).SetDailyWindowStart(day).SetDailyUsageUsd(used).SetLifetimeUsageUsd(used).Save(context.Background())
	require.NoError(t, err)
	return lot
}
func v2Order(t *testing.T, c *dbent.Client, s *dbent.UserSubscription) *dbent.PaymentOrder {
	t.Helper()
	now := time.Now()
	o, err := c.PaymentOrder.Create().SetUserID(s.UserID).SetUserEmail("test@test.invalid").SetUserName("test").SetAmount(30).SetPayAmount(30).SetRechargeCode(uuid.NewString()).SetPaymentType("test").SetPaymentTradeNo("").SetOutTradeNo(uuid.NewString()).SetOrderType("subscription").SetPlanID(*s.PlanID).SetStatus("PAID").SetPaidAt(now).SetExpiresAt(now.Add(time.Hour)).SetClientIP("127.0.0.1").SetSrcHost("test.invalid").Save(context.Background())
	require.NoError(t, err)
	return o
}
func TestContractStoreV2BaselineReplayAndOldWriterBridge(t *testing.T) {
	c, db := v2Store(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	s, p := v2Parent(t, c, 45, 30, now)
	lot := v2LegacyLot(t, c, s, p, 41.92257312, now)
	old := uuid.NewString()
	require.NoError(t, c.SubscriptionRequest.Create().SetRequestKey(old).SetSubscriptionID(s.ID).SetAPIKeyID(1).SetLots([]byte("[]")).SetAdmittedAt(now.Add(-time.Minute)).Exec(ctx))
	_, err := db.Exec(`INSERT INTO subscription_usage_allocations(request_key,entitlement_id,cost_usd) VALUES(?,?,?)`, old, lot.ID, 41.92257312)
	require.NoError(t, err)
	contract, err := EnsureContract(ctx, c, s.ID, now)
	require.NoError(t, err)
	require.Equal(t, ContractModeV2, contract.Mode)
	again, err := EnsureContract(ctx, c, s.ID, now)
	require.NoError(t, err)
	require.Equal(t, contract.TermID, again.TermID)
	used, err := ReadDailyUsage(ctx, c, s.ID, contract.TermID, now)
	require.NoError(t, err)
	require.Equal(t, 41.92257312, used)
	late := uuid.NewString()
	require.NoError(t, c.SubscriptionRequest.Create().SetRequestKey(late).SetSubscriptionID(s.ID).SetAPIKeyID(1).SetLots([]byte("[]")).SetAdmittedAt(now).Exec(ctx))
	_, err = db.Exec(`INSERT INTO subscription_usage_allocations(request_key,entitlement_id,cost_usd) VALUES(?,?,?)`, late, lot.ID, .1234)
	require.NoError(t, err)
	used, err = ReadDailyUsage(ctx, c, s.ID, contract.TermID, now)
	require.NoError(t, err)
	require.InDelta(t, 42.04597312, used, 1e-10)
	for i := 0; i < 2; i++ {
		require.NoError(t, SyncDailyLedger(ctx, c, s.ID, now))
	}
	used, err = ReadDailyUsage(ctx, c, s.ID, contract.TermID, now)
	require.NoError(t, err)
	require.InDelta(t, 42.04597312, used, 1e-10)
	var bound string
	require.NoError(t, db.QueryRow(`SELECT term_id FROM subscription_request_contracts WHERE request_key=?`, late).Scan(&bound))
	require.Equal(t, contract.TermID, bound)
}
func TestContractStoreV2ChangesKeepQuotaAndRefundGuard(t *testing.T) {
	c, _ := v2Store(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	s, p := v2Parent(t, c, 45, 30, now)
	v2LegacyLot(t, c, s, p, 12.5, now)
	contract, err := EnsureContract(ctx, c, s.ID, now)
	require.NoError(t, err)
	plan := PlanFromEntity(p)
	change, err := PreviewContract(contract, plan, plan, "stack", 2, 0, now)
	require.NoError(t, err)
	order := v2Order(t, c, s)
	got, err := ApplyContractChange(ctx, c, change, order.ID, "payment", fmt.Sprint(order.ID), 0, now)
	require.NoError(t, err)
	require.Equal(t, 3, got.Quantity)
	require.Equal(t, contract.TermID, got.TermID)
	require.Equal(t, contract.ExpiresAt, got.ExpiresAt)
	again, err := ApplyContractChange(ctx, c, change, order.ID, "payment", fmt.Sprint(order.ID), 0, now)
	require.NoError(t, err)
	require.Equal(t, got, again)
	used, err := ReadDailyUsage(ctx, c, s.ID, got.TermID, now)
	require.NoError(t, err)
	require.Equal(t, 12.5, used)
	_, err = ValidateContractRefund(ctx, c, order.ID, now)
	require.NoError(t, err)
	_, err = FreezeContractRefund(ctx, c, order.ID, now)
	require.NoError(t, err)
	lots, err := ReadLots(ctx, c, s.ID)
	require.NoError(t, err)
	require.ErrorIs(t, CheckMutable(lots), ErrStateConflict)
	_, err = RestoreContractRefund(ctx, c, order.ID, now)
	require.NoError(t, err)
	_, err = FreezeContractRefund(ctx, c, order.ID, now)
	require.NoError(t, err)
	reverted, err := RevertContractChange(ctx, c, order.ID, now)
	require.NoError(t, err)
	require.Equal(t, 1, reverted.Quantity)
	require.Greater(t, reverted.Revision, got.Revision)
	used, err = ReadDailyUsage(ctx, c, s.ID, reverted.TermID, now)
	require.NoError(t, err)
	require.Equal(t, 12.5, used)
}
func TestContractStoreV2UpgradeKeepsHistoricalTargetPool(t *testing.T) {
	c, _ := v2Store(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	s, p := v2Parent(t, c, 45, 30, now)
	v2LegacyLot(t, c, s, p, 0, now)
	high, err := c.SubscriptionPlan.Create().SetName("high").SetPrice(60).SetDailyLimitUsd(90).SetValidityDays(30).Save(ctx)
	require.NoError(t, err)
	expired, err := c.UserSubscription.Create().SetUserID(s.UserID).SetPlanID(high.ID).SetStartsAt(now.Add(-31 * 24 * time.Hour)).SetExpiresAt(now.Add(-24 * time.Hour)).SetStatus("expired").Save(ctx)
	require.NoError(t, err)
	contract, err := EnsureContract(ctx, c, s.ID, now)
	require.NoError(t, err)
	change, err := PreviewContract(contract, PlanFromEntity(p), PlanFromEntity(high), "upgrade", 0, 0, now)
	require.NoError(t, err)
	got, err := ApplyContractChange(ctx, c, change, v2Order(t, c, s).ID, "payment", "test", 0, now)
	require.NoError(t, err)
	require.Equal(t, s.ID, got.SubscriptionID)
	require.Equal(t, high.ID, got.PlanID)
	untouched, err := c.UserSubscription.Get(ctx, expired.ID)
	require.NoError(t, err)
	require.Equal(t, expired.ExpiresAt, untouched.ExpiresAt)
	require.Equal(t, "expired", untouched.Status)
}
func TestContractStoreV2SpecialLegacyNeverUpgradesAutomatically(t *testing.T) {
	c, _ := v2Store(t)
	ctx := context.Background()
	now := time.Now()
	s, p := v2Parent(t, c, 45, 30, now)
	require.NoError(t, c.SubscriptionPlan.UpdateOneID(p.ID).SetIsLegacyCompat(true).Exec(ctx))
	v2LegacyLot(t, c, s, p, 0, now)
	contract, err := EnsureContract(ctx, c, s.ID, now)
	require.NoError(t, err)
	require.Equal(t, ContractModeLegacy, contract.Mode)
}

func TestContractStoreV2LateOldRequestCannotChargeRepurchase(t *testing.T) {
	c, db := v2Store(t)
	ctx := context.Background()
	now := DayStart(time.Now()).Add(10 * time.Hour)
	s, p := v2Parent(t, c, 45, 30, now)
	s.ExpiresAt = now.Add(time.Hour)
	require.NoError(t, c.UserSubscription.UpdateOneID(s.ID).SetExpiresAt(s.ExpiresAt).Exec(ctx))
	lot := v2LegacyLot(t, c, s, p, 5, now)
	old, err := EnsureContract(ctx, c, s.ID, now)
	require.NoError(t, err)
	request := uuid.NewString()
	require.NoError(t, c.SubscriptionRequest.Create().SetRequestKey(request).SetSubscriptionID(s.ID).SetAPIKeyID(1).SetLots([]byte("[]")).SetAdmittedAt(now).Exec(ctx))
	repurchaseAt := now.Add(2 * time.Hour)
	plan := PlanFromEntity(p)
	change, err := PreviewContract(old, plan, plan, "purchase", 1, 0, repurchaseAt)
	require.NoError(t, err)
	change.After.SubscriptionID = s.ID
	change.After.UserID = s.UserID
	fresh, err := ApplyContractChange(ctx, c, change, v2Order(t, c, s).ID, "payment", "repurchase", 0, repurchaseAt)
	require.NoError(t, err)
	require.NotEqual(t, old.TermID, fresh.TermID)
	_, err = db.Exec(`INSERT INTO subscription_usage_allocations(request_key,entitlement_id,cost_usd) VALUES(?,?,?)`, request, lot.ID, 2.5)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		require.NoError(t, SyncDailyLedger(ctx, c, s.ID, repurchaseAt))
	}
	oldUsed, err := ReadDailyUsage(ctx, c, s.ID, old.TermID, repurchaseAt)
	require.NoError(t, err)
	require.Equal(t, 7.5, oldUsed)
	newUsed, err := ReadDailyUsage(ctx, c, s.ID, fresh.TermID, repurchaseAt)
	require.NoError(t, err)
	require.Zero(t, newUsed)
	var term string
	require.NoError(t, db.QueryRow(`SELECT term_id FROM subscription_request_contracts WHERE request_key=?`, request).Scan(&term))
	require.Equal(t, old.TermID, term)
}

func TestContractStoreV2LateSettlementCannotChargeTomorrow(t *testing.T) {
	c, db := v2Store(t)
	ctx := context.Background()
	now := DayStart(time.Now()).Add(23*time.Hour + 59*time.Minute)
	s, p := v2Parent(t, c, 45, 30, now)
	lot := v2LegacyLot(t, c, s, p, 5, now)
	contract, err := EnsureContract(ctx, c, s.ID, now)
	require.NoError(t, err)
	request := uuid.NewString()
	require.NoError(t, c.SubscriptionRequest.Create().SetRequestKey(request).SetSubscriptionID(s.ID).SetAPIKeyID(1).SetLots([]byte("[]")).SetAdmittedAt(now).Exec(ctx))
	require.NoError(t, BindAdmission(ctx, c, request, contract, now))
	_, err = db.Exec(`INSERT INTO subscription_usage_allocations(request_key,entitlement_id,cost_usd) VALUES(?,?,?)`, request, lot.ID, 2.5)
	require.NoError(t, err)
	tomorrow := now.Add(2 * time.Minute)
	require.NoError(t, SyncDailyLedger(ctx, c, s.ID, tomorrow))
	yesterdayUsed, err := ReadDailyUsage(ctx, c, s.ID, contract.TermID, now)
	require.NoError(t, err)
	require.Equal(t, 7.5, yesterdayUsed)
	todayUsed, err := ReadDailyUsage(ctx, c, s.ID, contract.TermID, tomorrow)
	require.NoError(t, err)
	require.Zero(t, todayUsed)
}

func TestContractStoreV2PendingRequestsNeverAgeIntoRefundability(t *testing.T) {
	c, _ := v2Store(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	s, p := v2Parent(t, c, 45, 30, now)
	v2LegacyLot(t, c, s, p, 0, now)
	contract, err := EnsureContract(ctx, c, s.ID, now)
	require.NoError(t, err)
	plan := PlanFromEntity(p)
	change, err := PreviewContract(contract, plan, plan, "renew", 0, 1, now)
	require.NoError(t, err)
	order := v2Order(t, c, s)
	_, err = ApplyContractChange(ctx, c, change, order.ID, "payment", "renew", 0, now)
	require.NoError(t, err)
	require.NoError(t, c.SubscriptionRequest.Create().SetRequestKey(uuid.NewString()).SetSubscriptionID(s.ID).SetAPIKeyID(1).SetLots([]byte("[]")).SetAdmittedAt(now.Add(-100*24*time.Hour)).Exec(ctx))
	_, err = ValidateContractRefund(ctx, c, order.ID, now)
	require.ErrorIs(t, err, ErrContractRefund)
}
