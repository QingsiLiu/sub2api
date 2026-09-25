package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/ent/usersubscriptionentitlement"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// This journal is independent of usage delivery and contains no credentials.
// A provider timeout is an unknown financial outcome, never proof of failure.
type paymentRefundJournal struct {
	OrderID          int64
	RequestKey       string
	UserID           int64
	RefundAmount     float64
	GatewayAmount    float64
	DeductedBalance  float64
	State            string
	ProviderRefundID string
	Payload          paymentRefundJournalPayload
}
type paymentRefundJournalPayload struct {
	ProviderInstanceID string                       `json:"provider_instance_id"`
	ProviderKey        string                       `json:"provider_key"`
	ProviderOrderID    string                       `json:"provider_order_id"`
	ProviderTradeNo    string                       `json:"provider_trade_no"`
	Currency           string                       `json:"currency"`
	Reason             string                       `json:"reason"`
	Force              bool                         `json:"force"`
	DeductBalance      bool                         `json:"deduct_balance"`
	DeductionType      string                       `json:"deduction_type"`
	SubscriptionDays   int                          `json:"subscription_days"`
	Before             *paymentRefundParentSnapshot `json:"parent_before,omitempty"`
	After              *paymentRefundParentSnapshot `json:"parent_after,omitempty"`
}
type paymentRefundParentSnapshot struct {
	ID        int64                      `json:"id"`
	UserID    int64                      `json:"user_id"`
	GroupID   *int64                     `json:"group_id"`
	PlanID    *int64                     `json:"plan_id"`
	StartsAt  time.Time                  `json:"starts_at"`
	ExpiresAt time.Time                  `json:"expires_at"`
	Status    string                     `json:"status"`
	DeletedAt *time.Time                 `json:"deleted_at"`
	Contract  *geilisub.Contract         `json:"contract,omitempty"`
	Lots      []paymentRefundLotSnapshot `json:"lots"`
}
type paymentRefundLotSnapshot struct {
	ID              int64     `json:"id"`
	ParentID        int64     `json:"parent_id"`
	ExpiresAt       time.Time `json:"expires_at"`
	Status          string    `json:"status"`
	PlanID          *int64    `json:"plan_id"`
	SourceOrderID   *int64    `json:"source_order_id"`
	SourceType      string    `json:"source_type"`
	SourceReference string    `json:"source_reference"`
	StartsAt        time.Time `json:"starts_at"`
	Daily           *float64  `json:"daily"`
	Weekly          *float64  `json:"weekly"`
	Monthly         *float64  `json:"monthly"`
}

func readPaymentRefundJournal(ctx context.Context, c *dbent.Client, orderID int64) (*paymentRefundJournal, error) {
	rows, err := c.QueryContext(ctx, `SELECT order_id,request_key,user_id,refund_amount,gateway_amount,deducted_balance,payload,state,provider_refund_id FROM payment_refund_journals WHERE order_id=$1`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	j := &paymentRefundJournal{}
	var raw []byte
	if err := rows.Scan(&j.OrderID, &j.RequestKey, &j.UserID, &j.RefundAmount, &j.GatewayAmount, &j.DeductedBalance, &raw, &j.State, &j.ProviderRefundID); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &j.Payload); err != nil {
		return nil, err
	}
	return j, rows.Err()
}

// Include the globally unique merchant order identifier: Stage and production
// may share a merchant account and the same local numeric order ID.
func paymentRefundOperationKey(o *dbent.PaymentOrder, amount float64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%s", o.ID, o.OutTradeNo, PaymentOrderCurrency(o), formatGatewayRefundAmount(amount, o))))
	return fmt.Sprintf("geili-rf-%x", sum[:16])
}

