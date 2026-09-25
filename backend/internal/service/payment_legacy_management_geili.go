package service

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionplan"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SettingLegacySubscriptionManagement = "geili_legacy_subscription_management"

var errLegacyManagementDisabled = infraerrors.Conflict("LEGACY_MANAGEMENT_DISABLED", "旧订阅自助办理尚未开放，请联系客服 / Legacy self-service is disabled")

// The rollout gate only controls new quotations/orders, not paid fulfillment or
// refunds. Default is disabled; a JSON user-ID allowlist supports canary rollout.
func legacyManagementEnabled(ctx context.Context, c *dbent.Client, uid int64) (bool, error) {
	row, err := c.Setting.Query().Where(setting.KeyEQ(SettingLegacySubscriptionManagement)).Only(ctx)
	if dbent.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if row.Value == "true" {
		return true, nil
	}
	var ids []int64
	if json.Unmarshal([]byte(row.Value), &ids) != nil {
		return false, nil
	}
	for _, id := range ids {
		if id == uid {
			return true, nil
		}
	}
	return false, nil
}

type LegacyManagedLot struct {
	ID             int64     `json:"id"`
	ExpiresAt      time.Time `json:"expires_at"`
	DailyUSD       *float64  `json:"daily_limit_usd"`
	Status         string    `json:"status"`
	PlanID         int64     `json:"plan_id,omitempty"`
	Kind           string    `json:"kind,omitempty"`
	PeriodDays     int       `json:"period_days,omitempty"`
	UpgradePlanIDs []int64   `json:"upgrade_plan_ids"`
	Reason         string    `json:"reason,omitempty"`
}
type LegacyManagedPool struct {
	SubscriptionID int64              `json:"subscription_id"`
	Mode           string             `json:"management_mode"`
	Lots           []LegacyManagedLot `json:"lots"`
}
type LegacyManagementOptions struct {
	Enabled bool                `json:"enabled"`
	Pools   []LegacyManagedPool `json:"pools"`
}

