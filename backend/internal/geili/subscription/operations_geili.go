package subscription

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionentitlementorder"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/ent/usersubscriptionentitlement"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrStateConflict = infraerrors.Conflict("SUBSCRIPTION_STATE_CONFLICT", "subscription rights changed or are frozen; refresh and retry")
var ErrQuantity = infraerrors.BadRequest("INVALID_SUBSCRIPTION_QUANTITY", "subscription quantity must be between 1 and 10")
var ErrRenewQuantity = infraerrors.Conflict("SUBSCRIPTION_RENEW_QUANTITY", "renewal quantity exceeds available entitlements")
var ErrSelection = infraerrors.Conflict("SUBSCRIPTION_ENTITLEMENT_SELECTION_REQUIRED", "select the entitlements to adjust")
var MaxExpiry = time.Date(2099, 12, 31, 23, 59, 59, 0, time.UTC)

func PurchaseOptions(mode string, quantity int) (string, int, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "renew"
	}
	if mode != "renew" && mode != "stack" {
		return "", 0, infraerrors.BadRequest("INVALID_SUBSCRIPTION_MODE", "subscription mode must be renew or stack")
	}
	if quantity == 0 {
		quantity = 1
	}
	if quantity < 1 || quantity > 10 {
		return "", 0, ErrQuantity
	}
	return mode, quantity, nil
}
func FromEntity(e *dbent.UserSubscriptionEntitlement) Lot {
	return Lot{ID: e.ID, UserSubscriptionID: e.UserSubscriptionID, PlanID: e.PlanID, SourceOrderID: e.SourceOrderID, LotIndex: e.LotIndex, PurchaseMode: e.PurchaseMode, Status: e.Status, StartsAt: e.StartsAt, ExpiresAt: e.ExpiresAt, DailyLimitUSD: e.DailyLimitUsd, WeeklyLimitUSD: e.WeeklyLimitUsd, MonthlyLimitUSD: e.MonthlyLimitUsd, DailyWindowStart: e.DailyWindowStart, WeeklyWindowStart: e.WeeklyWindowStart, MonthlyWindowStart: e.MonthlyWindowStart, DailyUsageUSD: e.DailyUsageUsd, WeeklyUsageUSD: e.WeeklyUsageUsd, MonthlyUsageUSD: e.MonthlyUsageUsd, LifetimeUsageUSD: e.LifetimeUsageUsd, RefundedAt: e.RefundedAt, CreatedAt: e.CreatedAt, SourceType: e.SourceType, SourceReference: e.SourceReference}
}
func ReadLots(ctx context.Context, c *dbent.Client, id int64) ([]Lot, error) {
	rows, err := c.UserSubscriptionEntitlement.Query().Where(usersubscriptionentitlement.UserSubscriptionIDEQ(id)).Order(usersubscriptionentitlement.ByExpiresAt(), usersubscriptionentitlement.ByID()).All(ctx)
	if err != nil {
		return nil, err
	}
	lots := make([]Lot, 0, len(rows))
	for _, e := range rows {
		lots = append(lots, FromEntity(e))
	}
	return lots, nil
}
func LockParent(ctx context.Context, c *dbent.Client, id int64) (*dbent.UserSubscription, error) {
	return c.UserSubscription.Query().Unique(false).Where(usersubscription.IDEQ(id), LockRows).Only(ctx)
}

