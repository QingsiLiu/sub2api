//go:build unit && integration

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/stretchr/testify/require"
)

func TestLegacyManagementPostgres(t *testing.T) {
	c, _ := v2PaymentPostgres(t)
	ctx := context.Background()
	s, u, plans := v2PaymentFixtureWithClient(t, c)
	s, u, plans, parent, ids := legacyPaymentFixtureWithService(t, s, u, plans)
	req := SubscriptionQuoteRequest{UserID: u.ID, SubscriptionID: parent.ID, PlanID: plans[0].ID, Operation: "renew", EntitlementIDs: ids[:1], Periods: 1}
	q, err := s.QuoteSubscription(ctx, req)
	require.NoError(t, err)
	t.Run("one-pending-order", func(t *testing.T) {
		start := make(chan struct{})
		results := make(chan error, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; _, e := v2Create(t, s, u, q); results <- e }()
		}
		close(start)
		wg.Wait()
		close(results)
		success := 0
		for e := range results {
			if e == nil {
				success++
			} else {
				require.ErrorIs(t, e, errSubscriptionPending)
			}
		}
		require.Equal(t, 1, success)
	})
	orders, err := c.PaymentOrder.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, orders, 1)
	order := orders[0]
	require.NoError(t, c.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusPaid).SetPaidAt(time.Now()).Exec(ctx))
	before, err := geilisub.ReadLots(ctx, c, parent.ID)
	require.NoError(t, err)
	t.Run("duplicate-callback-grants-once", func(t *testing.T) {
		start := make(chan struct{})
		results := make(chan error, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; results <- s.ensureSubscriptionV2Assigned(ctx, order) }()
		}
		close(start)
		wg.Wait()
		close(results)
		for e := range results {
			require.NoError(t, e)
		}
		after, e := geilisub.ReadLots(ctx, c, parent.ID)
		require.NoError(t, e)
		require.Len(t, after, 2)
		for _, l := range after {
			if l.ID == ids[0] {
				require.True(t, l.ExpiresAt.Equal(before[0].ExpiresAt.Add(7*24*time.Hour)))
			} else {
				require.True(t, l.ExpiresAt.Equal(before[1].ExpiresAt))
			}
		}
	})
	require.NoError(t, c.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusCompleted).Exec(ctx))
	t.Run("alignment-compare-and-swap", func(t *testing.T) {
		svc := &SubscriptionService{entClient: c}
		request := LegacyAlignmentRequest{UserID: u.ID, EntitlementIDs: ids, IdempotencyKey: "pg-once-alignment", Reason: "Synthetic support adjustment"}
		preview, e := svc.AlignLegacyEntitlements(ctx, parent.ID, 1, request)
		require.NoError(t, e)
		request.ExpectedSnapshot = preview.Snapshot
		request.Apply = true
		start := make(chan struct{})
		results := make(chan error, 4)
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, e := svc.AlignLegacyEntitlements(ctx, parent.ID, 1, request)
				results <- e
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		for e := range results {
			require.NoError(t, e)
		}
		lots, e := geilisub.ReadLots(ctx, c, parent.ID)
		require.NoError(t, e)
		for _, l := range lots {
			require.True(t, l.ExpiresAt.Equal(preview.ExpiresAt))
			require.Equal(t, 10.0, l.DailyUsageUSD)
		}
	})
}
