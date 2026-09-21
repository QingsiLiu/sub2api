//go:build unit && integration

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionplan"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func v2PaymentPostgres(t *testing.T) (*dbent.Client, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	image := os.Getenv("SUB2API_TEST_POSTGRES_IMAGE")
	if image == "" {
		image = "postgres:18.1-alpine3.23"
	}
	container, err := tcpostgres.Run(ctx, image, tcpostgres.WithDatabase("payment_v2"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("synthetic"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(20)
	t.Cleanup(func() { _ = db.Close() })
	c := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.Schema.Create(ctx))
	raw, err := migrations.FS.ReadFile("251_subscription_entitlement_consistency.sql")
	require.NoError(t, err)
	allocation := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS subscription_usage_allocations \(.*?;`).FindString(string(raw))
	_, err = db.Exec(allocation)
	require.NoError(t, err)
	raw, err = migrations.FS.ReadFile("253_subscription_contract_v2.sql")
	require.NoError(t, err)
	_, err = db.Exec(strings.Split(string(raw), "DO $$")[0])
	require.NoError(t, err)
	return c, db
}

func waitPaymentLock(t *testing.T, db *sql.DB, fragment string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var n int
		err := db.QueryRow(`SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1`, "%"+fragment+"%").Scan(&n)
		return err == nil && n > 0
	}, 5*time.Second, 10*time.Millisecond, "expected PostgreSQL row-lock wait for %s", fragment)
}

func TestSubscriptionV2PostgresPaymentRaces(t *testing.T) {
	c, db := v2PaymentPostgres(t)
	ctx := context.Background()
	t.Run("one-pending-under-eight-concurrent-checkouts", func(t *testing.T) {
		s, u, p := v2PaymentFixtureWithClient(t, c)
		q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
		start := make(chan struct{})
		results := make(chan error, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; _, err := v2Create(t, s, u, q); results <- err }()
		}
		close(start)
		wg.Wait()
		close(results)
		successful, blocked := 0, 0
		for err := range results {
			if err == nil {
				successful++
			} else {
				require.Equal(t, "SUBSCRIPTION_ORDER_PENDING", infraerrors.Reason(err))
				blocked++
			}
		}
		require.Equal(t, 1, successful)
		require.Equal(t, 7, blocked)
	})
	t.Run("pending-refund-and-success-race-is-terminal", func(t *testing.T) {
		s, u, p := v2PaymentFixtureWithClient(t, c)
		q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
		o, err := v2Create(t, s, u, q)
		require.NoError(t, err)
		o, err = c.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusRefunding).SetPaidAt(time.Now()).SetRefundAmount(o.Amount).Save(ctx)
		require.NoError(t, err)
		plan := s.refundFinalizePlan(o)
		var wg sync.WaitGroup
		errs := make(chan error, 10)
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				status := payment.ProviderStatusPending
				if i%2 == 0 {
					status = payment.ProviderStatusSuccess
				}
				_, err := s.finishUnassignedSubscriptionV2Refund(ctx, plan, &payment.RefundResponse{Status: status}, nil)
				errs <- err
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		got, err := c.PaymentOrder.Get(ctx, o.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusRefunded, got.Status)
	})
	t.Run("plan-edit-while-creating-order-invalidates-quote", func(t *testing.T) {
		s, u, p := v2PaymentFixtureWithClient(t, c)
		q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
		locker, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer locker.Rollback()
		_, err = locker.Exec(`UPDATE subscription_plans SET price=price+1,updated_at=NOW() WHERE id=$1`, p[2].ID)
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { _, err := v2Create(t, s, u, q); done <- err }()
		waitPaymentLock(t, db, "subscription_plans")
		require.NoError(t, locker.Commit())
		require.Equal(t, "SUBSCRIPTION_QUOTE_CHANGED", infraerrors.Reason(<-done))
	})
	t.Run("upgrade-quote-reads-both-prices-under-row-lock", func(t *testing.T) {
		s, u, p := v2PaymentFixtureWithClient(t, c)
		q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
		o, err := v2Create(t, s, u, q)
		require.NoError(t, err)
		v2Fulfill(t, s, o)
		locker, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer locker.Rollback()
		_, err = locker.Exec(`UPDATE subscription_plans SET price=90,updated_at=NOW() WHERE id=$1`, p[4].ID)
		require.NoError(t, err)
		done := make(chan *SubscriptionQuoteResponse, 1)
		failed := make(chan error, 1)
		go func() {
			q, err := s.QuoteSubscription(ctx, SubscriptionQuoteRequest{UserID: u.ID, PlanID: p[4].ID, Operation: "upgrade"})
			if err != nil {
				failed <- err
				return
			}
			done <- q
		}()
		waitPaymentLock(t, db, "subscription_plans")
		require.NoError(t, locker.Commit())
		select {
		case err := <-failed:
			require.NoError(t, err)
		case q := <-done:
			require.Equal(t, 75.0, q.OrderAmount)
		case <-time.After(5 * time.Second):
			t.Fatal("quote did not finish")
		}
	})
	t.Run("quote-expiring-during-plan-lock-wait-cannot-charge", func(t *testing.T) {
		s, u, p := v2PaymentFixtureWithClient(t, c)
		q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
		payload, err := s.readSubscriptionV2Quote(q.QuoteID, u.ID, time.Now())
		require.NoError(t, err)
		payload.ExpiresAt = time.Now().Add(300 * time.Millisecond)
		q.QuoteID, err = s.paymentResume().createSignedToken(payload)
		require.NoError(t, err)
		locker, err := c.Tx(ctx)
		require.NoError(t, err)
		defer locker.Rollback()
		_, err = locker.Client().SubscriptionPlan.Query().Unique(false).Where(subscriptionplan.IDEQ(p[2].ID), geilisub.LockRows).Only(ctx)
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { _, err := v2Create(t, s, u, q); done <- err }()
		waitPaymentLock(t, db, "subscription_plans")
		time.Sleep(time.Until(payload.ExpiresAt.Add(20 * time.Millisecond)))
		require.NoError(t, locker.Commit())
		require.Equal(t, "SUBSCRIPTION_QUOTE_EXPIRED", infraerrors.Reason(<-done))
	})
	t.Run("contract-expiring-during-fulfillment-lock-enters-paid-review", func(t *testing.T) {
		s, u, p := v2PaymentFixtureWithClient(t, c)
		q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
		o, err := v2Create(t, s, u, q)
		require.NoError(t, err)
		before := v2Fulfill(t, s, o)
		expiry := time.Now().Add(time.Second).Truncate(time.Microsecond)
		_, err = db.Exec(`UPDATE subscription_contracts SET expires_at=$2 WHERE subscription_id=$1`, before.SubscriptionID, expiry)
		require.NoError(t, err)
		require.NoError(t, c.UserSubscription.UpdateOneID(before.SubscriptionID).SetExpiresAt(expiry).Exec(ctx))
		q = v2Quote(t, s, u.ID, p[2], "stack", 1, 0)
		o, err = v2Create(t, s, u, q)
		require.NoError(t, err)
		require.NoError(t, c.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusPaid).SetPaidAt(time.Now()).Exec(ctx))
		locker, err := c.Tx(ctx)
		require.NoError(t, err)
		defer locker.Rollback()
		_, err = geilisub.LockParent(ctx, locker.Client(), before.SubscriptionID)
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { done <- s.ExecuteSubscriptionFulfillment(ctx, o.ID) }()
		waitPaymentLock(t, db, "user_subscriptions")
		time.Sleep(time.Until(expiry.Add(20 * time.Millisecond)))
		require.NoError(t, locker.Commit())
		require.Error(t, <-done)
		got, err := c.PaymentOrder.Get(ctx, o.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusFailed, got.Status)
		require.Contains(t, *got.FailedReason, "SUBSCRIPTION_PAID_REVIEW_REQUIRED")
		current, err := geilisub.LoadContract(ctx, c, before.SubscriptionID)
		require.NoError(t, err)
		require.Equal(t, 1, current.Quantity)
	})
	t.Run("contract-expiring-during-create-plan-lock-cannot-charge", func(t *testing.T) {
		s, u, p := v2PaymentFixtureWithClient(t, c)
		q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
		o, err := v2Create(t, s, u, q)
		require.NoError(t, err)
		before := v2Fulfill(t, s, o)
		expiry := time.Now().Add(time.Second).Truncate(time.Microsecond)
		_, err = db.Exec(`UPDATE subscription_contracts SET expires_at=$2 WHERE subscription_id=$1`, before.SubscriptionID, expiry)
		require.NoError(t, err)
		require.NoError(t, c.UserSubscription.UpdateOneID(before.SubscriptionID).SetExpiresAt(expiry).Exec(ctx))
		q = v2Quote(t, s, u.ID, p[2], "stack", 1, 0)
		locker, err := c.Tx(ctx)
		require.NoError(t, err)
		defer locker.Rollback()
		_, err = locker.Client().SubscriptionPlan.Query().Unique(false).Where(subscriptionplan.IDEQ(p[2].ID), geilisub.LockRows).Only(ctx)
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { _, err := v2Create(t, s, u, q); done <- err }()
		waitPaymentLock(t, db, "subscription_plans")
		time.Sleep(time.Until(expiry.Add(20 * time.Millisecond)))
		require.NoError(t, locker.Commit())
		require.Equal(t, "SUBSCRIPTION_QUOTE_CHANGED", infraerrors.Reason(<-done))
	})

	t.Run("cancel-versus-paid-callback-keeps-paid-rights", func(t *testing.T) {
		s, u, p := v2PaymentFixtureWithClient(t, c)
		q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
		o, err := v2Create(t, s, u, q)
		require.NoError(t, err)
		start := make(chan struct{})
		done := make(chan error, 2)
		go func() {
			<-start
			_, err := db.ExecContext(ctx, `UPDATE payment_orders SET status='CANCELLED' WHERE id=$1 AND status='PENDING'`, o.ID)
			done <- err
		}()
		go func() {
			<-start
			done <- s.HandlePaymentNotification(ctx, &payment.PaymentNotification{OrderID: o.OutTradeNo, TradeNo: "concurrent-paid", Status: payment.NotificationStatusSuccess, Amount: o.PayAmount}, payment.TypeAlipay)
		}()
		close(start)
		require.NoError(t, <-done)
		require.NoError(t, <-done)
		got, err := c.PaymentOrder.Get(ctx, o.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusCompleted, got.Status)
		require.NotNil(t, got.PaidAt)
	})

	t.Run("duplicate-fulfillment-concurrently-grants-once", func(t *testing.T) {
		s, u, p := v2PaymentFixtureWithClient(t, c)
		q := v2Quote(t, s, u.ID, p[2], "purchase", 2, 0)
		o, err := v2Create(t, s, u, q)
		require.NoError(t, err)
		o, err = c.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusPaid).SetPaidAt(time.Now()).Save(ctx)
		require.NoError(t, err)
		var wg sync.WaitGroup
		errs := make(chan error, 6)
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); errs <- s.ensureSubscriptionV2Assigned(ctx, o) }()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		rows, err := c.QueryContext(ctx, `SELECT after_contract FROM subscription_contract_changes WHERE order_id=$1`, o.ID)
		require.NoError(t, err)
		defer rows.Close()
		require.True(t, rows.Next())
		var raw []byte
		require.NoError(t, rows.Scan(&raw))
		var contract geilisub.Contract
		require.NoError(t, json.Unmarshal(raw, &contract))
		require.False(t, rows.Next())
		lots, err := geilisub.ReadLots(ctx, c, contract.SubscriptionID)
		require.NoError(t, err)
		require.Len(t, lots, 2)
	})
}
