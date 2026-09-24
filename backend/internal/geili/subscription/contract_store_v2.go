package subscription

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func LoadContract(ctx context.Context, q SQL, subID int64) (*Contract, error) {
	rows, err := q.QueryContext(ctx, `SELECT c.subscription_id,c.user_id,c.term_id,c.revision,c.mode,c.kind,c.plan_id,c.plan_name,c.unit_daily_usd,c.quantity,c.period_days,c.starts_at,c.expires_at,CASE WHEN s.deleted_at IS NOT NULL THEN 'revoked' ELSE s.status END FROM subscription_contracts c JOIN user_subscriptions s ON s.id=c.subscription_id WHERE c.subscription_id=$1`, subID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	c := &Contract{}
	if err = rows.Scan(&c.SubscriptionID, &c.UserID, &c.TermID, &c.Revision, &c.Mode, &c.Kind, &c.PlanID, &c.PlanName, &c.UnitDailyUSD, &c.Quantity, &c.PeriodDays, &c.StartsAt, &c.ExpiresAt, &c.Status); err != nil {
		return nil, err
	}
	return c, nil
}

func planDays(p *dbent.SubscriptionPlan) int {
	if p == nil {
		return 0
	}
	switch p.ValidityUnit {
	case "week", "weeks":
		return p.ValidityDays * 7
	case "month", "months":
		return p.ValidityDays * 30
	default:
		return p.ValidityDays
	}
}

// PlanFromEntity snapshots prices for one quotation. Legacy compatibility plans
// cannot silently become new products.
func PlanFromEntity(p *dbent.SubscriptionPlan) Plan {
	if p == nil {
		return Plan{}
	}
	out := Plan{ID: p.ID, Name: p.Name, PeriodDays: planDays(p), Price: decimal.NewFromFloat(p.Price)}
	if p.DailyLimitUsd != nil {
		out.DailyUSD = *p.DailyLimitUsd
	}
	if !p.IsLegacyCompat {
		out.Kind = RecognizePlan(out.PeriodDays, out.DailyUSD)
	}
	return out
}

// EnsureContract captures a legacy baseline while the caller holds the parent
// lock. It is idempotent and never clears or rewrites existing financial usage.
func EnsureContract(ctx context.Context, c *dbent.Client, subID int64, now time.Time) (*Contract, error) {
	existing, err := LoadContract(ctx, c, subID)
	if err != nil || existing != nil {
		return existing, err
	}
	parent, err := c.UserSubscription.Query().Where(usersubscription.IDEQ(subID)).WithPlan().Only(ctx)
	if err != nil {
		return nil, err
	}
	lots, err := EnsureLegacyLot(ctx, c, subID)
	if err != nil {
		return nil, err
	}
	active := []Lot{}
	outstanding := 0
	for _, lot := range lots {
		// A future or frozen purchased lot must remain visible in compatibility
		// mode; collapsing only today's active lot would discard that right.
		if lot.ExpiresAt.After(now) && lot.Status != "refunded" && lot.Status != "revoked" {
			outstanding++
		}
		if lot.Active(now) {
			active = append(active, lot)
		}
	}
	liveCount, err := c.UserSubscription.Query().Where(usersubscription.UserIDEQ(parent.UserID), usersubscription.ExpiresAtGT(now), usersubscription.StatusIn("active", "suspended")).Count(ctx)
	if err != nil {
		return nil, err
	}
	contract := &Contract{SubscriptionID: subID, UserID: parent.UserID, TermID: uuid.NewString(), Revision: 1, Mode: ContractModeLegacy, StartsAt: parent.StartsAt, ExpiresAt: parent.ExpiresAt, Status: parent.Status}
	if parent.PlanID != nil {
		contract.PlanID = *parent.PlanID
	}
	if parent.Edges.Plan != nil {
		contract.PlanName = parent.Edges.Plan.Name
	}
	if liveCount == 1 && len(active) == 1 && outstanding == 1 && parent.Status == "active" && !parent.StartsAt.After(now) && parent.DeletedAt == nil {
		lot := active[0]
		plan := parent.Edges.Plan
		if lot.PlanID != nil && (plan == nil || *lot.PlanID != plan.ID) {
			plan, err = c.SubscriptionPlan.Get(ctx, *lot.PlanID)
			if err != nil {
				return nil, err
			}
		}
		if lot.DailyLimitUSD != nil && plan != nil && !plan.IsLegacyCompat && plan.Price > 0 && plan.DailyLimitUsd != nil && *plan.DailyLimitUsd == *lot.DailyLimitUSD {
			days := planDays(plan)
			matchesPurchase := true
			if lot.SourceOrderID != nil {
				order, err := c.PaymentOrder.Get(ctx, *lot.SourceOrderID)
				if err != nil {
					return nil, err
				}
				if order.SubscriptionDays != nil && *order.SubscriptionDays > 0 && *order.SubscriptionDays != days {
					matchesPurchase = false
				}
			}
			kind := RecognizePlan(days, *lot.DailyLimitUSD)
			if kind != "" && matchesPurchase {
				contract.Mode, contract.Kind, contract.PlanID, contract.PlanName, contract.UnitDailyUSD, contract.Quantity, contract.PeriodDays = ContractModeV2, kind, plan.ID, plan.Name, *lot.DailyLimitUSD, 1, days
				contract.ExpiresAt = lot.ExpiresAt
			}
		}
	}
	if err = saveContract(ctx, c, contract); err != nil {
		return nil, err
	}
	if err = seedDailyBaseline(ctx, c, contract, lots, now); err != nil {
		return nil, err
	}
	return contract, nil
}

// CurrentContract requires the owner's row lock before quoting or purchasing.
// Multiple still-live pools always remain in compatibility mode.
func CurrentContract(ctx context.Context, c *dbent.Client, userID int64, now time.Time) (*Contract, error) {
	parents, err := c.UserSubscription.Query().Where(usersubscription.UserIDEQ(userID), usersubscription.ExpiresAtGT(now), usersubscription.StatusIn("active", "suspended")).Order(usersubscription.ByID()).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(parents) == 0 {
		return nil, nil
	}
	var selected *Contract
	paidParents := 0
	for _, parent := range parents {
		lots, e := ReadLots(ctx, c, parent.ID)
		if e != nil {
			return nil, e
		}
		if !CampaignOnly(lots) {
			paidParents++
		}
	}
	for _, parent := range parents {
		if _, err = LockParent(ctx, c, parent.ID); err != nil {
			return nil, err
		}
		contract, e := EnsureContract(ctx, c, parent.ID, now)
		if e != nil {
			return nil, e
		}
		lots, e := ReadLots(ctx, c, parent.ID)
		if e != nil {
			return nil, e
		}
		if selected == nil {
			selected = contract
		}
		// A gift may keep the pool alive beyond the paid term. Return a
		// compatibility projection with the effective expiry, never rewrite the
		// persisted paid contract during a quote.
		if contract.Mode == ContractModeV2 && !contract.ExpiresAt.After(now) {
			for _, lot := range lots {
				if lot.SourceType == "campaign" && lot.Active(now) {
					projection := *contract
					projection.Mode, projection.ExpiresAt = ContractModeLegacy, parent.ExpiresAt
					return &projection, nil
				}
			}
		}
		if paidParents > 1 || contract.Mode == ContractModeLegacy {
			if _, err = c.ExecContext(ctx, `UPDATE subscription_contracts SET mode='legacy_daily',is_current=FALSE,updated_at=$2 WHERE subscription_id=$1`, parent.ID, now); err != nil {
				return nil, err
			}
			selected.Mode = ContractModeLegacy
		}

	}
	return selected, nil
}

func saveContract(ctx context.Context, q SQL, c *Contract) error {
	if _, err := q.ExecContext(ctx, `INSERT INTO subscription_contract_terms(term_id,subscription_id,starts_at,expires_at) VALUES($1,$2,$3,$4) ON CONFLICT(term_id) DO UPDATE SET expires_at=EXCLUDED.expires_at`, c.TermID, c.SubscriptionID, c.StartsAt, c.ExpiresAt); err != nil {
		return err
	}
	if c.Mode == ContractModeV2 {
		if _, err := q.ExecContext(ctx, `UPDATE subscription_contracts SET is_current=FALSE WHERE user_id=$1 AND subscription_id<>$2`, c.UserID, c.SubscriptionID); err != nil {
			return err
		}
	}
	_, err := q.ExecContext(ctx, `INSERT INTO subscription_contracts(subscription_id,user_id,term_id,revision,mode,kind,plan_id,plan_name,unit_daily_usd,quantity,period_days,starts_at,expires_at,is_current,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) ON CONFLICT(subscription_id) DO UPDATE SET user_id=EXCLUDED.user_id,term_id=EXCLUDED.term_id,revision=EXCLUDED.revision,mode=EXCLUDED.mode,kind=EXCLUDED.kind,plan_id=EXCLUDED.plan_id,plan_name=EXCLUDED.plan_name,unit_daily_usd=EXCLUDED.unit_daily_usd,quantity=EXCLUDED.quantity,period_days=EXCLUDED.period_days,starts_at=EXCLUDED.starts_at,expires_at=EXCLUDED.expires_at,is_current=EXCLUDED.is_current,updated_at=EXCLUDED.updated_at`, c.SubscriptionID, c.UserID, c.TermID, c.Revision, c.Mode, c.Kind, c.PlanID, c.PlanName, c.UnitDailyUSD, c.Quantity, c.PeriodDays, c.StartsAt, c.ExpiresAt, c.Mode == ContractModeV2, time.Now())
	return err
}

func scalar(ctx context.Context, q SQL, query string, args []any, dest ...any) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if rows.Err() != nil {
			return rows.Err()
		}
		return sql.ErrNoRows
	}
	return rows.Scan(dest...)
}

