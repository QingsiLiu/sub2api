//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func journalRefundFixture(t *testing.T, c *dbent.Client) (*PaymentService, *RefundPlan) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()
	u, err := c.User.Create().SetEmail("refund-" + suffix + "@example.invalid").SetPasswordHash("synthetic").SetBalance(100).Save(ctx)
	require.NoError(t, err)
	inst, err := c.PaymentProviderInstance.Create().SetName("refund-" + suffix).SetProviderKey("stripe").SetConfig("{}").SetRefundEnabled(true).Save(ctx)
	require.NoError(t, err)
	o, err := c.PaymentOrder.Create().SetUserID(u.ID).SetUserEmail(u.Email).SetUserName("fixture").SetAmount(100).SetPayAmount(100).SetFeeRate(0).SetRechargeCode("r-" + suffix).SetOutTradeNo("o-" + suffix).SetPaymentType("stripe").SetProviderInstanceID(strconv.FormatInt(inst.ID, 10)).SetPaymentTradeNo("pi-" + suffix).SetOrderType(payment.OrderTypeBalance).SetStatus(OrderStatusCompleted).SetExpiresAt(time.Now().Add(time.Hour)).SetPaidAt(time.Now()).SetClientIP("127.0.0.1").SetSrcHost("localhost").Save(ctx)
	require.NoError(t, err)
	return &PaymentService{entClient: c, loadBalancer: &captureLoadBalancer{}}, &RefundPlan{OrderID: o.ID, Order: o, RefundAmount: 40, GatewayAmount: 40, Reason: "synthetic fault test", DeductBalance: true, DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 40}
}
func journalBalance(t *testing.T, c *dbent.Client, userID int64) float64 {
	t.Helper()
	u, e := c.User.Get(context.Background(), userID)
	require.NoError(t, e)
	return u.Balance
}

func TestRefundJournalIntentAndDeductionReplay(t *testing.T) {
	c := newPaymentConfigServiceTestClient(t)
	s, p := journalRefundFixture(t, c)
	ctx := context.Background()
	j, created, err := s.claimPaymentRefundJournal(ctx, p)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, 60.0, journalBalance(t, c, p.Order.UserID))
	require.Equal(t, 40.0, j.DeductedBalance)
	for i := 0; i < 3; i++ {
		again, newly, err := s.claimPaymentRefundJournal(ctx, p)
		require.NoError(t, err)
		require.False(t, newly)
		require.Equal(t, j.RequestKey, again.RequestKey)
	}
	require.Equal(t, 60.0, journalBalance(t, c, p.Order.UserID))
	p.RefundAmount = 50
	_, _, err = s.claimPaymentRefundJournal(ctx, p)
	require.Error(t, err)
	require.Equal(t, 60.0, journalBalance(t, c, p.Order.UserID))
}
func TestRefundJournalUnknownRetainsDebitAndConfirmedFailureRestoresOnce(t *testing.T) {
	c := newPaymentConfigServiceTestClient(t)
	s, p := journalRefundFixture(t, c)
	ctx := context.Background()
	j, _, err := s.claimPaymentRefundJournal(ctx, p)
	require.NoError(t, err)
	r, err := s.finishJournalRefund(ctx, j, nil, errors.New("transport deadline; result unknown"))
	require.NoError(t, err)
	require.False(t, r.Success)
	require.Equal(t, 60.0, journalBalance(t, c, p.Order.UserID))
	o, e := c.PaymentOrder.Get(ctx, p.OrderID)
	require.NoError(t, e)
	require.Equal(t, OrderStatusRefundPending, o.Status)
	for i := 0; i < 3; i++ {
		_, err = s.finishJournalRefund(ctx, j, &payment.RefundResponse{Status: payment.ProviderStatusFailed, RefundID: "r1"}, nil)
		require.NoError(t, err)
	}
	require.Equal(t, 100.0, journalBalance(t, c, p.Order.UserID))
	u, e := c.User.Get(ctx, p.Order.UserID)
	require.NoError(t, e)
	require.Zero(t, u.TotalRecharged, "rollback is not a recharge")
	j, e = readPaymentRefundJournal(ctx, c, p.OrderID)
	require.NoError(t, e)
	require.Equal(t, "failed", j.State)
	n, e := c.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(p.OrderID, 10)), paymentauditlog.ActionEQ("REFUND_FAILURE_RESTORED")).Count(ctx)
	require.NoError(t, e)
	require.Equal(t, 1, n)
}
func TestRefundJournalSuccessAndPendingReplaysDoNotRedebit(t *testing.T) {
	c := newPaymentConfigServiceTestClient(t)
	s, p := journalRefundFixture(t, c)
	ctx := context.Background()
	j, _, err := s.claimPaymentRefundJournal(ctx, p)
	require.NoError(t, err)
	for _, status := range []string{payment.ProviderStatusSuccess, payment.ProviderStatusSuccess, payment.ProviderStatusPending, payment.ProviderStatusFailed} {
		r, e := s.finishJournalRefund(ctx, j, &payment.RefundResponse{Status: status, RefundID: "r1"}, nil)
		require.NoError(t, e)
		require.True(t, r.Success)
	}
	require.Equal(t, 60.0, journalBalance(t, c, p.Order.UserID))
	n, e := c.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(p.OrderID, 10)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).Count(ctx)
	require.NoError(t, e)
	require.Equal(t, 1, n)
}
func TestRefundJournalLegacyDayRevokeRollbackAndConcurrentChange(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(fmt.Sprint(change), func(t *testing.T) {
			c := newPaymentConfigServiceTestClient(t)
			s, p := journalRefundFixture(t, c)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Microsecond)
			parent, err := c.UserSubscription.Create().SetUserID(p.Order.UserID).SetStartsAt(now.Add(-time.Hour)).SetExpiresAt(now.AddDate(0, 0, 2)).SetStatus("active").Save(ctx)
			require.NoError(t, err)
			p.Order, err = c.PaymentOrder.UpdateOneID(p.Order.ID).SetOrderType(payment.OrderTypeSubscription).SetSubscriptionDays(7).Save(ctx)
			require.NoError(t, err)
			p.SubscriptionID = parent.ID
			p.DeductionType = payment.DeductionTypeSubscription
			p.BalanceToDeduct = 0
			p.SubDaysToDeduct = 7
			j, _, err := s.claimPaymentRefundJournal(ctx, p)
			require.NoError(t, err)
			require.NotNil(t, j.Payload.After.DeletedAt)
			if change {
				require.NoError(t, c.UserSubscription.UpdateOneID(parent.ID).SetStatus("suspended").Exec(mixins.SkipSoftDelete(ctx)))
			}
			_, err = s.finishJournalRefund(ctx, j, &payment.RefundResponse{Status: payment.ProviderStatusFailed}, nil)
			require.NoError(t, err)
			actual, err := c.UserSubscription.Get(mixins.SkipSoftDelete(ctx), parent.ID)
			require.NoError(t, err)
			state, err := readPaymentRefundJournal(ctx, c, p.OrderID)
			require.NoError(t, err)
			if change {
				require.Equal(t, "manual_review", state.State)
				require.Equal(t, "suspended", actual.Status)
				require.NotNil(t, actual.DeletedAt)
			} else {
				require.Equal(t, "failed", state.State)
				require.Nil(t, actual.DeletedAt)
				require.WithinDuration(t, parent.ExpiresAt, actual.ExpiresAt, time.Microsecond)
			}
		})
	}
}