// resolveLegacyPlans never derives a renewed period from current remaining
// duration. Source plan and original order period must agree. Historical custom
// or ambiguous sale tiers stay manual, independently of their siblings.
func resolveLegacyPlans(ctx context.Context, c *dbent.Client, lots []geilisub.Lot, plans []*dbent.SubscriptionPlan) (map[int64]geilisub.Plan, map[int64]string, error) {
	resolved := map[int64]geilisub.Plan{}
	reasons := map[int64]string{}
	for _, l := range lots {
		if l.SourceType == "campaign" {
			reasons[l.ID] = "gift"
			continue
		}
		if l.PlanID == nil || l.DailyLimitUSD == nil {
			reasons[l.ID] = "unrecognized"
			continue
		}
		source, err := c.SubscriptionPlan.Get(ctx, *l.PlanID)
		if dbent.IsNotFound(err) {
			reasons[l.ID] = "unrecognized"
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		p := geilisub.PlanFromEntity(source)
		if p.Kind == "" || p.DailyUSD != *l.DailyLimitUSD {
			reasons[l.ID] = "unrecognized"
			continue
		}
		if l.SourceOrderID != nil {
			order, e := c.PaymentOrder.Get(ctx, *l.SourceOrderID)
			if e != nil {
				return nil, nil, e
			}
			if order.SubscriptionDays != nil && *order.SubscriptionDays > 0 && *order.SubscriptionDays != p.PeriodDays {
				reasons[l.ID] = "unrecognized"
				continue
			}
		}
		var matches []geilisub.Plan
		for _, sale := range plans {
			sp, e := paymentContractPlan(sale)
			if e == nil && sale.ForSale && sale.ArchivedAt == nil && sp.Kind == p.Kind && sp.PeriodDays == p.PeriodDays && sp.DailyUSD == p.DailyUSD {
				matches = append(matches, sp)
			}
		}
		if len(matches) != 1 {
			reasons[l.ID] = "price_unavailable"
			continue
		}
		resolved[l.ID] = matches[0]
	}
	return resolved, reasons, nil
}

func (s *PaymentService) LegacySubscriptionOptions(ctx context.Context, uid int64) (*LegacyManagementOptions, error) {
	out := &LegacyManagementOptions{Pools: []LegacyManagedPool{}}
	enabled, err := legacyManagementEnabled(ctx, s.entClient, uid)
	if err != nil {
		return nil, err
	}
	out.Enabled = enabled
	if !enabled {
		return out, nil
	}
	rows, err := s.entClient.UserSubscription.Query().Where(usersubscription.UserIDEQ(uid), usersubscription.DeletedAtIsNil(), usersubscription.StatusIn("active", "suspended"), usersubscription.ExpiresAtGT(time.Now())).Order(usersubscription.ByID()).All(ctx)
	if err != nil {
		return nil, err
	}
	plans, err := s.entClient.SubscriptionPlan.Query().Where(subscriptionplan.ForSaleEQ(true), subscriptionplan.ArchivedAtIsNil()).All(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, row := range rows {
		contract, e := geilisub.LoadContract(ctx, s.entClient, row.ID)
		if e != nil {
			return nil, e
		}
		if contract == nil || contract.Mode != geilisub.ContractModeLegacy {
			continue
		}
		lots, e := geilisub.ReadLots(ctx, s.entClient, row.ID)
		if e != nil {
			return nil, e
		}
		recognized, reasons, e := resolveLegacyPlans(ctx, s.entClient, lots, plans)
		if e != nil {
			return nil, e
		}
		pool := LegacyManagedPool{SubscriptionID: row.ID, Mode: "legacy_lots", Lots: []LegacyManagedLot{}}
		for _, l := range lots {
			item := LegacyManagedLot{ID: l.ID, ExpiresAt: l.ExpiresAt, DailyUSD: l.DailyLimitUSD, Status: l.Status, UpgradePlanIDs: []int64{}, Reason: reasons[l.ID]}
			if !l.ExpiresAt.After(now) && l.Status == "active" {
				item.Status = "expired"
			}
			if row.Status != "active" || !l.Active(now) {
				item.Reason = "inactive"
			}
			if p, ok := recognized[l.ID]; ok && item.Reason == "" {
				item.PlanID = p.ID
				item.Kind = p.Kind
				item.PeriodDays = p.PeriodDays
				for _, sale := range plans {
					sp, e := paymentContractPlan(sale)
					if e == nil && sp.Kind == p.Kind && sp.PeriodDays == p.PeriodDays && sp.DailyUSD > p.DailyUSD && sp.Price.GreaterThan(p.Price) {
						item.UpgradePlanIDs = append(item.UpgradePlanIDs, sp.ID)
					}
				}
			}
			pool.Lots = append(pool.Lots, item)
		}
		out.Pools = append(out.Pools, pool)
	}
	return out, nil
}

func (s *PaymentService) quoteLegacySubscriptionTx(ctx context.Context, c *dbent.Client, req SubscriptionQuoteRequest) (*SubscriptionQuoteResponse, error) {
	enabled, err := legacyManagementEnabled(ctx, c, req.UserID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, errLegacyManagementDisabled
	}
	parent, err := geilisub.LockParent(ctx, c, req.SubscriptionID)
	if err != nil {
		return nil, err
	}
	if parent.UserID != req.UserID || parent.DeletedAt != nil || parent.Status != "active" {
		return nil, errSubscriptionQuoteInvalid
	}
	current, err := geilisub.LoadContract(ctx, c, parent.ID)
	if err != nil {
		return nil, err
	}
	if current == nil || current.Mode != geilisub.ContractModeLegacy {
		return nil, geilisub.ErrContractCompatibility
	}
	lots, err := geilisub.ReadLots(ctx, c, parent.ID)
	if err != nil {
		return nil, err
	}
	// Lock the catalog in ID order, covering source rows and potentially ambiguous
	// matching tiers; all accepted prices are snapshotted on the signed quote.
	rows, err := c.SubscriptionPlan.Query().Unique(false).Where(geilisub.LockRows).Order(subscriptionplan.ByID()).All(ctx)
	if err != nil {
		return nil, err
	}
	recognized, _, err := resolveLegacyPlans(ctx, c, lots, rows)
	if err != nil {
		return nil, err
	}
	var target *dbent.SubscriptionPlan
	revisions := map[string]string{}
	for _, p := range rows {
		revisions[strconv.FormatInt(p.ID, 10)] = p.UpdatedAt.UTC().Format(time.RFC3339Nano)
		if p.ID == req.PlanID {
			target = p
		}
	}
	if target == nil || !target.ForSale || target.ArchivedAt != nil {
		return nil, errSubscriptionQuoteInvalid
	}
	tp, err := paymentContractPlan(target)
	if err != nil {
		return nil, err
	}
	now := time.Now().Truncate(time.Microsecond)
	change, detail, err := geilisub.PreviewLegacy(current, lots, recognized, tp, req.Operation, req.EntitlementIDs, req.ExpiryAnchorEntitlementID, req.Units, req.Periods, now)
	if err != nil {
		return nil, err
	}
	q := &subscriptionV2Quote{Version: 3, TokenType: "subscription_quote_legacy_v3", UserID: req.UserID, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute), PlanRevision: target.UpdatedAt.UTC().Format(time.RFC3339Nano), Change: change, Legacy: detail, LegacyPlanRevisions: revisions}
	token, err := s.paymentResume().createSignedToken(q)
	if err != nil {
		return nil, err
	}
	if len(token) > 32768 {
		return nil, geilisub.ErrSelection
	}
	used, err := geilisub.ReadDailyUsage(ctx, c, parent.ID, current.TermID, now)
	if err != nil {
		return nil, err
	}
	summary := geilisub.ContractSummary(current, lots, used, now)
	projectedLots := geilisub.ProjectLegacyLots(lots, detail)
	projected := geilisub.ContractSummary(current, projectedLots, used, now)
	projectedContract := *current
	if projected.ExpiresAt != nil {
		projectedContract.ExpiresAt = *projected.ExpiresAt
	}
	amount, _ := change.Amount.Float64()
	quantity := req.Units
	if req.Operation == "renew" {
		quantity = req.Periods
	}
	return &SubscriptionQuoteResponse{QuoteID: token, ExpiresAt: q.ExpiresAt, Operation: req.Operation, Units: req.Units, Periods: req.Periods, PlanID: req.PlanID, SubscriptionMode: req.Operation, SubscriptionQuantity: quantity, OrderAmount: amount, ValidityDays: tp.PeriodDays, BillableDays: change.BillableDays, Current: &summary, Projected: &projected, CurrentContract: current, ProjectedContract: &projectedContract, ManagementMode: "legacy_lots", EntitlementChanges: detail.Lines}, nil
}

func (s *PaymentService) validateLegacyOrderTx(ctx context.Context, c *dbent.Client, q *subscriptionV2Quote) error {
	enabled, err := legacyManagementEnabled(ctx, c, q.UserID)
	if err != nil {
		return err
	}
	if !enabled {
		return errLegacyManagementDisabled
	}
	if q.Legacy == nil || q.Change.Before == nil {
		return errSubscriptionQuoteInvalid
	}
	parent, err := geilisub.LockParent(ctx, c, q.Change.Before.SubscriptionID)
	if err != nil {
		return err
	}
	if parent.UserID != q.UserID || parent.DeletedAt != nil {
		return errSubscriptionQuoteInvalid
	}
	current, err := geilisub.LoadContract(ctx, c, parent.ID)
	if err != nil {
		return err
	}
	lots, err := geilisub.ReadLots(ctx, c, parent.ID)
	if err != nil {
		return err
	}
	plans, err := c.SubscriptionPlan.Query().Unique(false).Where(geilisub.LockRows).Order(subscriptionplan.ByID()).All(ctx)
	if err != nil {
		return err
	}
	if len(plans) != len(q.LegacyPlanRevisions) {
		return errSubscriptionQuoteChanged
	}
	for _, p := range plans {
		if q.LegacyPlanRevisions[strconv.FormatInt(p.ID, 10)] != p.UpdatedAt.UTC().Format(time.RFC3339Nano) {
			return errSubscriptionQuoteChanged
		}
	}
	if err = geilisub.ValidateLegacyChange(current, lots, q.Change, q.Legacy, time.Now(), false); err != nil {
		return err
	}
	if !time.Now().Before(q.ExpiresAt) {
		return errSubscriptionQuoteChanged
	}
	return nil
}

// PaymentLegacyLines exposes only commercial lines, never the signed quote,
// user binding, internal catalog revisions or full ledger snapshot.
func PaymentLegacyLines(order *dbent.PaymentOrder) []geilisub.LegacyLine {
	if order == nil {
		return nil
	}
	q, err := readSubscriptionV2Snapshot(order)
	if err != nil || q.Version != 3 || q.Legacy == nil {
		return nil
	}
	if raw, ok := order.SubscriptionSnapshot["fulfilled_entitlement_changes"]; ok {
		data, err := json.Marshal(raw)
		var lines []geilisub.LegacyLine
		if err == nil && json.Unmarshal(data, &lines) == nil {
			return lines
		}
	}
	return q.Legacy.Lines
}
