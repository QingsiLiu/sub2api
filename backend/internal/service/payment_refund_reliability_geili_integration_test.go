//go:build unit && integration

package service

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestRefundJournalPostgresCrashAndConcurrency(t *testing.T) {
	c, db := v2PaymentPostgres(t)
	ctx := context.Background()
	migration, err := migrations.FS.ReadFile("263_payment_refund_reliability_geili.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	t.Run("post-debit-pre-journal-insert-failure-rolls-everything-back", func(t *testing.T) {
		s, p := journalRefundFixture(t, c)
		_, err = db.Exec(`CREATE FUNCTION fail_refund_intent() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected intent failure'; END $$; CREATE TRIGGER fail_refund_intent BEFORE INSERT ON payment_refund_journals FOR EACH ROW EXECUTE FUNCTION fail_refund_intent()`)
		require.NoError(t, err)
		_, _, err = s.claimPaymentRefundJournal(ctx, p)
		require.Error(t, err)
		_, dropErr := db.Exec(`DROP TRIGGER fail_refund_intent ON payment_refund_journals; DROP FUNCTION fail_refund_intent()`)
		require.NoError(t, dropErr)
		require.Equal(t, 100.0, journalBalance(t, c, p.Order.UserID))
		j, e := readPaymentRefundJournal(ctx, c, p.OrderID)
		require.NoError(t, e)
		require.Nil(t, j)
		o, e := c.PaymentOrder.Get(ctx, p.OrderID)
		require.NoError(t, e)
		require.Equal(t, OrderStatusCompleted, o.Status)
	})
	t.Run("post-intent-crash-resumes-once-with-16-workers", func(t *testing.T) {
		s, p := journalRefundFixture(t, c)
		j, _, err := s.claimPaymentRefundJournal(ctx, p)
		require.NoError(t, err)
		fresh := &PaymentService{entClient: c}
		var wg sync.WaitGroup
		errors := make(chan error, 16)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, _, e := fresh.claimPaymentRefundJournal(ctx, p); errors <- e }()
		}
		wg.Wait()
		close(errors)
		for e := range errors {
			require.NoError(t, e)
		}
		require.Equal(t, 60.0, journalBalance(t, c, p.Order.UserID))
		errors = make(chan error, 16)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := fresh.finishJournalRefund(ctx, j, &payment.RefundResponse{Status: payment.ProviderStatusSuccess, RefundID: "confirmed-refund"}, nil)
				errors <- e
			}()
		}
		wg.Wait()
		close(errors)
		for e := range errors {
			require.NoError(t, e)
		}
		n, e := c.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(p.OrderID, 10)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).Count(ctx)
		require.NoError(t, e)
		require.Equal(t, 1, n)
		require.Equal(t, 60.0, journalBalance(t, c, p.Order.UserID))
	})
	t.Run("post-provider-success-audit-failure-retains-intent", func(t *testing.T) {
		s, p := journalRefundFixture(t, c)
		j, _, err := s.claimPaymentRefundJournal(ctx, p)
		require.NoError(t, err)
		_, err = db.Exec(`CREATE FUNCTION fail_refund_success() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='REFUND_SUCCESS' THEN RAISE EXCEPTION 'injected success evidence failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_refund_success BEFORE INSERT ON payment_audit_logs FOR EACH ROW EXECUTE FUNCTION fail_refund_success()`)
		require.NoError(t, err)
		_, err = s.finishJournalRefund(ctx, j, &payment.RefundResponse{Status: payment.ProviderStatusSuccess, RefundID: "r1"}, nil)
		require.Error(t, err)
		_, dropErr := db.Exec(`DROP TRIGGER fail_refund_success ON payment_audit_logs; DROP FUNCTION fail_refund_success()`)
		require.NoError(t, dropErr)
		stored, e := readPaymentRefundJournal(ctx, c, p.OrderID)
		require.NoError(t, e)
		require.Equal(t, "pending_provider", stored.State)
		require.Equal(t, 60.0, journalBalance(t, c, p.Order.UserID))
		r, e := s.finishJournalRefund(ctx, j, &payment.RefundResponse{Status: payment.ProviderStatusSuccess, RefundID: "r1"}, nil)
		require.NoError(t, e)
		require.True(t, r.Success)
	})
	t.Run("confirmed-failure-16-rollbacks-restore-exact-once", func(t *testing.T) {
		s, p := journalRefundFixture(t, c)
		j, _, err := s.claimPaymentRefundJournal(ctx, p)
		require.NoError(t, err)
		var wg sync.WaitGroup
		errors := make(chan error, 16)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := s.finishJournalRefund(ctx, j, &payment.RefundResponse{Status: payment.ProviderStatusFailed, RefundID: "failed-refund"}, nil)
				errors <- e
			}()
		}
		wg.Wait()
		close(errors)
		for e := range errors {
			require.NoError(t, e)
		}
		require.Equal(t, 100.0, journalBalance(t, c, p.Order.UserID))
		u, e := c.User.Get(ctx, p.Order.UserID)
		require.NoError(t, e)
		require.Zero(t, u.TotalRecharged)
	})
	t.Run("legacy-lot-and-contract-expiry-restored-without-rewriting-usage", func(t *testing.T) {
		s, p := journalRefundFixture(t, c)
		now := time.Now().UTC().Truncate(time.Microsecond)
		expiry := now.AddDate(0, 0, 30)
		parent, e := c.UserSubscription.Create().SetUserID(p.Order.UserID).SetStartsAt(now.Add(-time.Hour)).SetExpiresAt(expiry).SetStatus("active").Save(ctx)
		require.NoError(t, e)
		lot, e := c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(parent.ID).SetSourceType("legacy").SetStartsAt(parent.StartsAt).SetExpiresAt(expiry).SetStatus("active").SetDailyUsageUsd(9).SetLifetimeUsageUsd(20).Save(ctx)
		require.NoError(t, e)
		term := fmt.Sprintf("refund-legacy-%d", parent.ID)
		_, e = db.Exec(`INSERT INTO subscription_contract_terms(term_id,subscription_id,starts_at,expires_at) VALUES($1,$2,$3,$4)`, term, parent.ID, parent.StartsAt, expiry)
		require.NoError(t, e)
		_, e = db.Exec(`INSERT INTO subscription_contracts(subscription_id,user_id,term_id,mode,starts_at,expires_at) VALUES($1,$2,$3,'legacy_daily',$4,$5)`, parent.ID, p.Order.UserID, term, parent.StartsAt, expiry)
		require.NoError(t, e)
		p.Order, e = c.PaymentOrder.UpdateOneID(p.Order.ID).SetOrderType(payment.OrderTypeSubscription).SetSubscriptionDays(7).Save(ctx)
		require.NoError(t, e)
		p.SubscriptionID = parent.ID
		p.DeductionType = payment.DeductionTypeSubscription
		p.BalanceToDeduct = 0
		p.SubDaysToDeduct = 7
		j, _, e := s.claimPaymentRefundJournal(ctx, p)
		require.NoError(t, e)
		require.True(t, j.Payload.After.Lots[0].ExpiresAt.Equal(expiry.AddDate(0, 0, -7)))
		// New completed usage is legitimate; restore only changed rights, not counters.
		require.NoError(t, c.UserSubscriptionEntitlement.UpdateOneID(lot.ID).AddLifetimeUsageUsd(3).Exec(ctx))
		_, e = s.finishJournalRefund(ctx, j, &payment.RefundResponse{Status: payment.ProviderStatusFailed}, nil)
		require.NoError(t, e)
		restored, e := c.UserSubscriptionEntitlement.Get(ctx, lot.ID)
		require.NoError(t, e)
		require.True(t, expiry.Equal(restored.ExpiresAt))
		require.Equal(t, 23.0, restored.LifetimeUsageUsd)
		var contractExpiry time.Time
		var revision int64
		require.NoError(t, db.QueryRow(`SELECT expires_at,revision FROM subscription_contracts WHERE subscription_id=$1`, parent.ID).Scan(&contractExpiry, &revision))
		require.True(t, expiry.Equal(contractExpiry))
		require.Greater(t, revision, j.Payload.Before.Contract.Revision)
	})

	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy-day-snapshot-cas-%v", changed), func(t *testing.T) {
			s, p := journalRefundFixture(t, c)
			now := time.Now().UTC().Truncate(time.Microsecond)
			parent, e := c.UserSubscription.Create().SetUserID(p.Order.UserID).SetStartsAt(now.Add(-time.Hour)).SetExpiresAt(now.AddDate(0, 0, 2)).SetStatus("active").Save(ctx)
			require.NoError(t, e)
			p.Order, e = c.PaymentOrder.UpdateOneID(p.Order.ID).SetOrderType(payment.OrderTypeSubscription).SetSubscriptionDays(7).Save(ctx)
			require.NoError(t, e)
			p.SubscriptionID = parent.ID
			p.DeductionType = payment.DeductionTypeSubscription
			p.BalanceToDeduct = 0
			p.SubDaysToDeduct = 7
			j, _, e := s.claimPaymentRefundJournal(ctx, p)
			require.NoError(t, e)
			if changed {
				require.NoError(t, c.UserSubscription.UpdateOneID(parent.ID).SetStatus("suspended").Exec(mixins.SkipSoftDelete(ctx)))
			}
			_, e = s.finishJournalRefund(ctx, j, &payment.RefundResponse{Status: payment.ProviderStatusFailed}, nil)
			require.NoError(t, e)
			got, e := c.UserSubscription.Get(mixins.SkipSoftDelete(ctx), parent.ID)
			require.NoError(t, e)
			if changed {
				require.NotNil(t, got.DeletedAt)
				require.Equal(t, "suspended", got.Status)
			} else {
				require.Nil(t, got.DeletedAt)
				require.True(t, parent.ExpiresAt.Equal(got.ExpiresAt))
			}
		})
	}
}