// EnsureLegacyLot is only for existing records, never the empty shell of a new paid purchase.
// Caller holds the parent lock. Historical lifetime remains a conservative lower bound.
func EnsureLegacyLot(ctx context.Context, c *dbent.Client, id int64) ([]Lot, error) {
	lots, err := ReadLots(ctx, c, id)
	if err != nil || len(lots) > 0 {
		return lots, err
	}
	s, err := c.UserSubscription.Query().Where(usersubscription.IDEQ(id)).WithPlan().WithGroup().Only(ctx)
	if err != nil {
		return nil, err
	}
	var daily, weekly, monthly *float64
	if s.Edges.Plan != nil {
		p := s.Edges.Plan
		daily, weekly, monthly = p.DailyLimitUsd, p.WeeklyLimitUsd, p.MonthlyLimitUsd
	} else if s.Edges.Group != nil {
		g := s.Edges.Group
		daily, weekly, monthly = g.DailyLimitUsd, g.WeeklyLimitUsd, g.MonthlyLimitUsd
	} else {
		return nil, errors.New("subscription quota snapshot unavailable")
	}
	status := "active"
	if !s.ExpiresAt.After(time.Now()) {
		status = "expired"
	}
	e, err := c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(id).SetNillablePlanID(s.PlanID).SetSourceType("legacy").SetStatus(status).SetStartsAt(s.StartsAt).SetExpiresAt(s.ExpiresAt).SetNillableDailyLimitUsd(daily).SetNillableWeeklyLimitUsd(weekly).SetNillableMonthlyLimitUsd(monthly).SetNillableDailyWindowStart(s.DailyWindowStart).SetNillableWeeklyWindowStart(s.WeeklyWindowStart).SetNillableMonthlyWindowStart(s.MonthlyWindowStart).SetDailyUsageUsd(s.DailyUsageUsd).SetWeeklyUsageUsd(s.WeeklyUsageUsd).SetMonthlyUsageUsd(s.MonthlyUsageUsd).SetLifetimeUsageUsd(max(s.DailyUsageUsd, s.WeeklyUsageUsd, s.MonthlyUsageUsd)).SetCreatedAt(s.CreatedAt).Save(ctx)
	if err != nil {
		return nil, err
	}
	return []Lot{FromEntity(e)}, nil
}
func PersistLots(ctx context.Context, c *dbent.Client, lots []Lot) error {
	for _, e := range lots {
		u := c.UserSubscriptionEntitlement.UpdateOneID(e.ID).SetStatus(e.Status).SetExpiresAt(e.ExpiresAt).SetDailyUsageUsd(e.DailyUsageUSD).SetWeeklyUsageUsd(e.WeeklyUsageUSD).SetMonthlyUsageUsd(e.MonthlyUsageUSD).SetLifetimeUsageUsd(e.LifetimeUsageUSD)
		if e.DailyWindowStart == nil {
			u.ClearDailyWindowStart()
		} else {
			u.SetDailyWindowStart(*e.DailyWindowStart)
		}
		if e.WeeklyWindowStart == nil {
			u.ClearWeeklyWindowStart()
		} else {
			u.SetWeeklyWindowStart(*e.WeeklyWindowStart)
		}
		if e.MonthlyWindowStart == nil {
			u.ClearMonthlyWindowStart()
		} else {
			u.SetMonthlyWindowStart(*e.MonthlyWindowStart)
		}
		if err := u.Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
func RefreshParent(ctx context.Context, c *dbent.Client, id int64, now time.Time) error {
	lots, err := ReadLots(ctx, c, id)
	if err != nil || len(lots) == 0 {
		return err
	}
	s, err := c.UserSubscription.Get(ctx, id)
	if err != nil {
		return err
	}

	return RefreshParentSnapshot(ctx, c, s, lots, now)

}
func RecordOperation(ctx context.Context, c *dbent.Client, id, lotID int64, operation, source, reference string, actor int64, detail map[string]any) error {
	return c.SubscriptionOperation.Create().SetSubscriptionID(id).SetEntitlementID(lotID).SetOperation(operation).SetSourceType(source).SetSourceReference(reference).SetActorID(actor).SetDetail(detail).Exec(ctx)
}
func PreviewPurchase(lots []Lot, mode string, quantity, days int, daily, weekly, monthly *float64, now time.Time) ([]Lot, int, error) {
	mode, quantity, err := PurchaseOptions(mode, quantity)
	if err != nil {
		return nil, 0, err
	}
	if days <= 0 {
		return nil, 0, infraerrors.BadRequest("INVALID_SUBSCRIPTION_DAYS", "validity days must be positive")
	}
	out := append([]Lot(nil), lots...)
	SortLots(out)
	active := 0
	for _, e := range out {
		if e.Status == "refund_pending" || e.Status == "suspended" || e.Status == "revoked" {
			return nil, 0, ErrStateConflict
		}
		if e.SourceType != "campaign" && e.Active(now) {
			active++
		}
	}
	if mode == "renew" && active > 0 {
		if quantity > active {
			return nil, active, ErrRenewQuantity
		}
		left := quantity
		for i := range out {
			if left > 0 && out[i].SourceType != "campaign" && out[i].Active(now) {
				out[i].ExpiresAt = out[i].ExpiresAt.AddDate(0, 0, days)
				if out[i].ExpiresAt.After(MaxExpiry) {
					return nil, active, ErrStateConflict
				}
				left--
			}
		}
	} else {
		expiry := now.AddDate(0, 0, days)
		if expiry.After(MaxExpiry) {
			return nil, active, ErrStateConflict
		}
		for i := 0; i < quantity; i++ {
			out = append(out, Lot{Status: "active", StartsAt: now, ExpiresAt: expiry, DailyLimitUSD: daily, WeeklyLimitUSD: weekly, MonthlyLimitUSD: monthly})
		}
	}
	return out, active, nil
}

// Purchase runs inside the caller's transaction. Every paid operation has one durable line per lot.
func Purchase(ctx context.Context, c *dbent.Client, id int64, planID *int64, orderID int64, days, quantity int, mode string, daily, weekly, monthly *float64, source, reference string, actor int64, now time.Time) error {
	// PostgreSQL stores microseconds; normalize before persisting and comparing starts_at.
	now = now.Truncate(time.Microsecond)
	parent, err := LockParent(ctx, c, id)
	if err != nil {
		return err
	}
	if parent.Status != "active" && parent.Status != "expired" {
		return ErrStateConflict
	}
	if orderID > 0 {
		n, err := c.SubscriptionEntitlementOrder.Query().Where(subscriptionentitlementorder.OrderIDEQ(orderID)).Count(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
	}
	lots, err := ReadLots(ctx, c, id)
	if err != nil {
		return err
	}
	projected, _, err := PreviewPurchase(lots, mode, quantity, days, daily, weekly, monthly, now)
	if err != nil {
		return err
	}
	originals := map[int64]Lot{}
	for _, e := range lots {
		originals[e.ID] = e
	}
	idx := 0
	for _, e := range projected {
		op := "renew"
		var before *time.Time
		if e.ID == 0 {
			op = "create"
			b := c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(id).SetNillablePlanID(planID).SetLotIndex(idx).SetPurchaseMode(mode).SetSourceType(source).SetSourceReference(reference).SetStatus("active").SetStartsAt(e.StartsAt).SetExpiresAt(e.ExpiresAt).SetNillableDailyLimitUsd(daily).SetNillableWeeklyLimitUsd(weekly).SetNillableMonthlyLimitUsd(monthly)
			if orderID > 0 {
				b.SetSourceOrderID(orderID)
			}
			row, err := b.Save(ctx)
			if err != nil {
				return err
			}
			e = FromEntity(row)
		} else {
			prev := originals[e.ID]
			if e.ExpiresAt.Equal(prev.ExpiresAt) {
				continue
			}
			t := prev.ExpiresAt
			before = &t
			if err := c.UserSubscriptionEntitlement.UpdateOneID(e.ID).SetExpiresAt(e.ExpiresAt).Exec(ctx); err != nil {
				return err
			}
		}
		if orderID > 0 {
			if err := c.SubscriptionEntitlementOrder.Create().SetEntitlementID(e.ID).SetOrderID(orderID).SetLotIndex(idx).SetOperation(op).SetDaysAdded(days).SetNillableBeforeExpiresAt(before).SetAfterExpiresAt(e.ExpiresAt).Exec(ctx); err != nil {
				return err
			}
		}
		if err := RecordOperation(ctx, c, id, e.ID, op, source, reference, actor, map[string]any{"before_expires_at": before, "after_expires_at": e.ExpiresAt, "order_id": orderID}); err != nil {
			return err
		}
		idx++
	}
	return RefreshParent(ctx, c, id, now)
}
func PurchaseReference(id int64) string { return strconv.FormatInt(id, 10) }
func CheckMutable(lots []Lot) error {
	for _, e := range lots {
		if e.Status == "refund_pending" {
			return ErrStateConflict
		}
	}
	return nil
}
func ValidateSelection(lots []Lot, ids []int64, now time.Time) ([]Lot, error) {
	if err := CheckMutable(lots); err != nil {
		return nil, err
	}
	var selected []Lot
	eligible := map[int64]Lot{}
	for _, e := range lots {
		if e.Status == "active" || e.Status == "expired" {
			eligible[e.ID] = e
		}
	}
	if len(ids) == 0 {
		if len(eligible) != 1 {
			return nil, ErrSelection
		}
		for _, e := range eligible {
			selected = append(selected, e)
		}
		return selected, nil
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		e, ok := eligible[id]
		if !ok || seen[id] {
			return nil, fmt.Errorf("invalid entitlement selection: %d", id)
		}
		seen[id] = true
		selected = append(selected, e)
	}
	return selected, nil
}

// LockRows retains real PostgreSQL locks; SQLite fixtures use their enclosing transaction.
func LockRows(s *entsql.Selector) {
	if s.Dialect() != dialect.SQLite {
		s.ForUpdate()
	}
}

func RefreshParentSnapshot(ctx context.Context, c *dbent.Client, parent *dbent.UserSubscription, lots []Lot, now time.Time) error {
	a, expiry, status := ParentProjection(lots, now)
	if err := projectContractDaily(ctx, c, parent.ID, lots, now, &a); err != nil {
		return err
	}
	if parent.Status != "active" && parent.Status != "expired" {
		status = parent.Status
	}
	if parent.ExpiresAt.Equal(expiry) && parent.Status == status && parent.DailyUsageUsd == a.DailyUsageUSD && parent.WeeklyUsageUsd == a.WeeklyUsageUSD && parent.MonthlyUsageUsd == a.MonthlyUsageUSD {
		return nil
	}
	return c.UserSubscription.UpdateOneID(parent.ID).SetExpiresAt(expiry).SetStatus(status).SetDailyUsageUsd(a.DailyUsageUSD).SetWeeklyUsageUsd(a.WeeklyUsageUSD).SetMonthlyUsageUsd(a.MonthlyUsageUSD).Exec(ctx)
}
func UsageChanged(a, b Lot) bool {
	return a.DailyUsageUSD != b.DailyUsageUSD || a.WeeklyUsageUSD != b.WeeklyUsageUSD || a.MonthlyUsageUSD != b.MonthlyUsageUSD || a.LifetimeUsageUSD != b.LifetimeUsageUSD || !sameWindow(a.DailyWindowStart, b.DailyWindowStart) || !sameWindow(a.WeeklyWindowStart, b.WeeklyWindowStart) || !sameWindow(a.MonthlyWindowStart, b.MonthlyWindowStart)
}