func seedDailyBaseline(ctx context.Context, q SQL, c *Contract, lots []Lot, now time.Time) error {
	day := DayStart(now)
	used := decimal.Zero
	for _, lot := range lots {
		if lot.Status == "refunded" || lot.Status == "revoked" || lot.DailyWindowStart == nil {
			continue
		}
		if DayStart(*lot.DailyWindowStart).Equal(day) {
			used = used.Add(decimal.NewFromFloat(lot.DailyUsageUSD))
		}
	}
	var watermark int64
	if err := scalar(ctx, q, `SELECT COALESCE(MAX(a.id),0) FROM subscription_usage_allocations a JOIN subscription_requests r ON r.request_key=a.request_key WHERE r.subscription_id=$1`, []any{c.SubscriptionID}, &watermark); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO subscription_daily_usage(subscription_id,term_id,usage_date,used_usd) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, c.SubscriptionID, c.TermID, day.Format("2006-01-02"), used.StringFixed(10)); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO subscription_ledger_state(subscription_id,allocation_watermark,baseline_date) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, c.SubscriptionID, watermark, day.Format("2006-01-02")); err != nil {
		return err
	}
	return bindUnboundAdmissions(ctx, q, c.SubscriptionID)
}

func BindAdmission(ctx context.Context, q SQL, requestKey string, c *Contract, now time.Time) error {
	if c == nil {
		return errors.New("subscription contract is required for admission")
	}
	_, err := q.ExecContext(ctx, `INSERT INTO subscription_request_contracts(request_key,term_id,usage_date) VALUES($1,$2,$3) ON CONFLICT(request_key) DO NOTHING`, requestKey, c.TermID, DayStart(now).Format("2006-01-02"))
	return err
}

