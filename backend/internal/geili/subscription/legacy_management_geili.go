package subscription

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/shopspring/decimal"
)

// LegacyLine is a signed commercial snapshot, deliberately excluding usage.
// Usage is read under the parent lock at fulfillment, never restored from a quote.
type LegacyLine struct {
	EntitlementID int64           `json:"entitlement_id"`
	Before        *LegacyRight    `json:"before,omitempty"`
	After         LegacyRight     `json:"after"`
	BillableDays  int             `json:"billable_days"`
	Amount        decimal.Decimal `json:"amount"`
}
type LegacyRight struct {
	PlanID     *int64    `json:"plan_id,omitempty"`
	SourceType string    `json:"source_type"`
	Status     string    `json:"status"`
	StartsAt   time.Time `json:"starts_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	DailyUSD   *float64  `json:"daily_limit_usd"`
}
type LegacyChange struct {
	Target Plan         `json:"target"`
	Lines  []LegacyLine `json:"lines"`
	Anchor *LegacyLine  `json:"anchor,omitempty"`
}

func legacyRight(l Lot) LegacyRight {
	return LegacyRight{l.PlanID, l.SourceType, l.Status, l.StartsAt, l.ExpiresAt, l.DailyLimitUSD}
}
func sameLegacyRight(l Lot, r LegacyRight) bool {
	return reflect.DeepEqual(l.PlanID, r.PlanID) && l.SourceType == r.SourceType && l.Status == r.Status && l.StartsAt.Equal(r.StartsAt) && l.ExpiresAt.Equal(r.ExpiresAt) && reflect.DeepEqual(l.DailyLimitUSD, r.DailyUSD)
}

// PreviewLegacy is pure. resolved contains only unambiguously recognized,
// currently offered commercial tiers; a special sibling does not block others.
func PreviewLegacy(current *Contract, lots []Lot, resolved map[int64]Plan, target Plan, operation string, ids []int64, anchorID int64, units, periods int, now time.Time) (Change, *LegacyChange, error) {
	out := Change{Operation: operation, Units: units, Periods: periods}
	if current == nil || current.Mode != ContractModeLegacy || !current.Active(now) || !target.valid() {
		return out, nil, ErrStateConflict
	}
	before := *current
	out.Before = &before
	out.After = before
	out.After.Revision++
	// Target sales metadata is needed by the existing order pipeline. The actual
	// legacy contract's identity is kept by ApplyLegacyChange.
	out.After.PlanID = target.ID
	out.After.PeriodDays = target.PeriodDays
	lchange := &LegacyChange{Target: target}
	index := map[int64]Lot{}
	for _, l := range lots {
		index[l.ID] = l
	}
	eligible := func(id int64) (Lot, Plan, bool) {
		l, ok := index[id]
		p, known := resolved[id]
		return l, p, ok && known && l.Active(now) && l.SourceType != "campaign" && p.valid()
	}
	switch operation {
	case "renew", "upgrade":
		if len(ids) < 1 || len(ids) > 100 || units != 0 || anchorID != 0 {
			return out, nil, ErrSelection
		}
		if operation == "renew" && (periods < 1 || periods > 10) || operation == "upgrade" && periods != 0 {
			return out, nil, ErrQuantity
		}
		chosen := append([]int64(nil), ids...)
		sort.Slice(chosen, func(i, j int) bool { return chosen[i] < chosen[j] })
		var source Plan
		for i, id := range chosen {
			if i > 0 && id == chosen[i-1] {
				return out, nil, ErrSelection
			}
			l, p, ok := eligible(id)
			if !ok {
				return out, nil, ErrSelection
			}
			if i == 0 {
				source = p
			}
			if p.ID != source.ID || p.Kind != target.Kind || p.PeriodDays != target.PeriodDays {
				return out, nil, ErrContractType
			}
			b := legacyRight(l)
			line := LegacyLine{EntitlementID: id, Before: &b, After: b}
			if operation == "renew" {
				if p.ID != target.ID || p.DailyUSD != target.DailyUSD {
					return out, nil, ErrContractTier
				}
				line.BillableDays = periods * p.PeriodDays
				line.After.ExpiresAt = l.ExpiresAt.Add(time.Duration(line.BillableDays) * 24 * time.Hour)
				line.Amount = target.Price.Mul(decimal.NewFromInt(int64(periods)))
			} else {
				if target.DailyUSD <= p.DailyUSD || !target.Price.GreaterThan(p.Price) {
					return out, nil, ErrContractTier
				}
				line.BillableDays = RemainingDays(l.ExpiresAt, now)
				line.After.PlanID = &lchange.Target.ID
				line.After.DailyUSD = &lchange.Target.DailyUSD
				line.Amount = target.Price.Sub(p.Price).Mul(decimal.NewFromInt(int64(line.BillableDays))).Div(decimal.NewFromInt(int64(p.PeriodDays)))
			}
			if line.After.ExpiresAt.After(MaxExpiry) {
				return out, nil, ErrStateConflict
			}
			out.Amount = out.Amount.Add(line.Amount)
			if line.BillableDays > out.BillableDays {
				out.BillableDays = line.BillableDays
			}
			lchange.Lines = append(lchange.Lines, line)
		}
	case "purchase", "stack":
		if len(ids) > 0 || units < 1 || units > 10 || periods != 0 {
			return out, nil, ErrQuantity
		}
		// Both additions require an existing recognized paid type, not a gift-only pool.
		supported := false
		for id := range resolved {
			_, p, ok := eligible(id)
			if ok && p.Kind == target.Kind && p.PeriodDays == target.PeriodDays {
				supported = true
			}
		}
		if !supported {
			return out, nil, ErrContractType
		}
		expiry := now.Add(time.Duration(target.PeriodDays) * 24 * time.Hour)
		days := target.PeriodDays
		if operation == "stack" {
			l, p, ok := eligible(anchorID)
			if !ok || p.ID != target.ID || p.DailyUSD != target.DailyUSD {
				return out, nil, ErrSelection
			}
			b := legacyRight(l)
			lchange.Anchor = &LegacyLine{EntitlementID: l.ID, Before: &b, After: b}
			expiry = l.ExpiresAt
			days = RemainingDays(expiry, now)
		} else if anchorID != 0 {
			return out, nil, ErrSelection
		}
		if expiry.After(MaxExpiry) {
			return out, nil, ErrStateConflict
		}
		amount := target.Price.Mul(decimal.NewFromInt(int64(days))).Div(decimal.NewFromInt(int64(target.PeriodDays)))
		for i := 0; i < units; i++ {
			lchange.Lines = append(lchange.Lines, LegacyLine{After: LegacyRight{PlanID: &lchange.Target.ID, SourceType: "payment", Status: "active", StartsAt: now, ExpiresAt: expiry, DailyUSD: &lchange.Target.DailyUSD}, BillableDays: days, Amount: amount})
		}
		out.Amount = amount.Mul(decimal.NewFromInt(int64(units)))
		out.BillableDays = days
	default:
		return out, nil, ErrContractOperation
	}
	out.Amount = out.Amount.Round(2)
	if !out.Amount.IsPositive() {
		return out, nil, ErrContractTier
	}
	return out, lchange, nil
}

func ValidateLegacyChange(current *Contract, lots []Lot, change Change, detail *LegacyChange, now time.Time, fulfillment bool) error {
	if detail == nil || change.Before == nil || current == nil || current.Mode != ContractModeLegacy {
		return ErrStateConflict
	}
	// A naturally expired pool may receive a full-cycle addition from a paid,
	// previously accepted order; no other mutation or term change is allowed.
	snapshot := *current
	snapshot.Status = change.Before.Status
	if !contractMatches(&snapshot, change.Before) || current.Status != "active" && !(fulfillment && change.Operation == "purchase" && current.Status == "expired") {
		return ErrStateConflict
	}
	if !fulfillment && !current.Active(now) {
		return ErrStateConflict
	}
	// A frozen refund must block new changes even if live siblings keep the
	// parent active. Existing pending-order checks cover new checkouts; this also
	// guards paid callbacks racing an administrator refund.
	for _, lot := range lots {
		if lot.Status == "refund_pending" {
			return ErrStateConflict
		}
	}
	byID := map[int64]Lot{}
	for _, l := range lots {
		byID[l.ID] = l
	}
	check := func(line LegacyLine) bool {
		l, ok := byID[line.EntitlementID]
		return ok && line.Before != nil && l.Active(now) && l.SourceType != "campaign" && sameLegacyRight(l, *line.Before)
	}
	if detail.Anchor != nil && !check(*detail.Anchor) {
		return ErrStateConflict
	}
	for _, line := range detail.Lines {
		if line.EntitlementID != 0 && !check(line) {
			return ErrStateConflict
		}
	}
	return nil
}

// ProjectLegacyLots is also used for previews. It never copies quoted usage.
func ProjectLegacyLots(lots []Lot, detail *LegacyChange) []Lot {
	out := append([]Lot(nil), lots...)
	for _, line := range detail.Lines {
		if line.EntitlementID == 0 {
			out = append(out, Lot{PlanID: line.After.PlanID, SourceType: "payment", Status: "active", StartsAt: line.After.StartsAt, ExpiresAt: line.After.ExpiresAt, DailyLimitUSD: line.After.DailyUSD})
			continue
		}
		for i := range out {
			if out[i].ID == line.EntitlementID {
				out[i].ExpiresAt = line.After.ExpiresAt
				out[i].PlanID = line.After.PlanID
				out[i].DailyLimitUSD = line.After.DailyUSD
			}
		}
	}
	return out
}

// ApplyLegacyChange runs inside the owner-locked payment transaction. The
// existing change journal drives exact reversal and duplicate callback recovery.
func ApplyLegacyChange(ctx context.Context, c *dbent.Client, change Change, detail *LegacyChange, orderID int64, now time.Time) (*Contract, error) {
	now = now.Truncate(time.Microsecond)
	if detail == nil || change.Before == nil || orderID <= 0 {
		return nil, ErrStateConflict
	}
	parent, err := LockParent(ctx, c, change.Before.SubscriptionID)
	if err != nil {
		return nil, err
	}
	now = time.Now().Truncate(time.Microsecond)
	if parent.UserID != change.Before.UserID {
		return nil, ErrStateConflict
	}
	var raw []byte
	err = scalar(ctx, c, `SELECT after_contract FROM subscription_contract_changes WHERE order_id=$1`, []any{orderID}, &raw)
	if err == nil {
		var previous Contract
		err = json.Unmarshal(raw, &previous)
		return &previous, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	current, err := LoadContract(ctx, c, parent.ID)
	if err != nil {
		return nil, err
	}
	lots, err := ReadLots(ctx, c, parent.ID)
	if err != nil {
		return nil, err
	}
	if err = ValidateLegacyChange(current, lots, change, detail, now, true); err != nil {
		return nil, err
	}
	if err = SyncDailyLedger(ctx, c, parent.ID, now); err != nil {
		return nil, err
	}
	beforeRaw, _ := recordJSON(current)
	beforeLots, _ := recordJSON(lots)
	baseline := decimal.Zero
	for _, l := range lots {
		baseline = baseline.Add(decimal.NewFromFloat(l.LifetimeUsageUSD))
	}
	fulfilled := make([]LegacyLine, 0, len(detail.Lines))
	for i, line := range detail.Lines {
		id := line.EntitlementID
		expiry := line.After.ExpiresAt
		if id == 0 {
			if change.Operation == "purchase" {
				expiry = now.Add(time.Duration(detail.Target.PeriodDays) * 24 * time.Hour)
			}
			if !expiry.After(now) || expiry.After(MaxExpiry) {
				return nil, ErrStateConflict
			}
			row, e := c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(parent.ID).SetPlanID(detail.Target.ID).SetSourceOrderID(orderID).SetLotIndex(i).SetPurchaseMode(change.Operation).SetSourceType("payment").SetSourceReference(PurchaseReference(orderID)).SetStartsAt(now).SetExpiresAt(expiry).SetDailyLimitUsd(detail.Target.DailyUSD).SetDailyWindowStart(DayStart(now)).Save(ctx)
			if e != nil {
				return nil, e
			}
			id = row.ID
		} else {
			u := c.UserSubscriptionEntitlement.UpdateOneID(id).SetExpiresAt(expiry)
			if change.Operation == "upgrade" {
				u.SetPlanID(detail.Target.ID).SetDailyLimitUsd(detail.Target.DailyUSD)
			}
			if err = u.Exec(ctx); err != nil {
				return nil, err
			}
		}
		line.EntitlementID = id
		line.After.ExpiresAt = expiry
		if line.Before == nil {
			line.After.StartsAt = now
		}
		fulfilled = append(fulfilled, line)
		if err = RecordOperation(ctx, c, parent.ID, id, change.Operation, "payment", PurchaseReference(orderID), 0, map[string]any{"before": line.Before, "after_expires_at": expiry, "plan_id": detail.Target.ID}); err != nil {
			return nil, err
		}
	}
	// Record the fulfilled timestamps/IDs separately from the immutable promise.
	orderRow, err := c.PaymentOrder.Get(ctx, orderID)
	if err != nil {
		return nil, err
	}
	snapshot := orderRow.SubscriptionSnapshot
	if snapshot == nil {
		snapshot = map[string]any{}
	}
	snapshot["fulfilled_entitlement_changes"] = fulfilled
	if err = c.PaymentOrder.UpdateOneID(orderID).SetSubscriptionSnapshot(snapshot).Exec(ctx); err != nil {
		return nil, err
	}
	after, err := MarkLegacyContract(ctx, c, parent.ID, now)
	if err != nil {
		return nil, err
	}
	if err = RefreshParent(ctx, c, parent.ID, now); err != nil {
		return nil, err
	}
	newLots, err := ReadLots(ctx, c, parent.ID)
	if err != nil {
		return nil, err
	}
	afterRaw, _ := recordJSON(after)
	afterLots, _ := recordJSON(newLots)
	_, err = c.ExecContext(ctx, `INSERT INTO subscription_contract_changes(subscription_id,order_id,operation,before_contract,after_contract,before_lots,after_lots,lifetime_usage_baseline,source_type,source_reference,actor_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, parent.ID, orderID, change.Operation, string(beforeRaw), string(afterRaw), string(beforeLots), string(afterLots), baseline.StringFixed(10), "payment", PurchaseReference(orderID), 0)
	return after, err
}
