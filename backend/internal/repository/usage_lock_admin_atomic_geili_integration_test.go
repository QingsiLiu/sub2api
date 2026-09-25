//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUsageParentLockAllowsEvidenceForeignKeyAndSerializesSettlement(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	require.NoError(t, geilisub.LockSubscriptionUsage(ctx, tx, f.sub.ID))
	other, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer other.Rollback()
	short, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err = other.ExecContext(short, `INSERT INTO usage_logs(user_id,api_key_id,account_id,subscription_id,request_id,model) VALUES($1,$2,$3,$4,$5,'fixture')`, f.user.ID, f.key.ID, f.account.ID, f.sub.ID, uuid.NewString())
	require.NoError(t, err, "usage evidence FK must not wait for non-key parent update lock")
	require.NoError(t, other.Rollback())
	blocked, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer blocked.Rollback()
	_, err = blocked.ExecContext(ctx, `SET LOCAL lock_timeout='150ms'`)
	require.NoError(t, err)
	require.Error(t, geilisub.LockSubscriptionUsage(ctx, blocked, f.sub.ID), "another settlement must serialize on the parent")
	require.NoError(t, blocked.Rollback())
	require.NoError(t, tx.Rollback())
	strong, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer strong.Rollback()
	require.NoError(t, geilisub.LockSubscription(ctx, strong, f.sub.ID))
	fk, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer fk.Rollback()
	_, err = fk.ExecContext(ctx, `SET LOCAL lock_timeout='150ms'`)
	require.NoError(t, err)
	_, err = fk.ExecContext(ctx, `INSERT INTO usage_logs(user_id,api_key_id,account_id,subscription_id,request_id,model) VALUES($1,$2,$3,$4,$5,'fixture')`, f.user.ID, f.key.ID, f.account.ID, f.sub.ID, uuid.NewString())
	require.Error(t, err, "management/deletion lock remains strong")
}

