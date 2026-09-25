package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionrequest"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/google/uuid"
)

// AdmitConsumption captures server-owned entitlement/window identity before forwarding.
// Unsettled requests deliberately block automatic refunds; age is not proof of no usage.
func (s *SubscriptionService) AdmitConsumption(ctx context.Context, sub *UserSubscription, keyID int64) (*UserSubscription, error) {
	if sub == nil {
		return nil, ErrSubscriptionNilInput
	}
	if s.entClient == nil {
		return sub, nil
	} // legacy test/service implementations
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	c := tx.Client()
	tc := dbent.NewTxContext(ctx, tx)
	parent, err := geilisub.LockParentUsage(tc, c, sub.ID)
	if err != nil {
		return nil, err
	}
	// Admission is serialized by the parent lock; waits may cross midnight or expiry.
	now := time.Now().Truncate(time.Microsecond)
	if parent.Status != "active" || parent.StartsAt.After(now) || !parent.ExpiresAt.After(now) {
		return nil, ErrSubscriptionInvalid
	}
	lots, err := geilisub.EnsureLegacyLot(tc, c, sub.ID)
	if err != nil {
		return nil, err
	}
	contract, err := geilisub.EnsureContract(tc, c, sub.ID, now)
	if err != nil {
		return nil, err
	}
	if err := geilisub.SyncDailyLedger(tc, c, sub.ID, now); err != nil {
		return nil, err
	}
	used, err := geilisub.ReadDailyUsage(tc, c, sub.ID, contract.TermID, now)
	if err != nil {
		return nil, err
	}
	var eligible, changed []geilisub.Lot
	for _, e := range lots {
		if e.Active(now) {
			before := e
			geilisub.NormalizeContractLot(&e, now, true)
			if geilisub.UsageChanged(before, e) {
				changed = append(changed, e)
			}
			eligible = append(eligible, e)
		}
	}
	a := geilisub.ContractSummary(contract, lots, used, now)
	if a.ActiveLotCount == 0 {
		return nil, ErrSubscriptionExpired
	}
	if a.AvailableUSD <= 0 {
		rejected := *sub
		rejected.Contract = contract
		rejected.Entitlements = lots
		rejected.DailyUsageUSD = used
		rejected.LedgerDailyUsageUSD = &used
		rejected.QuotaUsageDate = geilisub.DayStart(now)
		return nil, DailyQuotaExceeded(&rejected, now)
	}
	if err := geilisub.PersistLots(tc, c, changed); err != nil {
		return nil, err
	}

	for i := range eligible {
		rows, err := c.QueryContext(tc, `SELECT COALESCE(MAX(id),0) FROM subscription_usage_allocations WHERE entitlement_id=$1`, eligible[i].ID)
		if err != nil {
			return nil, err
		}
		if !rows.Next() {
			_ = rows.Close()
			return nil, fmt.Errorf("missing allocation watermark")
		}
		err = rows.Scan(&eligible[i].AllocationWatermark)
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(eligible)
	if err != nil {
		return nil, err
	}
	key := uuid.NewString()
	if err := c.SubscriptionRequest.Create().SetRequestKey(key).SetSubscriptionID(sub.ID).SetAPIKeyID(keyID).SetLots(raw).SetAdmittedAt(now).Exec(tc); err != nil {
		return nil, err
	}
	if err := geilisub.BindAdmission(tc, c, key, contract, now); err != nil {
		return nil, err
	}
	if err := geilisub.RefreshParentSnapshot(tc, c, parent, eligible, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	copy := *sub
	copy.AdmissionKey = key
	copy.Entitlements = lots
	copy.QuotaSummary = &a
	copy.Contract = contract
	copy.QuotaUsageDate = geilisub.DayStart(now)
	copy.DailyUsageUSD = a.DailyUsageUSD
	copy.LedgerDailyUsageUSD = &used
	copy.WeeklyUsageUSD = a.WeeklyUsageUSD
	copy.MonthlyUsageUSD = a.MonthlyUsageUSD
	return &copy, nil
}

// CancelUnsentConsumption is called only by the request lifecycle owner after it
// proves no transport dispatch or asynchronous handoff occurred. Never called by age.
func (s *SubscriptionService) CancelUnsentConsumption(ctx context.Context, sub *UserSubscription) error {
	if s.entClient == nil || sub == nil || sub.AdmissionKey == "" {
		return nil
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	tc := dbent.NewTxContext(ctx, tx)
	c := tx.Client()
	if _, err := geilisub.LockParentUsage(tc, c, sub.ID); err != nil {
		return err
	}
	_, err = c.SubscriptionRequest.Update().Where(subscriptionrequest.RequestKeyEQ(sub.AdmissionKey), subscriptionrequest.StatusEQ("admitted")).SetStatus("cancelled").SetSettledAt(time.Now()).Save(tc)
	if err != nil {
		return err
	}
	return tx.Commit()
}

type subscriptionAdmissionFactoryKey struct{}
type SubscriptionAdmissionFactory func(context.Context) (*UserSubscription, error)

func WithSubscriptionAdmissionFactory(ctx context.Context, f SubscriptionAdmissionFactory) context.Context {
	return context.WithValue(ctx, subscriptionAdmissionFactoryKey{}, f)
}

// WebSocket turns have independent billing identities within one authenticated connection.
func AdmitSubscriptionTurn(ctx context.Context, fallback *UserSubscription) (*UserSubscription, error) {
	if f, ok := ctx.Value(subscriptionAdmissionFactoryKey{}).(SubscriptionAdmissionFactory); ok {
		return f(ctx)
	}
	return fallback, nil
}

// CancelReplayedSubscriptionAdmission releases only the new, unsent admission
// created for an idempotent HTTP replay. The existing task owns a different
// admission and must never be cancelled by this request's lifecycle.
func CancelReplayedSubscriptionAdmission(ctx context.Context, sub *UserSubscription) error {
	if sub == nil || sub.AdmissionKey == "" {
		return nil
	}
	cancelAdmission, ok := ctx.Value(subscriptionCancelFactoryKey{}).(SubscriptionCancelFactory)
	if !ok {
		return nil
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return cancelAdmission(cleanup, sub)
}