func bindUnboundAdmissions(ctx context.Context, q SQL, subID int64) error {
	var watermark int64
	if err := scalar(ctx, q, `SELECT COALESCE((SELECT request_watermark FROM subscription_admission_cursor WHERE subscription_id=$1),0)`, []any{subID}, &watermark); err != nil {
		return err
	}
	rows, err := q.QueryContext(ctx, `SELECT r.id,r.request_key,r.admitted_at,COALESCE(b.term_id,(SELECT t.term_id FROM subscription_contract_terms t WHERE t.subscription_id=r.subscription_id AND t.starts_at<=r.admitted_at AND t.expires_at>r.admitted_at ORDER BY t.starts_at DESC LIMIT 1),(SELECT t.term_id FROM subscription_contract_terms t WHERE t.subscription_id=r.subscription_id ORDER BY t.starts_at LIMIT 1)) FROM subscription_requests r LEFT JOIN subscription_request_contracts b ON b.request_key=r.request_key WHERE r.subscription_id=$1 AND r.id>$2 ORDER BY r.id`, subID, watermark)
	if err != nil {
		return err
	}
	type binding struct {
		id        int64
		key, term string
		admitted  time.Time
	}
	var pending []binding
	for rows.Next() {
		var b binding
		if err = rows.Scan(&b.id, &b.key, &b.admitted, &b.term); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, b := range pending {
		if _, err = q.ExecContext(ctx, `INSERT INTO subscription_request_contracts(request_key,term_id,usage_date) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, b.key, b.term, DayStart(b.admitted).Format("2006-01-02")); err != nil {
			return err
		}
		watermark = b.id
	}
	if len(pending) > 0 {
		_, err = q.ExecContext(ctx, `INSERT INTO subscription_admission_cursor(subscription_id,request_watermark) VALUES($1,$2) ON CONFLICT(subscription_id) DO UPDATE SET request_watermark=EXCLUDED.request_watermark`, subID, watermark)
	}
	return err
}

// SyncDailyLedger catches allocations above the locked migration watermark,
// including settlement by an old process still running during deployment.
func SyncDailyLedger(ctx context.Context, q SQL, subID int64, now time.Time) error {
	contract, err := LoadContract(ctx, q, subID)
	if err != nil {
		return err
	}
	if contract == nil {
		return nil
	}
	if err = bindUnboundAdmissions(ctx, q, subID); err != nil {
		return err
	}
	var watermark int64
	if err = scalar(ctx, q, `SELECT allocation_watermark FROM subscription_ledger_state WHERE subscription_id=$1`, []any{subID}, &watermark); err != nil {
		return err
	}
	rows, err := q.QueryContext(ctx, `SELECT a.id,b.term_id,CAST(b.usage_date AS TEXT),a.cost_usd FROM subscription_usage_allocations a JOIN subscription_requests r ON r.request_key=a.request_key JOIN subscription_request_contracts b ON b.request_key=r.request_key WHERE r.subscription_id=$1 AND a.id>$2 ORDER BY a.id`, subID, watermark)
	if err != nil {
		return err
	}
	type delta struct {
		id        int64
		term, day string
		cost      decimal.Decimal
	}
	var deltas []delta
	for rows.Next() {
		var d delta
		if err = rows.Scan(&d.id, &d.term, &d.day, &d.cost); err != nil {
			rows.Close()
			return err
		}
		deltas = append(deltas, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, d := range deltas {
		if _, err = q.ExecContext(ctx, `INSERT INTO subscription_daily_usage(subscription_id,term_id,usage_date,used_usd) VALUES($1,$2,$3,$4) ON CONFLICT(subscription_id,term_id,usage_date) DO UPDATE SET used_usd=subscription_daily_usage.used_usd+EXCLUDED.used_usd`, subID, d.term, d.day, d.cost.StringFixed(10)); err != nil {
			return err
		}
		watermark = d.id
	}
	if len(deltas) > 0 {
		_, err = q.ExecContext(ctx, `UPDATE subscription_ledger_state SET allocation_watermark=$2 WHERE subscription_id=$1`, subID, watermark)
	}
	return err
}

// ReadDailyUsage projects both the ledger and not-yet-bridged allocations in a
// single database statement, so dashboards need not mutate accounting state.
func ReadDailyUsage(ctx context.Context, q SQL, subID int64, termID string, now time.Time) (float64, error) {
	day := DayStart(now)
	var used decimal.Decimal
	err := scalar(ctx, q, `SELECT COALESCE((SELECT used_usd FROM subscription_daily_usage WHERE subscription_id=$1 AND term_id=$2 AND usage_date=$3),0)+COALESCE((SELECT SUM(a.cost_usd) FROM subscription_usage_allocations a JOIN subscription_requests r ON r.request_key=a.request_key LEFT JOIN subscription_request_contracts b ON b.request_key=r.request_key JOIN subscription_ledger_state st ON st.subscription_id=r.subscription_id WHERE r.subscription_id=$1 AND a.id>st.allocation_watermark AND r.admitted_at>=$4 AND r.admitted_at<$5 AND COALESCE(b.term_id,(SELECT t.term_id FROM subscription_contract_terms t WHERE t.subscription_id=r.subscription_id AND t.starts_at<=r.admitted_at AND t.expires_at>r.admitted_at ORDER BY t.starts_at DESC LIMIT 1),(SELECT t.term_id FROM subscription_contract_terms t WHERE t.subscription_id=r.subscription_id ORDER BY t.starts_at LIMIT 1))=$2),0)`, []any{subID, termID, day.Format("2006-01-02"), day, day.Add(24 * time.Hour)}, &used)
	return used.InexactFloat64(), err
}

func recordJSON(v any) ([]byte, error) { return json.Marshal(v) }
func contractMatches(current, before *Contract) bool {
	return current != nil && before != nil && current.SubscriptionID == before.SubscriptionID && current.UserID == before.UserID && current.TermID == before.TermID && current.Revision == before.Revision && current.Mode == before.Mode && current.PlanID == before.PlanID && current.UnitDailyUSD == before.UnitDailyUSD && current.Quantity == before.Quantity && current.ExpiresAt.Equal(before.ExpiresAt) && current.Status == before.Status
}

// ApplyContractChange runs inside the caller's owner-locked transaction. Prices
// and entitlements come from the accepted snapshot, never mutable sale plans.
func ApplyContractChange(ctx context.Context, c *dbent.Client, change Change, orderID int64, source, reference string, actor int64, now time.Time) (*Contract, error) {
	now = now.Truncate(time.Microsecond)
	if change.After.SubscriptionID <= 0 || change.After.UserID <= 0 {
		return nil, ErrStateConflict
	}
	parent, err := LockParent(ctx, c, change.After.SubscriptionID)
	if err != nil {
		return nil, err
	}
	if parent.UserID != change.After.UserID {
		return nil, ErrStateConflict
	}
	if orderID > 0 {
		var raw []byte
		e := scalar(ctx, c, `SELECT after_contract FROM subscription_contract_changes WHERE order_id=$1`, []any{orderID}, &raw)
		if e == nil {
			var previous Contract
			if err = json.Unmarshal(raw, &previous); err != nil {
				return nil, err
			}
			return &previous, nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
	}
	current, err := LoadContract(ctx, c, parent.ID)
	if err != nil {
		return nil, err
	}
	lots, err := ReadLots(ctx, c, parent.ID)
	if err != nil {
		return nil, err
	}
	// A repurchase of an unrepresented expired parent must first retain the
	// previous term's baseline and outstanding request identities.
	if current == nil && len(lots) > 0 {
		current, err = EnsureContract(ctx, c, parent.ID, now)
		if err != nil {
			return nil, err
		}
	}
	if current != nil {
		if err = SyncDailyLedger(ctx, c, parent.ID, now); err != nil {
			return nil, err
		}
	}
	after := change.After
	if change.Operation == "purchase" {
		if parent.Status != "active" && parent.Status != "expired" {
			return nil, ErrStateConflict
		}
		if current != nil && current.ExpiresAt.After(now) {
			return nil, ErrStateConflict
		}
		live, err := c.UserSubscription.Query().Where(usersubscription.UserIDEQ(parent.UserID), usersubscription.IDNEQ(parent.ID), usersubscription.ExpiresAtGT(now), usersubscription.StatusIn("active", "suspended")).Exist(ctx)
		if err != nil {
			return nil, err
		}
		if live {
			return nil, ErrContractCompatibility
		}
		after.TermID = uuid.NewString()
		after.Revision = 1
		if current != nil {
			after.Revision = current.Revision + 1
		}
		after.StartsAt = now
		after.ExpiresAt = now.Add(time.Duration(after.PeriodDays) * 24 * time.Hour)
	} else {
		if !contractMatches(current, change.Before) || !current.Active(now) {
			return nil, ErrStateConflict
		}
		after.TermID = current.TermID
		after.Revision = current.Revision + 1
	}
	if after.Mode != ContractModeV2 || after.Quantity < 1 || RecognizePlan(after.PeriodDays, after.UnitDailyUSD) != after.Kind || after.ExpiresAt.After(MaxExpiry) {
		return nil, ErrStateConflict
	}
	if err = CheckMutable(lots); err != nil {
		return nil, err
	}
	beforeLots, err := recordJSON(lots)
	if err != nil {
		return nil, err
	}
	baseline := decimal.Zero
	for _, lot := range lots {
		baseline = baseline.Add(decimal.NewFromFloat(lot.LifetimeUsageUSD))
	}
	if change.Operation == "purchase" || change.Operation == "stack" {
		for i := 0; i < change.Units; i++ {
			b := c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(parent.ID).SetPlanID(after.PlanID).SetLotIndex(i).SetPurchaseMode(change.Operation).SetSourceType(source).SetSourceReference(reference).SetStatus("active").SetStartsAt(now).SetExpiresAt(after.ExpiresAt).SetDailyLimitUsd(after.UnitDailyUSD).SetDailyWindowStart(DayStart(now))
			if orderID > 0 {
				b.SetSourceOrderID(orderID)
			}
			if _, err = b.Save(ctx); err != nil {
				return nil, err
			}
		}
	} else if change.Operation == "renew" || change.Operation == "upgrade" {
		for _, lot := range lots {
			if lot.SourceType == "campaign" || !lot.Active(now) {
				continue
			}
			b := c.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetExpiresAt(after.ExpiresAt)
			if change.Operation == "upgrade" {
				b.SetPlanID(after.PlanID).SetDailyLimitUsd(after.UnitDailyUSD)
			}
			if err = b.Exec(ctx); err != nil {
				return nil, err
			}
		}
	} else {
		return nil, ErrContractOperation
	}
	if err = saveContract(ctx, c, &after); err != nil {
		return nil, err
	}
	if current == nil {
		if err = seedDailyBaseline(ctx, c, &after, lots, now); err != nil {
			return nil, err
		}
	}
	if _, err = c.ExecContext(ctx, `INSERT INTO subscription_daily_usage(subscription_id,term_id,usage_date,used_usd) VALUES($1,$2,$3,0) ON CONFLICT DO NOTHING`, parent.ID, after.TermID, DayStart(now).Format("2006-01-02")); err != nil {
		return nil, err
	}
	used, err := ReadDailyUsage(ctx, c, parent.ID, after.TermID, now)
	if err != nil {
		return nil, err
	}
	// Contract changes must never truncate an independent campaign lot. The
	// parent expiry is the effective pool expiry; the contract row keeps the
	// paid term expiry used for quotation semantics.
	if _, err = c.ExecContext(ctx, `UPDATE user_subscriptions SET plan_id=$2,starts_at=$3,expires_at=CASE WHEN COALESCE((SELECT MAX(expires_at) FROM user_subscription_entitlements WHERE user_subscription_id=$1 AND source_type='campaign' AND status='active'),$4) > $4 THEN (SELECT MAX(expires_at) FROM user_subscription_entitlements WHERE user_subscription_id=$1 AND source_type='campaign' AND status='active') ELSE $4 END,status='active',daily_usage_usd=$5,daily_window_start=$6,updated_at=$7 WHERE id=$1`, parent.ID, after.PlanID, after.StartsAt, after.ExpiresAt, used, DayStart(now), now); err != nil {
		return nil, err
	}
	newLots, err := ReadLots(ctx, c, parent.ID)
	if err != nil {
		return nil, err
	}
	afterLots, err := recordJSON(newLots)
	if err != nil {
		return nil, err
	}
	beforeRaw, err := recordJSON(change.Before)
	if err != nil {
		return nil, err
	}
	afterRaw, err := recordJSON(after)
	if err != nil {
		return nil, err
	}
	var order any
	if orderID > 0 {
		order = orderID
	}
	_, err = c.ExecContext(ctx, `INSERT INTO subscription_contract_changes(subscription_id,order_id,operation,before_contract,after_contract,before_lots,after_lots,lifetime_usage_baseline,source_type,source_reference,actor_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, parent.ID, order, change.Operation, string(beforeRaw), string(afterRaw), string(beforeLots), string(afterLots), baseline.StringFixed(10), source, reference, actor)
	if err != nil {
		return nil, fmt.Errorf("record contract change: %w", err)
	}
	return &after, nil
}

// projectContractDaily keeps the compatibility parent snapshot on the same
// Beijing day ledger as admission, independent of the server timezone.
func projectContractDaily(ctx context.Context, q SQL, subID int64, lots []Lot, now time.Time, summary *Summary) error {
	contract, err := LoadContract(ctx, q, subID)
	if err != nil || contract == nil {
		return err
	}
	used, err := ReadDailyUsage(ctx, q, subID, contract.TermID, now)
	if err != nil {
		return err
	}
	projected := ContractSummary(contract, lots, used, now)
	summary.DailyUsageUSD = projected.DailyUsageUSD
	return nil
}
