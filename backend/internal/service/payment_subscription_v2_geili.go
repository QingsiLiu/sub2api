package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

var errSubscriptionQuoteChanged = infraerrors.Conflict("SUBSCRIPTION_QUOTE_CHANGED", "subscription or sale price changed; review a new quote before paying")
var errSubscriptionQuoteInvalid = infraerrors.BadRequest("SUBSCRIPTION_QUOTE_INVALID", "a valid subscription quote is required")
var errSubscriptionPending = infraerrors.Conflict("SUBSCRIPTION_ORDER_PENDING", "another subscription order is awaiting payment or processing")

// The signed quote is user-bound and domain-separated from payment resume tokens.
// Only its immutable snapshot is persisted on the payment order.
type subscriptionV2Quote struct {
	Version             int             `json:"version"`
	TokenType           string          `json:"token_type"`
	UserID              int64           `json:"user_id"`
	IssuedAt            time.Time       `json:"issued_at"`
	ExpiresAt           time.Time       `json:"expires_at"`
	PlanRevision        string          `json:"plan_revision"`
	CurrentPlanRevision string          `json:"current_plan_revision,omitempty"`
	Change              geilisub.Change `json:"change"`
}

func paymentContractPlan(p *dbent.SubscriptionPlan) (geilisub.Plan, error) {
	if p == nil || math.IsNaN(p.Price) || math.IsInf(p.Price, 0) || p.Price <= 0 {
		return geilisub.Plan{}, infraerrors.BadRequest("SUBSCRIPTION_PLAN_UNSUPPORTED", "this plan requires administrator review")
	}
	plan := geilisub.PlanFromEntity(p)
	if plan.Kind == "" {
		return geilisub.Plan{}, infraerrors.BadRequest("SUBSCRIPTION_PLAN_UNSUPPORTED", "only the configured weekly and monthly tiers can be purchased")
	}
	return plan, nil
}