func refundJournalIdentityMatches(j *paymentRefundJournal, o *dbent.PaymentOrder) bool {
	return j.OrderID == o.ID && j.UserID == o.UserID && j.Payload.ProviderInstanceID == psStringValue(o.ProviderInstanceID) && j.Payload.ProviderKey == psStringValue(o.ProviderKey) && j.Payload.ProviderOrderID == o.OutTradeNo && j.Payload.ProviderTradeNo == o.PaymentTradeNo && j.Payload.Currency == PaymentOrderCurrency(o)
}
func refundJournalResult(j *paymentRefundJournal) *RefundResult {
	switch j.State {
	case "succeeded":
		return &RefundResult{Success: true, BalanceDeducted: j.DeductedBalance, SubDaysDeducted: j.Payload.SubscriptionDays}
	case "failed":
		return &RefundResult{Warning: "provider confirmed refund failure; local deduction restored; further refund requires manual review"}
	case "manual_review":
		return &RefundResult{Warning: "refund requires manual review; preserved deduction evidence conflicts with current state"}
	default:
		return &RefundResult{Warning: "refund outcome pending confirmation; local deduction retained; query the existing refund, do not submit another"}
	}
}
func writeRefundJournalAudit(ctx context.Context, c *dbent.Client, j *paymentRefundJournal, action string) error {
	raw, err := json.Marshal(map[string]any{"requestKey": j.RequestKey, "refundAmount": j.RefundAmount, "gatewayAmount": j.GatewayAmount, "balanceDeducted": j.DeductedBalance, "subDaysDeducted": j.Payload.SubscriptionDays, "state": j.State, "providerRefundID": j.ProviderRefundID})
	if err != nil {
		return err
	}
	return c.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(j.OrderID, 10)).SetAction(action).SetOperator("admin").SetDetail(string(raw)).Exec(ctx)
}
func refundParentSnapshot(ctx context.Context, c *dbent.Client, p *dbent.UserSubscription) (*paymentRefundParentSnapshot, error) {
	contract, err := geilisub.LoadContract(ctx, c, p.ID)
	if err != nil {
		return nil, err
	}
	rows, err := c.UserSubscriptionEntitlement.Query().Unique(false).Where(usersubscriptionentitlement.UserSubscriptionIDEQ(p.ID), geilisub.LockRows).Order(usersubscriptionentitlement.ByID()).All(ctx)
	if err != nil {
		return nil, err
	}
	snap := &paymentRefundParentSnapshot{ID: p.ID, UserID: p.UserID, GroupID: p.GroupID, PlanID: p.PlanID, StartsAt: p.StartsAt, ExpiresAt: p.ExpiresAt, Status: p.Status, DeletedAt: p.DeletedAt, Contract: contract, Lots: []paymentRefundLotSnapshot{}}
	for _, lot := range rows {
		snap.Lots = append(snap.Lots, paymentRefundLotSnapshot{ID: lot.ID, ParentID: lot.UserSubscriptionID, ExpiresAt: lot.ExpiresAt, Status: lot.Status, PlanID: lot.PlanID, SourceOrderID: lot.SourceOrderID, SourceType: lot.SourceType, SourceReference: lot.SourceReference, StartsAt: lot.StartsAt, Daily: lot.DailyLimitUsd, Weekly: lot.WeeklyLimitUsd, Monthly: lot.MonthlyLimitUsd})
	}
	return snap, nil
}
func refundParentEqual(a, b *paymentRefundParentSnapshot) bool {
	// JSON normalizes time zones but preserves exact PostgreSQL microseconds.
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	var xv, yv any
	if json.Unmarshal(x, &xv) != nil || json.Unmarshal(y, &yv) != nil {
		return false
	}
	return reflect.DeepEqual(xv, yv)
}