type journalProvider struct {
	refundProviderTestDouble
	refunds   int
	queries   int
	refundReq payment.RefundRequest
	queryReq  payment.RefundQueryRequest
}

func (p *journalProvider) Refund(_ context.Context, r payment.RefundRequest) (*payment.RefundResponse, error) {
	p.refunds++
	p.refundReq = r
	return nil, errors.New("unknown transport outcome")
}
func (p *journalProvider) QueryRefund(_ context.Context, r payment.RefundQueryRequest) (*payment.RefundResponse, error) {
	p.queries++
	p.queryReq = r
	return &payment.RefundResponse{Status: payment.ProviderStatusSuccess, RefundID: "r-known"}, nil
}
func TestRefundJournalResumeUsesOriginalProviderIdentityWithoutResubmission(t *testing.T) {
	c := newPaymentConfigServiceTestClient(t)
	s, p := journalRefundFixture(t, c)
	ctx := context.Background()
	provider := &journalProvider{}
	defer replacePaymentProviderFactoryForTest(t, provider)()
	r, err := s.ExecuteRefund(ctx, p)
	require.NoError(t, err)
	require.False(t, r.Success)
	_, err = s.ExecuteRefund(ctx, p)
	require.NoError(t, err)
	require.Equal(t, 1, provider.refunds)
	// A fresh service, not the original in-memory plan, resolves the journal.
	resumed := &PaymentService{entClient: c, loadBalancer: &captureLoadBalancer{}}
	r, err = resumed.QueryAndFinalizeRefund(ctx, p.OrderID)
	require.NoError(t, err)
	require.True(t, r.Success)
	require.Equal(t, 1, provider.refunds)
	require.Equal(t, 1, provider.queries)
	require.NotEmpty(t, provider.refundReq.IdempotencyKey)
	require.Equal(t, provider.refundReq.IdempotencyKey, provider.queryReq.IdempotencyKey)
	require.Equal(t, provider.refundReq.Amount, provider.queryReq.Amount)
	require.Equal(t, 60.0, journalBalance(t, c, p.Order.UserID))
}

