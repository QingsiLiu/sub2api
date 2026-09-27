package service

import (
	"context"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func (s *PaymentService) quoteCampaignStackTx(ctx context.Context, c *dbent.Client, req SubscriptionQuoteRequest, current *geilisub.Contract, gift *geilisub.CampaignStackSnapshot) (*SubscriptionQuoteResponse, error) {
	if req.Operation != "stack" {
		return nil, geilisub.ErrContractOperation
	}
	plans, err := lockSubscriptionQuotePlans(ctx, c, req.PlanID, nil)
	if err != nil {
		return nil, err
	}
	target := plans[req.PlanID]
	if target == nil || !target.ForSale || target.ArchivedAt != nil {
		return nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan not found or not for sale")
	}
	tp, err := paymentContractPlan(target)
	if err != nil {
		return nil, err
	}
	now := time.Now().Truncate(time.Microsecond)
	// Re-evaluate after waiting for plan locks; an expired gift never creates an order.
	lots, err := geilisub.ReadLots(ctx, c, current.SubscriptionID)
	if err != nil {
		return nil, err
	}
	if !geilisub.SameCampaignStack(gift, geilisub.CampaignStackCandidate(current, lots, now)) {
		return nil, errSubscriptionQuoteChanged
	}
	change, err := geilisub.PreviewCampaignStack(current, gift, tp, req.Units, req.Periods, now)
	if err != nil {
		return nil, err
	}
	expires := now.Add(5 * time.Minute)
	if gift.ExpiresAt.Before(expires) {
		expires = gift.ExpiresAt
	}
	q := &subscriptionV2Quote{Version: 4, TokenType: "subscription_quote_campaign_v4", UserID: req.UserID, IssuedAt: now, ExpiresAt: expires, PlanRevision: target.UpdatedAt.UTC().Format(time.RFC3339Nano), Change: change}
	token, err := s.paymentResume().createSignedToken(q)
	if err != nil {
		return nil, err
	}
	if len(token) > 32768 {
		return nil, errSubscriptionQuoteInvalid
	}
	if err = geilisub.SyncDailyLedger(ctx, c, current.SubscriptionID, now); err != nil {
		return nil, err
	}
	used, err := geilisub.ReadDailyUsage(ctx, c, current.SubscriptionID, current.TermID, now)
	if err != nil {
		return nil, err
	}
	before := geilisub.ContractSummary(current, lots, used, now)
	after := geilisub.ContractSummary(&change.After, lots, used, now)
	return &SubscriptionQuoteResponse{ManagementMode: "campaign_stack", CampaignStack: &gift.CampaignStackOffer, QuoteID: token, ExpiresAt: expires, BillableDays: change.BillableDays, Operation: "stack", Units: req.Units, PlanRevision: q.PlanRevision, PlanID: target.ID, SubscriptionMode: "stack", SubscriptionQuantity: req.Units, OrderAmount: change.Amount.InexactFloat64(), ValidityDays: 7, CanRenewLots: req.Units, Current: &before, Projected: &after, CurrentContract: change.Before, ProjectedContract: &change.After}, nil
}

func (s *PaymentService) validateCampaignStackOrderTx(ctx context.Context, c *dbent.Client, q *subscriptionV2Quote) error {
	current, gift, err := geilisub.CurrentCampaignStack(ctx, c, q.UserID, time.Now())
	if err != nil {
		return err
	}
	if !sameCampaignStackContract(q.Change.Before, current) || !geilisub.SameCampaignStack(q.Change.CampaignStack, gift) {
		return errSubscriptionQuoteChanged
	}
	plans, err := lockSubscriptionQuotePlans(ctx, c, q.Change.After.PlanID, nil)
	if err != nil {
		return err
	}
	target := plans[q.Change.After.PlanID]
	if target == nil || !target.ForSale || target.ArchivedAt != nil || target.UpdatedAt.UTC().Format(time.RFC3339Nano) != q.PlanRevision {
		return errSubscriptionQuoteChanged
	}
	if !time.Now().Before(q.ExpiresAt) {
		return infraerrors.Conflict("SUBSCRIPTION_QUOTE_EXPIRED", "quote expired; request a new quote")
	}
	// A shorter-lived gift can expire during the plan-lock wait even while the
	// shared alignment date is still in the future. Recheck every signed gift.
	lots, err := geilisub.ReadLots(ctx, c, current.SubscriptionID)
	if err != nil {
		return err
	}
	if !geilisub.SameCampaignStack(q.Change.CampaignStack, geilisub.CampaignStackCandidate(current, lots, time.Now())) {
		return errSubscriptionQuoteChanged
	}
	return nil
}

func sameCampaignStackContract(a, b *geilisub.Contract) bool {
	return a != nil && b != nil && a.SubscriptionID == b.SubscriptionID && a.UserID == b.UserID && a.TermID == b.TermID && a.Revision == b.Revision && a.Mode == b.Mode && a.Kind == b.Kind && a.PlanID == b.PlanID && a.UnitDailyUSD == b.UnitDailyUSD && a.Quantity == b.Quantity && a.PeriodDays == b.PeriodDays && a.StartsAt.Equal(b.StartsAt) && a.ExpiresAt.Equal(b.ExpiresAt) && a.Status == b.Status
}

func validCampaignStackQuote(q *subscriptionV2Quote) bool {
	c := q.Change
	return q.Version == 4 && q.TokenType == "subscription_quote_campaign_v4" && q.Legacy == nil && c.CampaignStack != nil && len(c.CampaignStack.Gifts) > 0 && c.Before != nil && c.Operation == "stack" && c.Units >= 1 && c.Units <= 10 && c.Periods == 0 && c.After.SubscriptionID == c.Before.SubscriptionID && c.After.UserID == c.Before.UserID && c.After.TermID == c.Before.TermID && c.After.Mode == geilisub.ContractModeV2 && c.After.Kind == geilisub.ContractKindWeek && c.After.PeriodDays == 7 && c.After.Quantity == c.Units && c.After.ExpiresAt.Equal(c.CampaignStack.ExpiresAt) && !q.ExpiresAt.After(c.CampaignStack.ExpiresAt)
}