func TestFirstUsageSettlementKeepsWeeklyMonthlyMirrorsAtSQLPrecision(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	admitted, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.NoError(t, err)
	var raw []byte
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT lots FROM subscription_requests WHERE request_key=$1`, admitted.AdmissionKey).Scan(&raw))
	var lots []geilisub.Lot
	require.NoError(t, json.Unmarshal(raw, &lots))
	require.NotEmpty(t, lots)
	require.Equal(t, 0, lots[0].WeeklyWindowStart.Nanosecond()%1000)
	require.Equal(t, 0, lots[0].MonthlyWindowStart.Nanosecond()%1000)
	// An in-flight old binary can leave nanoseconds in JSON while SQL has microseconds.
	legacyWeek := lots[0].WeeklyWindowStart.Add(789 * time.Nanosecond)
	legacyMonth := lots[0].MonthlyWindowStart.Add(789 * time.Nanosecond)
	_, err = integrationDB.ExecContext(ctx, `UPDATE user_subscription_entitlements SET weekly_window_start=$2,monthly_window_start=$3 WHERE id=$1`, lots[0].ID, legacyWeek, legacyMonth)
	require.NoError(t, err)
	lots[0].WeeklyWindowStart = &legacyWeek
	lots[0].MonthlyWindowStart = &legacyMonth
	raw, err = json.Marshal(lots)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE subscription_requests SET lots=$2::jsonb WHERE request_key=$1`, admitted.AdmissionKey, string(raw))
	require.NoError(t, err)
	billing := NewUsageBillingRepository(f.c, integrationDB)
	result, err := billing.Apply(ctx, &service.UsageBillingCommand{RequestID: uuid.NewString(), APIKeyID: f.key.ID, UserID: f.user.ID, AccountID: f.account.ID, SubscriptionID: &f.sub.ID, SubscriptionAdmissionKey: admitted.AdmissionKey, SubscriptionCost: 2.75})
	require.NoError(t, err)
	require.True(t, result.Applied)
	var weekly, monthly, lifetime float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT weekly_usage_usd,monthly_usage_usd,lifetime_usage_usd FROM user_subscription_entitlements WHERE id=$1`, lots[0].ID).Scan(&weekly, &monthly, &lifetime))
	require.Equal(t, 2.75, weekly)
	require.Equal(t, 2.75, monthly)
	require.Equal(t, 2.75, lifetime)
}

type adminBalanceAuditFailure struct{ service.RedeemCodeRepository }

func (r adminBalanceAuditFailure) Create(context.Context, *service.RedeemCode) error {
	return errors.New("audit unavailable")
}

func TestAdminBalanceRealTransactionRollsBackWithoutEvidence(t *testing.T) {
	ctx := context.Background()
	c := testEntClient(t)
	userRepo := NewUserRepository(c, integrationDB)
	redeemRepo := NewRedeemCodeRepository(c)
	u := mustCreateUser(t, c, &service.User{Email: "admin-atomic-" + uuid.NewString() + "@example.invalid", Balance: 10})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM redeem_codes WHERE used_by=$1`, u.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, u.ID)
		require.NoError(t, err)
	})
	newSvc := func(audit service.RedeemCodeRepository) service.AdminService {
		return service.NewAdminService(nil, userRepo, nil, nil, nil, nil, audit, nil, nil, nil, nil, nil, nil, c, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	}
	_, err := newSvc(adminBalanceAuditFailure{redeemRepo}).UpdateUserBalance(ctx, u.ID, 5, "add", "rollback proof")
	require.Error(t, err)
	got, err := userRepo.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, 10.0, got.Balance)
	got, err = newSvc(redeemRepo).UpdateUserBalance(ctx, u.ID, 5, "add", "committed proof")
	require.NoError(t, err)
	require.Equal(t, 15.0, got.Balance)
	var count int
	var delta float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(value),0) FROM redeem_codes WHERE used_by=$1 AND type='admin_balance'`, u.ID).Scan(&count, &delta))
	require.Equal(t, 1, count)
	require.Equal(t, 5.0, delta)
	got, err = newSvc(redeemRepo).UpdateUserBalance(ctx, u.ID, .000000005, "add", "NUMERIC half-step precision")
	require.NoError(t, err)
	require.InDelta(t, 15.00000001, got.Balance, 1e-12)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(value),0) FROM redeem_codes WHERE used_by=$1 AND type='admin_balance'`, u.ID).Scan(&count, &delta))
	require.Equal(t, 2, count)
	require.InDelta(t, 5.00000001, delta, 1e-12)
}

func TestSetBalanceAuditReadsLockedValueAfterConcurrentDebit(t *testing.T) {
	ctx := context.Background()
	c := testEntClient(t)
	repo := NewUserRepository(c, integrationDB)
	u := mustCreateUser(t, c, &service.User{Email: "set-locked-" + uuid.NewString() + "@example.invalid", Balance: 10})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, u.ID)
		require.NoError(t, err)
	})
	tx, err := c.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	tc := dbent.NewTxContext(ctx, tx)
	require.NoError(t, repo.DeductBalance(tc, u.ID, 3))
	type result struct {
		change service.BalanceChange
		err    error
	}
	done := make(chan result, 1)
	go func() { change, e := repo.SetBalance(ctx, u.ID, 20); done <- result{change, e} }()
	// Keep the UPDATE blocked until its SQL is actually waiting, rather than
	// testing two sequential statements that could never expose stale FROM data.
	require.Eventually(t, func() bool {
		var n int
		e := integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%RETURNING target.balance, u.balance%'`).Scan(&n)
		return e == nil && n > 0
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit())
	r := <-done
	require.NoError(t, r.err)
	require.Equal(t, service.BalanceChange{Old: 7, New: 20}, r.change)
}

func TestPostgresWindowTimestampRounding(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ nanos, storedNanos int }{{499, 0}, {500, 0}, {501, 1000}, {789, 1000}, {999, 1000}, {1499, 1000}, {1500, 2000}, {1501, 2000}} {
		input := time.Date(2026, 9, 26, 0, 0, 0, tc.nanos, time.UTC)
		var stored time.Time
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT $1::timestamptz`, input).Scan(&stored))
		require.Equal(t, tc.storedNanos, stored.Nanosecond())
	}
}