func TestRefundJournalRollbackHelperCannotGuessUnknownOutcome(t *testing.T) {
	c := newPaymentConfigServiceTestClient(t)
	s, p := journalRefundFixture(t, c)
	ctx := context.Background()
	j, _, err := s.claimPaymentRefundJournal(ctx, p)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		require.False(t, s.RollbackRefund(ctx, p, errors.New("timeout")))
	}
	require.Equal(t, 60.0, journalBalance(t, c, p.Order.UserID))
	_, err = s.finishRefund(ctx, p, &payment.RefundResponse{Status: payment.ProviderStatusFailed, RefundID: "r1"})
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		require.True(t, s.RollbackRefund(ctx, p, nil))
	}
	require.Equal(t, 100.0, journalBalance(t, c, p.Order.UserID))
	stored, err := readPaymentRefundJournal(ctx, c, p.OrderID)
	require.NoError(t, err)
	require.Equal(t, j.RequestKey, stored.RequestKey)
}

func TestRefundJournalOperationKeySeparatesEnvironmentsWithSameLocalID(t *testing.T) {
	a := &dbent.PaymentOrder{ID: 7, OutTradeNo: "stage-7"}
	b := &dbent.PaymentOrder{ID: 7, OutTradeNo: "production-7"}
	require.NotEqual(t, paymentRefundOperationKey(a, 1), paymentRefundOperationKey(b, 1))
	require.Equal(t, paymentRefundOperationKey(a, 1), paymentRefundOperationKey(a, 1))
	require.LessOrEqual(t, len(paymentRefundOperationKey(a, 1)), 64)
}

func TestRefundJournalHistoricalUnknownDebitCannotBeRetried(t *testing.T) {
	c := newPaymentConfigServiceTestClient(t)
	s, p := journalRefundFixture(t, c)
	ctx := context.Background()
	for _, status := range []string{OrderStatusRefunding, OrderStatusRefundPending, OrderStatusRefundFailed} {
		require.NoError(t, c.PaymentOrder.UpdateOneID(p.OrderID).SetStatus(status).Exec(ctx))
		_, _, err := s.claimPaymentRefundJournal(ctx, p)
		require.Error(t, err)
		require.Equal(t, 100.0, journalBalance(t, c, p.Order.UserID))
	}
}

func TestRefundJournalLegacyDayNeverRevokesActiveCampaignGift(t *testing.T) {
	c := newPaymentConfigServiceTestClient(t)
	s, p := journalRefundFixture(t, c)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	parent, err := c.UserSubscription.Create().SetUserID(p.Order.UserID).SetStartsAt(now.Add(-time.Hour)).SetExpiresAt(now.AddDate(0, 0, 5)).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	gift, err := c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(parent.ID).SetSourceType("campaign").SetStartsAt(now.Add(-time.Hour)).SetExpiresAt(parent.ExpiresAt).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	p.Order, err = c.PaymentOrder.UpdateOneID(p.Order.ID).SetOrderType(payment.OrderTypeSubscription).SetSubscriptionDays(7).Save(ctx)
	require.NoError(t, err)
	p.SubscriptionID = parent.ID
	p.DeductionType = payment.DeductionTypeSubscription
	p.BalanceToDeduct = 0
	p.SubDaysToDeduct = 7
	_, _, err = s.claimPaymentRefundJournal(ctx, p)
	require.ErrorIs(t, err, errLotRefundManual)
	got, err := c.UserSubscription.Get(ctx, parent.ID)
	require.NoError(t, err)
	require.Nil(t, got.DeletedAt)
	unchanged, err := c.UserSubscriptionEntitlement.Get(ctx, gift.ID)
	require.NoError(t, err)
	require.True(t, gift.ExpiresAt.Equal(unchanged.ExpiresAt))
	j, err := readPaymentRefundJournal(ctx, c, p.OrderID)
	require.NoError(t, err)
	require.Nil(t, j)
}

func TestRefundJournalRejectsChangedRetryPolicy(t *testing.T) {
	c := newPaymentConfigServiceTestClient(t)
	s, p := journalRefundFixture(t, c)
	ctx := context.Background()
	_, _, err := s.claimPaymentRefundJournal(ctx, p)
	require.NoError(t, err)
	for _, change := range []func(*RefundPlan){func(v *RefundPlan) { v.DeductBalance = false }, func(v *RefundPlan) { v.Force = true }, func(v *RefundPlan) { v.DeductionType = payment.DeductionTypeNone }, func(v *RefundPlan) { v.SubscriptionID = 123 }} {
		changed := *p
		change(&changed)
		_, _, err = s.claimPaymentRefundJournal(ctx, &changed)
		require.Error(t, err)
	}
	require.Equal(t, 60.0, journalBalance(t, c, p.Order.UserID))
}