// claimPaymentRefundJournal commits the exact local debit and immutable provider
// intent atomically. Returning created=false must never invoke Provider.Refund.
func (s *PaymentService) claimPaymentRefundJournal(ctx context.Context, p *RefundPlan) (*paymentRefundJournal, bool, error) {
	if p == nil || p.Order == nil || p.OrderID != p.Order.ID {
		return nil, false, errors.New("invalid refund plan")
	}
	if p.RefundAmount <= 0 || p.GatewayAmount <= 0 || math.IsNaN(p.RefundAmount) || math.IsInf(p.RefundAmount, 0) || math.IsNaN(p.GatewayAmount) || math.IsInf(p.GatewayAmount, 0) {
		return nil, false, errors.New("invalid refund amount")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	tc := dbent.NewTxContext(ctx, tx)
	c := tx.Client()
	u, err := c.User.Query().Unique(false).Where(user.IDEQ(p.Order.UserID), geilisub.LockRows).Only(tc)
	if err != nil {
		return nil, false, err
	}
	o, err := c.PaymentOrder.Query().Unique(false).Where(paymentorder.IDEQ(p.OrderID), geilisub.LockRows).Only(tc)
	if err != nil {
		return nil, false, err
	}
	if o.UserID != u.ID {
		return nil, false, infraerrors.Conflict("REFUND_IDENTITY_CONFLICT", "order owner changed")
	}
	existing, err := readPaymentRefundJournal(tc, c, o.ID)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		subscriptionIdentityMatches := existing.Payload.After == nil && p.SubscriptionID == 0 || existing.Payload.After != nil && existing.Payload.After.ID == p.SubscriptionID
		policyMatches := existing.Payload.DeductBalance == p.DeductBalance && existing.Payload.Force == p.Force && existing.Payload.DeductionType == p.DeductionType && existing.Payload.SubscriptionDays == min(p.SubDaysToDeduct, MaxValidityDays) && subscriptionIdentityMatches
		if !policyMatches || !refundJournalIdentityMatches(existing, o) || math.Abs(existing.RefundAmount-p.RefundAmount) > 1e-8 || math.Abs(existing.GatewayAmount-p.GatewayAmount) > 1e-8 {
			return nil, false, infraerrors.Conflict("REFUND_IDENTITY_CONFLICT", "refund intent is immutable")
		}
		return existing, false, nil
	}
	if o.Status != OrderStatusCompleted && o.Status != OrderStatusRefundRequested && o.Status != OrderStatusRefundFailed {
		return nil, false, infraerrors.Conflict("REFUND_MANUAL_REVIEW", "refund without durable evidence requires manual review")
	}
	if o.Status == OrderStatusRefundFailed {
		return nil, false, infraerrors.Conflict("REFUND_MANUAL_REVIEW", "historical failed refund requires deduction reconciliation")
	}
	if p.RefundAmount-o.Amount > paymentAmountToleranceForCurrency(PaymentOrderCurrency(o)) {
		return nil, false, infraerrors.BadRequest("REFUND_AMOUNT_EXCEEDED", "refund exceeds order")
	}
	expectedGateway := calculateGatewayRefundAmount(o.Amount, o.PayAmount, p.RefundAmount, PaymentOrderCurrency(o))
	if math.Abs(expectedGateway-p.GatewayAmount) > 1e-8 {
		return nil, false, infraerrors.Conflict("REFUND_IDENTITY_CONFLICT", "gateway amount changed")
	}
	j := &paymentRefundJournal{OrderID: o.ID, RequestKey: paymentRefundOperationKey(o, p.GatewayAmount), UserID: o.UserID, RefundAmount: p.RefundAmount, GatewayAmount: p.GatewayAmount, State: "pending_provider", Payload: paymentRefundJournalPayload{ProviderInstanceID: psStringValue(o.ProviderInstanceID), ProviderKey: psStringValue(o.ProviderKey), ProviderOrderID: o.OutTradeNo, ProviderTradeNo: o.PaymentTradeNo, Currency: PaymentOrderCurrency(o), Reason: p.Reason, Force: p.Force, DeductBalance: p.DeductBalance, DeductionType: p.DeductionType}}
	if p.DeductionType == payment.DeductionTypeBalance && p.BalanceToDeduct > 0 {
		j.DeductedBalance = QuantizeUsageBillingAmount(math.Min(p.BalanceToDeduct, math.Max(u.Balance, 0)))
		if err = c.User.UpdateOneID(u.ID).AddBalance(-j.DeductedBalance).Exec(tc); err != nil {
			return nil, false, err
		}
	}
	if p.DeductionType == payment.DeductionTypeSubscription && p.SubscriptionID > 0 && p.SubDaysToDeduct > 0 {
		parent, err := c.UserSubscription.Query().Unique(false).Where(usersubscription.IDEQ(p.SubscriptionID), geilisub.LockRows).Only(tc)
		if err != nil {
			return nil, false, err
		}
		if parent.UserID != o.UserID {
			return nil, false, infraerrors.Conflict("REFUND_IDENTITY_CONFLICT", "subscription owner changed")
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		if !parent.ExpiresAt.After(now) {
			return nil, false, infraerrors.BadRequest("CANNOT_SHORTEN_EXPIRED", "cannot shorten an expired subscription")
		}
		j.Payload.Before, err = refundParentSnapshot(tc, c, parent)
		if err != nil {
			return nil, false, err
		}
		lots, err := geilisub.ReadLots(tc, c, parent.ID)
		if err != nil {
			return nil, false, err
		}
		if err = geilisub.CheckMutable(lots); err != nil {
			return nil, false, err
		}
		// Current-term gift reversals are manual. A historical paid order is
		// never evidence authorizing removal/shortening of a later campaign.
		for _, lot := range lots {
			if lot.SourceType == "campaign" && lot.Active(now) {
				return nil, false, errLotRefundManual
			}
		}
		days := min(p.SubDaysToDeduct, MaxValidityDays)
		j.Payload.SubscriptionDays = days
		expiry := parent.ExpiresAt.AddDate(0, 0, -days)
		update := c.UserSubscription.UpdateOneID(parent.ID)
		if expiry.After(now) {
			// Match the existing legacy-day adjustment selection policy.
			var selected []geilisub.Lot
			contract := j.Payload.Before.Contract
			if len(lots) > 0 {
				if contract != nil && contract.Mode == geilisub.ContractModeV2 {
					for _, lot := range lots {
						if lot.SourceType != "campaign" && lot.Active(now) {
							selected = append(selected, lot)
						}
					}
					if len(selected) == 0 {
						return nil, false, geilisub.ErrStateConflict
					}
				} else {
					selected, err = geilisub.ValidateSelection(lots, nil, now)
					if err != nil {
						return nil, false, err
					}
				}
				for _, lot := range selected {
					change := c.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetExpiresAt(expiry)
					if lot.Status == "expired" {
						change = change.SetStatus("active")
					}
					if err = change.Exec(tc); err != nil {
						return nil, false, err
					}
				}
			}
			update = update.SetExpiresAt(expiry)
			if err = update.Exec(tc); err != nil {
				return nil, false, err
			}
			if err = geilisub.RefreshParent(tc, c, parent.ID, now); err != nil {
				return nil, false, err
			}
			if contract != nil {
				if _, err = c.ExecContext(tc, `UPDATE subscription_contracts SET expires_at=$2,revision=revision+1,updated_at=$3 WHERE subscription_id=$1`, parent.ID, expiry, now); err != nil {
					return nil, false, err
				}
				if _, err = c.ExecContext(tc, `UPDATE subscription_contract_terms SET expires_at=$2 WHERE term_id=$1`, contract.TermID, expiry); err != nil {
					return nil, false, err
				}
			}
		} else {
			if err = update.SetDeletedAt(now).Exec(tc); err != nil {
				return nil, false, err
			}
			if _, err = c.ExecContext(tc, `UPDATE subscription_contracts SET revision=revision+1,updated_at=$2 WHERE subscription_id=$1`, parent.ID, now); err != nil {
				return nil, false, err
			}
		}
		after, err := c.UserSubscription.Get(mixins.SkipSoftDelete(tc), parent.ID)
		if err != nil {
			return nil, false, err
		}
		j.Payload.After, err = refundParentSnapshot(tc, c, after)
		if err != nil {
			return nil, false, err
		}
	}
	raw, err := json.Marshal(j.Payload)
	if err != nil {
		return nil, false, err
	}
	_, err = c.ExecContext(tc, `INSERT INTO payment_refund_journals(order_id,request_key,user_id,refund_amount,gateway_amount,deducted_balance,payload,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, j.OrderID, j.RequestKey, j.UserID, j.RefundAmount, j.GatewayAmount, j.DeductedBalance, string(raw), j.State)
	if err != nil {
		return nil, false, err
	}
	if err = c.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusRefunding).SetRefundAmount(j.RefundAmount).SetRefundReason(j.Payload.Reason).SetForceRefund(j.Payload.Force).Exec(tc); err != nil {
		return nil, false, err
	}
	if err = writeRefundJournalAudit(tc, c, j, "REFUND_INTENT_COMMITTED"); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	p.Order = o
	p.BalanceToDeduct = j.DeductedBalance
	p.SubDaysToDeduct = j.Payload.SubscriptionDays
	s.invalidateJournalRefund(ctx, j)
	return j, true, nil
}
func (s *PaymentService) executeJournalRefund(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	j, created, err := s.claimPaymentRefundJournal(ctx, p)
	if err != nil {
		return nil, err
	}
	if !created {
		return refundJournalResult(j), nil
	}
	if j.Payload.ProviderTradeNo == "" {
		return s.finishJournalRefund(ctx, j, &payment.RefundResponse{Status: payment.ProviderStatusSuccess}, nil)
	}
	prov, err := s.getRefundProvider(ctx, p.Order)
	if err != nil {
		return s.finishJournalRefund(ctx, j, nil, err)
	}
	if err = validateProviderSnapshotMetadata(p.Order, prov.ProviderKey(), providerMerchantIdentityMetadata(prov)); err != nil {
		return s.finishJournalRefund(ctx, j, nil, err)
	}
	resp, providerErr := prov.Refund(ctx, payment.RefundRequest{TradeNo: j.Payload.ProviderTradeNo, OrderID: j.Payload.ProviderOrderID, Amount: formatGatewayRefundAmount(j.GatewayAmount, p.Order), Reason: j.Payload.Reason, IdempotencyKey: j.RequestKey})
	// Even if the HTTP request context expired after provider acceptance, save
	// the observed result with a separate bounded context. On DB failure the
	// committed intent remains queryable after process restart.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	return s.finishJournalRefund(finishCtx, j, resp, providerErr)
}
func (s *PaymentService) queryJournalRefund(ctx context.Context, j *paymentRefundJournal, o *dbent.PaymentOrder) (*RefundResult, error) {
	if !refundJournalIdentityMatches(j, o) {
		return nil, infraerrors.Conflict("REFUND_IDENTITY_CONFLICT", "provider binding changed")
	}
	if j.State != "pending_provider" {
		return refundJournalResult(j), nil
	}
	prov, err := s.getRefundProvider(ctx, o)
	if err != nil {
		return nil, err
	}
	if err = validateProviderSnapshotMetadata(o, prov.ProviderKey(), providerMerchantIdentityMetadata(prov)); err != nil {
		return nil, err
	}
	query, ok := prov.(payment.RefundQueryProvider)
	if !ok {
		return nil, infraerrors.BadRequest("REFUND_QUERY_UNSUPPORTED", "provider cannot query the existing refund; verify manually without resubmitting")
	}
	resp, err := query.QueryRefund(ctx, payment.RefundQueryRequest{TradeNo: j.Payload.ProviderTradeNo, OrderID: j.Payload.ProviderOrderID, RefundID: j.ProviderRefundID, Amount: formatGatewayRefundAmount(j.GatewayAmount, o), IdempotencyKey: j.RequestKey})
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	return s.finishJournalRefund(finishCtx, j, resp, err)
}

func (s *PaymentService) finishJournalRefund(ctx context.Context, known *paymentRefundJournal, resp *payment.RefundResponse, providerErr error) (*RefundResult, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	tc := dbent.NewTxContext(ctx, tx)
	c := tx.Client()
	if _, err = c.User.Query().Unique(false).Where(user.IDEQ(known.UserID), geilisub.LockRows).Only(tc); err != nil {
		return nil, err
	}
	o, err := c.PaymentOrder.Query().Unique(false).Where(paymentorder.IDEQ(known.OrderID), geilisub.LockRows).Only(tc)
	if err != nil {
		return nil, err
	}
	j, err := readPaymentRefundJournal(tc, c, known.OrderID)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, errors.New("refund journal missing")
	}
	if !refundJournalIdentityMatches(j, o) {
		return nil, infraerrors.Conflict("REFUND_IDENTITY_CONFLICT", "refund identity changed")
	}
	if j.State != "pending_provider" {
		return refundJournalResult(j), nil
	}
	status := ""
	if providerErr == nil && resp != nil {
		status = strings.TrimSpace(resp.Status)
	}
	if resp != nil && strings.TrimSpace(resp.RefundID) != "" {
		if j.ProviderRefundID != "" && j.ProviderRefundID != strings.TrimSpace(resp.RefundID) {
			return nil, infraerrors.Conflict("REFUND_IDENTITY_CONFLICT", "provider refund identifier changed")
		}
		j.ProviderRefundID = strings.TrimSpace(resp.RefundID)
	}
	action := "REFUND_PENDING"
	lastError := ""
	if providerErr != nil {
		lastError = "provider result unavailable; query the original refund"
	}
	switch status {
	case payment.ProviderStatusSuccess, payment.ProviderStatusRefunded:
		j.State = "succeeded"
		action = "REFUND_SUCCESS"
		state := OrderStatusRefunded
		if j.RefundAmount < o.Amount {
			state = OrderStatusPartiallyRefunded
		}
		if err = c.PaymentOrder.UpdateOneID(o.ID).SetStatus(state).SetRefundAt(time.Now().UTC().Truncate(time.Microsecond)).ClearFailedAt().ClearFailedReason().Exec(tc); err != nil {
			return nil, err
		}
	case payment.ProviderStatusFailed:
		if j.Payload.After != nil {
			parent, lockErr := c.UserSubscription.Query().Unique(false).Where(usersubscription.IDEQ(j.Payload.After.ID), geilisub.LockRows).Only(mixins.SkipSoftDelete(tc))
			if lockErr != nil {
				return nil, lockErr
			}
			current, err := refundParentSnapshot(tc, c, parent)
			if err != nil {
				return nil, err
			}
			conflict := !refundParentEqual(current, j.Payload.After)
			if conflict {
				j.State = "manual_review"
				action = "REFUND_RESTORE_CONFLICT"
				lastError = "provider confirmed failure but subscription changed; manual restoration required"
				break
			}
			before := j.Payload.Before
			update := c.UserSubscription.UpdateOneID(parent.ID).SetExpiresAt(before.ExpiresAt).SetStatus(before.Status)
			if before.DeletedAt == nil {
				update = update.ClearDeletedAt()
			} else {
				update = update.SetDeletedAt(*before.DeletedAt)
			}
			if err = update.Exec(mixins.SkipSoftDelete(tc)); err != nil {
				return nil, err
			}
			for _, lot := range before.Lots {
				if err = c.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetExpiresAt(lot.ExpiresAt).SetStatus(lot.Status).Exec(tc); err != nil {
					return nil, err
				}
			}
			if before.Contract != nil {
				contract := before.Contract
				if _, err = c.ExecContext(tc, `UPDATE subscription_contracts SET expires_at=$2,revision=revision+1,updated_at=$3 WHERE subscription_id=$1`, parent.ID, contract.ExpiresAt, time.Now().UTC()); err != nil {
					return nil, err
				}
				if _, err = c.ExecContext(tc, `UPDATE subscription_contract_terms SET expires_at=$2 WHERE term_id=$1`, contract.TermID, contract.ExpiresAt); err != nil {
					return nil, err
				}
			}
		}
		if j.DeductedBalance > 0 {
			if err = c.User.UpdateOneID(j.UserID).AddBalance(j.DeductedBalance).Exec(tc); err != nil {
				return nil, err
			}
		}
		j.State = "failed"
		action = "REFUND_FAILURE_RESTORED"
		if err = c.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusRefundFailed).SetFailedAt(time.Now()).SetFailedReason("payment provider confirmed refund failure; local deduction restored").Exec(tc); err != nil {
			return nil, err
		}
	}
	if j.State == "pending_provider" || j.State == "manual_review" {
		if err = c.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusRefundPending).Exec(tc); err != nil {
			return nil, err
		}
	}
	_, err = c.ExecContext(tc, `UPDATE payment_refund_journals SET state=$2,provider_refund_id=$3,last_error=$4,updated_at=CURRENT_TIMESTAMP WHERE order_id=$1`, j.OrderID, j.State, j.ProviderRefundID, lastError)
	if err != nil {
		return nil, err
	}
	if err = writeRefundJournalAudit(tc, c, j, action); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.invalidateJournalRefund(ctx, j)
	return refundJournalResult(j), nil
}
func (s *PaymentService) invalidateJournalRefund(ctx context.Context, j *paymentRefundJournal) {
	cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if j.Payload.After != nil && s.subscriptionSvc != nil {
		gid := int64(0)
		if j.Payload.After.GroupID != nil {
			gid = *j.Payload.After.GroupID
		}
		_ = s.subscriptionSvc.invalidateSubscriptionCaches(j.UserID, gid, j.Payload.After.ID)
	}
	if s.redeemService != nil {
		if s.redeemService.authCacheInvalidator != nil {
			s.redeemService.authCacheInvalidator.InvalidateAuthCacheByUserID(cacheCtx, j.UserID)
		}
		if s.redeemService.billingCacheService != nil {
			_ = s.redeemService.billingCacheService.InvalidateUserBalance(cacheCtx, j.UserID)
		}
	}
}