func (s *PaymentService) QuoteSubscription(ctx context.Context, req SubscriptionQuoteRequest) (*SubscriptionQuoteResponse, error) {
	if req.UserID <= 0 || req.PlanID <= 0 {
		return nil, errSubscriptionQuoteInvalid
	}
	signer := s.paymentResume()
	if err := signer.ensureSigningKey(); err != nil {
		return nil, err
	}
	if s.entClient == nil {
		return nil, infraerrors.ServiceUnavailable("SUBSCRIPTION_REPOSITORY_UNAVAILABLE", "subscription quotes require database transactions")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	c := tx.Client()
	if _, err = c.User.Query().Unique(false).Where(user.IDEQ(req.UserID), geilisub.LockRows).Only(ctx); err != nil {
		return nil, err
	}
	now := time.Now().Truncate(time.Microsecond)
	current, err := geilisub.CurrentContract(ctx, c, req.UserID, now)
	if err != nil {
		return nil, err
	}
	target, err := c.SubscriptionPlan.Get(ctx, req.PlanID)
	if err != nil {
		return nil, err
	}
	if !target.ForSale || target.ArchivedAt != nil {
		return nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan not found or not for sale")
	}
	tp, err := paymentContractPlan(target)
	if err != nil {
		return nil, err
	}
	cp := tp
	currentRevision := ""
	if current != nil && current.Mode == geilisub.ContractModeLegacy {
		return nil, geilisub.ErrContractCompatibility
	}
	if current != nil && current.ExpiresAt.After(now) {
		old, err := c.SubscriptionPlan.Get(ctx, current.PlanID)
		if err != nil {
			return nil, errSubscriptionQuoteChanged
		}
		cp, err = paymentContractPlan(old)
		if err != nil {
			return nil, err
		}
		currentRevision = old.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	operation := strings.TrimSpace(req.Operation)
	units, periods := req.Units, req.Periods
	if operation == "" {
		return nil, infraerrors.BadRequest("INVALID_SUBSCRIPTION_OPERATION", "choose purchase, stack, renew, or upgrade")
	}
	change, err := geilisub.PreviewContract(current, cp, tp, operation, units, periods, now)
	if err != nil {
		return nil, err
	}
	change.After.UserID = req.UserID
	q := &subscriptionV2Quote{Version: 2, TokenType: "subscription_quote_v2", UserID: req.UserID, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute), PlanRevision: target.UpdatedAt.UTC().Format(time.RFC3339Nano), CurrentPlanRevision: currentRevision, Change: change}
	token, err := signer.createSignedToken(q)
	if err != nil {
		return nil, err
	}
	var summary *SubscriptionQuotaSummary
	if current != nil && current.SubscriptionID > 0 {
		if err = geilisub.SyncDailyLedger(ctx, c, current.SubscriptionID, now); err != nil {
			return nil, err
		}
		used, err := geilisub.ReadDailyUsage(ctx, c, current.SubscriptionID, current.TermID, now)
		if err != nil {
			return nil, err
		}
		lots, err := geilisub.ReadLots(ctx, c, current.SubscriptionID)
		if err != nil {
			return nil, err
		}
		a := geilisub.ContractSummary(current, lots, used, now)
		summary = &a
	}
	projected := contractQuoteSummary(change.After, summary, now)
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	amount, _ := change.Amount.Float64()
	quantity := change.Units
	if operation == "renew" {
		quantity = change.Periods
	}
	return &SubscriptionQuoteResponse{QuoteID: token, ExpiresAt: q.ExpiresAt, BillableDays: change.BillableDays, Operation: operation, Units: change.Units, Periods: change.Periods, PlanRevision: q.PlanRevision, PlanID: target.ID, SubscriptionMode: operation, SubscriptionQuantity: quantity, OrderAmount: amount, ValidityDays: tp.PeriodDays, CanRenewLots: change.After.Quantity, Current: summary, Projected: &projected, CurrentContract: change.Before, ProjectedContract: &change.After}, nil
}

func contractQuoteSummary(c geilisub.Contract, current *SubscriptionQuotaSummary, now time.Time) SubscriptionQuotaSummary {
	daily := decimal.NewFromFloat(c.UnitDailyUSD).Mul(decimal.NewFromInt(int64(c.Quantity))).InexactFloat64()
	usage := 0.0
	if current != nil {
		usage = current.DailyUsageUSD
	}
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	date := now.In(loc)
	reset := time.Date(date.Year(), date.Month(), date.Day()+1, 0, 0, 0, 0, loc)
	remaining := math.Max(0, decimal.NewFromFloat(daily).Sub(decimal.NewFromFloat(usage)).InexactFloat64())
	return SubscriptionQuotaSummary{ActiveLotCount: c.Quantity, DailyLimitUSD: &daily, DailyUsageUSD: usage, RemainingUSD: &remaining, AvailableUSD: remaining, ExpiresAt: &c.ExpiresAt, NextExpiryAt: &c.ExpiresAt, DailyResetAt: &reset}
}

func (s *PaymentService) readSubscriptionV2Quote(token string, userID int64, now time.Time) (*subscriptionV2Quote, error) {
	if token == "" || len(token) > 32768 {
		return nil, errSubscriptionQuoteInvalid
	}
	signer := s.paymentResume()
	if err := signer.ensureSigningKey(); err != nil {
		return nil, err
	}
	var q subscriptionV2Quote
	if err := signer.parseSignedToken(token, &q); err != nil {
		return nil, errSubscriptionQuoteInvalid
	}
	if q.Version != 2 || q.TokenType != "subscription_quote_v2" || q.UserID != userID || q.Change.After.UserID != userID || q.Change.After.PlanID <= 0 || !q.Change.Amount.IsPositive() {
		return nil, errSubscriptionQuoteInvalid
	}
	if !now.Before(q.ExpiresAt) || q.IssuedAt.After(now.Add(time.Second)) || q.ExpiresAt.Sub(q.IssuedAt) > 5*time.Minute {
		return nil, infraerrors.Conflict("SUBSCRIPTION_QUOTE_EXPIRED", "quote expired; request a new quote")
	}
	return &q, nil
}

func (s *PaymentService) prepareSubscriptionV2Order(req CreateOrderRequest) (CreateOrderRequest, error) {
	q, err := s.readSubscriptionV2Quote(req.QuoteID, req.UserID, time.Now())
	if err != nil {
		return req, err
	}
	if req.PlanID != 0 && req.PlanID != q.Change.After.PlanID || req.Operation != "" && req.Operation != q.Change.Operation || req.Units != 0 && req.Units != q.Change.Units || req.Periods != 0 && req.Periods != q.Change.Periods {
		return req, errSubscriptionQuoteInvalid
	}
	req.PlanID = q.Change.After.PlanID
	req.Operation = q.Change.Operation
	req.Units = q.Change.Units
	req.Periods = q.Change.Periods
	req.SubscriptionMode = req.Operation
	req.SubscriptionQuantity = req.Units
	if req.Operation == "renew" {
		req.SubscriptionQuantity = req.Periods
	}
	req.subscriptionQuote = q
	return req, nil
}

func (s *PaymentService) validateSubscriptionV2OrderTx(ctx context.Context, c *dbent.Client, req CreateOrderRequest) error {
	if _, err := c.User.Query().Unique(false).Where(user.IDEQ(req.UserID), geilisub.LockRows).Only(ctx); err != nil {
		return err
	}
	q, err := s.readSubscriptionV2Quote(req.QuoteID, req.UserID, time.Now())
	if err != nil {
		return err
	}
	if err = checkSubscriptionPending(ctx, c, req.UserID); err != nil {
		return err
	}
	current, err := geilisub.CurrentContract(ctx, c, req.UserID, time.Now())
	if err != nil {
		return err
	}
	if !sameQuotedContract(q.Change.Before, current, time.Now()) {
		return errSubscriptionQuoteChanged
	}
	for id, revision := range map[int64]string{q.Change.After.PlanID: q.PlanRevision} {
		p, err := c.SubscriptionPlan.Get(ctx, id)
		if err != nil || p.ArchivedAt != nil || !p.ForSale || p.UpdatedAt.UTC().Format(time.RFC3339Nano) != revision {
			return errSubscriptionQuoteChanged
		}
	}
	if q.Change.Before != nil && q.CurrentPlanRevision != "" {
		p, err := c.SubscriptionPlan.Get(ctx, q.Change.Before.PlanID)
		if err != nil || p.UpdatedAt.UTC().Format(time.RFC3339Nano) != q.CurrentPlanRevision {
			return errSubscriptionQuoteChanged
		}
	}
	return nil
}
func sameQuotedContract(expected, actual *geilisub.Contract, now time.Time) bool {
	if expected == nil {
		return actual == nil || !actual.ExpiresAt.After(now)
	}
	return actual != nil && actual.Active(now) && actual.Status == expected.Status && actual.Quantity == expected.Quantity && actual.PlanID == expected.PlanID && actual.UnitDailyUSD == expected.UnitDailyUSD && actual.Mode == expected.Mode && actual.SubscriptionID == expected.SubscriptionID && actual.TermID == expected.TermID && actual.Revision == expected.Revision && actual.ExpiresAt.Equal(expected.ExpiresAt) && actual.ExpiresAt.After(now)
}
func checkSubscriptionPending(ctx context.Context, c *dbent.Client, userID int64) error {
	exists, err := c.PaymentOrder.Query().Where(paymentorder.UserIDEQ(userID), paymentorder.OrderTypeEQ(payment.OrderTypeSubscription), paymentorder.Or(paymentorder.And(paymentorder.StatusEQ(OrderStatusPending), paymentorder.ExpiresAtGT(time.Now())), paymentorder.StatusIn(OrderStatusPaid, OrderStatusRecharging, OrderStatusRefundRequested, OrderStatusRefunding, OrderStatusRefundPending), paymentorder.And(paymentorder.StatusEQ(OrderStatusFailed), paymentorder.PaidAtNotNil()))).Exist(ctx)
	if err != nil {
		return err
	}
	if exists {
		return errSubscriptionPending
	}
	return nil
}
func subscriptionV2Snapshot(q *subscriptionV2Quote) (map[string]any, error) {
	raw, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = json.Unmarshal(raw, &out)
	return out, err
}
func isSubscriptionV2Order(o *dbent.PaymentOrder) bool {
	if o == nil {
		return false
	}
	v, ok := o.SubscriptionSnapshot["version"]
	if !ok {
		return false
	}
	switch v := v.(type) {
	case int:
		return v == 2
	case float64:
		return v == 2
	case json.Number:
		return v == "2"
	}
	return false
}
func readSubscriptionV2Snapshot(o *dbent.PaymentOrder) (*subscriptionV2Quote, error) {
	if !isSubscriptionV2Order(o) {
		return nil, errSubscriptionQuoteInvalid
	}
	raw, err := json.Marshal(o.SubscriptionSnapshot)
	if err != nil {
		return nil, err
	}
	var q subscriptionV2Quote
	if err = json.Unmarshal(raw, &q); err != nil {
		return nil, err
	}
	if q.UserID != o.UserID || q.Version != 2 || q.Change.After.PlanID <= 0 {
		return nil, errSubscriptionQuoteInvalid
	}
	return &q, nil
}

func (s *PaymentService) applySubscriptionV2Payment(ctx context.Context, c *dbent.Client, o *dbent.PaymentOrder) (*geilisub.Contract, error) {
	q, err := readSubscriptionV2Snapshot(o)
	if err != nil {
		return nil, err
	}
	if _, err = c.User.Query().Unique(false).Where(user.IDEQ(o.UserID), geilisub.LockRows).Only(ctx); err != nil {
		return nil, err
	}
	change := q.Change
	if change.Before == nil {
		current, err := geilisub.CurrentContract(ctx, c, o.UserID, time.Now())
		if err != nil {
			return nil, err
		}
		if current != nil && current.ExpiresAt.After(time.Now()) {
			return nil, errSubscriptionQuoteChanged
		}
		parent, err := c.UserSubscription.Query().Where(usersubscription.UserIDEQ(o.UserID), usersubscription.PlanIDEQ(change.After.PlanID)).Order(dbent.Desc(usersubscription.FieldExpiresAt), dbent.Desc(usersubscription.FieldID)).First(ctx)
		if err != nil && !dbent.IsNotFound(err) {
			return nil, err
		}
		if parent == nil {
			now := time.Now()
			parent, err = c.UserSubscription.Create().SetUserID(o.UserID).SetPlanID(change.After.PlanID).SetStartsAt(now).SetExpiresAt(now).SetAssignedAt(now).SetStatus("expired").Save(ctx)
			if err != nil {
				return nil, err
			}
		}
		if !parent.ExpiresAt.Equal(parent.StartsAt) {
			if _, err := geilisub.LockParent(ctx, c, parent.ID); err != nil {
				return nil, err
			}
			if _, err := geilisub.EnsureContract(ctx, c, parent.ID, time.Now()); err != nil {
				return nil, err
			}
		}
		change.After.SubscriptionID = parent.ID
	}
	return geilisub.ApplyContractChange(ctx, c, change, o.ID, "payment", geilisub.PurchaseReference(o.ID), 0, time.Now())
}

func subscriptionV2Conflict(err error) bool {
	return errors.Is(err, geilisub.ErrStateConflict) || infraerrors.Reason(err) == "SUBSCRIPTION_QUOTE_CHANGED" || infraerrors.Reason(err) == "SUBSCRIPTION_STATE_CONFLICT" || infraerrors.Reason(err) == "SUBSCRIPTION_COMPATIBILITY_MODE"
}

func (s *PaymentService) ensureSubscriptionV2Assigned(ctx context.Context, o *dbent.PaymentOrder) error {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	tc := dbent.NewTxContext(ctx, tx)
	c := tx.Client()
	if _, err = c.User.Query().Unique(false).Where(user.IDEQ(o.UserID), geilisub.LockRows).Only(tc); err != nil {
		return err
	}
	assigned, err := hasPaymentSubscriptionAssignmentAudit(tc, c, o.ID)
	if err != nil {
		return err
	}
	var subscriptionID int64
	if assigned {
		log, err := c.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(geilisub.PurchaseReference(o.ID)), paymentauditlog.ActionEQ("SUBSCRIPTION_ASSIGNED")).First(tc)
		if err != nil {
			return err
		}
		var detail struct {
			SubscriptionID int64 `json:"subscription_id"`
		}
		if err = json.Unmarshal([]byte(log.Detail), &detail); err != nil {
			return err
		}
		subscriptionID = detail.SubscriptionID
	}
	if !assigned {
		currentOrder, err := c.PaymentOrder.Query().Unique(false).Where(paymentorder.IDEQ(o.ID), geilisub.LockRows).Only(tc)
		if err != nil {
			return err
		}
		if psIsRefundStatus(currentOrder.Status) {
			return infraerrors.Conflict("SUBSCRIPTION_STATE_CONFLICT", "refund is being processed")
		}
		contract, err := s.applySubscriptionV2Payment(tc, c, o)
		if err != nil {
			return err
		}
		subscriptionID = contract.SubscriptionID
		detail, err := json.Marshal(map[string]any{"subscription_id": subscriptionID, "term_id": contract.TermID, "revision": contract.Revision, "version": 2})
		if err != nil {
			return err
		}
		if err = c.PaymentAuditLog.Create().SetOrderID(geilisub.PurchaseReference(o.ID)).SetAction("SUBSCRIPTION_ASSIGNED").SetOperator("system").SetDetail(string(detail)).Exec(tc); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if subscriptionID > 0 {
		return s.subscriptionSvc.invalidateSubscriptionCaches(o.UserID, 0, subscriptionID)
	}
	return s.invalidatePaymentSubscriptionCache(ctx, o, 0)
}

// A persisted paid-review conflict is terminal for provider notifications. It
// remains visible/refundable to the customer without asking the provider to pay
// again or retry the same impossible entitlement change indefinitely.
func (s *PaymentService) fulfillPaymentWebhook(ctx context.Context, id int64) error {
	order, err := s.entClient.PaymentOrder.Get(ctx, id)
	if err != nil {
		return err
	}
	pendingReview := func(o *dbent.PaymentOrder) bool {
		return isSubscriptionV2Order(o) && o.Status == OrderStatusFailed && o.PaidAt != nil && strings.HasPrefix(psStringValue(o.FailedReason), "SUBSCRIPTION_PAID_REVIEW_REQUIRED:")
	}
	if pendingReview(order) {
		return nil
	}
	err = s.executeFulfillment(ctx, id)
	if err != nil && subscriptionV2Conflict(err) {
		if current, e := s.entClient.PaymentOrder.Get(ctx, id); e == nil && pendingReview(current) {
			return nil
		}
	}
	return err
}
