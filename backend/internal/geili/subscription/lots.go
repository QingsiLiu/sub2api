package subscription

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/shopspring/decimal"
)

// Lot carries a quota snapshot independent of mutable sale-plan configuration.
type Lot struct {
	AllocationWatermark int64      `json:"allocation_watermark,omitempty"`
	SourceType          string     `json:"source_type"`
	SourceReference     string     `json:"source_reference,omitempty"`
	ID                  int64      `json:"id"`
	UserSubscriptionID  int64      `json:"user_subscription_id"`
	PlanID              *int64     `json:"plan_id,omitempty"`
	SourceOrderID       *int64     `json:"source_order_id,omitempty"`
	LotIndex            int        `json:"lot_index"`
	PurchaseMode        string     `json:"purchase_mode"`
	Status              string     `json:"status"`
	StartsAt            time.Time  `json:"starts_at"`
	ExpiresAt           time.Time  `json:"expires_at"`
	DailyLimitUSD       *float64   `json:"daily_limit_usd"`
	WeeklyLimitUSD      *float64   `json:"weekly_limit_usd"`
	MonthlyLimitUSD     *float64   `json:"monthly_limit_usd"`
	DailyWindowStart    *time.Time `json:"daily_window_start"`
	WeeklyWindowStart   *time.Time `json:"weekly_window_start"`
	MonthlyWindowStart  *time.Time `json:"monthly_window_start"`
	DailyUsageUSD       float64    `json:"daily_usage_usd"`
	WeeklyUsageUSD      float64    `json:"weekly_usage_usd"`
	MonthlyUsageUSD     float64    `json:"monthly_usage_usd"`
	LifetimeUsageUSD    float64    `json:"lifetime_usage_usd"`
	RefundedAt          *time.Time `json:"refunded_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

type Summary struct {
	ActiveLotCount  int        `json:"active_lot_count"`
	DailyLimitUSD   *float64   `json:"daily_limit_usd"`
	WeeklyLimitUSD  *float64   `json:"weekly_limit_usd"`
	MonthlyLimitUSD *float64   `json:"monthly_limit_usd"`
	DailyUsageUSD   float64    `json:"daily_usage_usd"`
	WeeklyUsageUSD  float64    `json:"weekly_usage_usd"`
	MonthlyUsageUSD float64    `json:"monthly_usage_usd"`
	NextExpiryAt    *time.Time `json:"next_expiry_at"`
	ExpiresAt       *time.Time `json:"expires_at"`
	DailyResetAt    *time.Time `json:"daily_reset_at"`
	WeeklyResetAt   *time.Time `json:"weekly_reset_at"`
	MonthlyResetAt  *time.Time `json:"monthly_reset_at"`
	AvailableUSD    float64    `json:"-"`
}

func (e Lot) Active(now time.Time) bool {
	return e.Status == "active" && !e.StartsAt.After(now) && e.ExpiresAt.After(now)
}
func earlier(dst **time.Time, v time.Time) {
	if *dst == nil || v.Before(**dst) {
		t := v
		*dst = &t
	}
}
func finite(v *float64) bool { return v != nil && *v > 0 }
func minCapacity(limit *float64, used float64) float64 {
	if !finite(limit) {
		return math.Inf(1)
	}
	return math.Max(0, *limit-used)
}
func (e Lot) Capacity() float64 {
	return math.Min(minCapacity(e.DailyLimitUSD, e.DailyUsageUSD), math.Min(minCapacity(e.WeeklyLimitUSD, e.WeeklyUsageUSD), minCapacity(e.MonthlyLimitUSD, e.MonthlyUsageUSD)))
}
func add(a, b float64) float64 {
	v, _ := decimal.NewFromFloat(a).Add(decimal.NewFromFloat(b)).Round(10).Float64()
	return v
}

// windowResetAnchor keeps displayed weekly/monthly resets aligned with Normalize.
func (e Lot) windowResetAnchor(start time.Time) time.Time {
	if start.Equal(timezone.StartOfDay(e.StartsAt)) && start.Before(e.StartsAt) {
		return e.StartsAt
	}
	return start
}

// Normalize only mutates a detached snapshot. Persistence is serialized by the
// aggregate subscription lock. Expired lots never receive fresh quota.
func (e *Lot) Normalize(now time.Time, activate bool) {
	if !e.Active(now) {
		return
	}
	if activate && e.DailyWindowStart == nil {
		t := timezone.StartOfDay(now)
		e.DailyWindowStart = &t
	}
	if activate && e.WeeklyWindowStart == nil {
		t := now
		e.WeeklyWindowStart = &t
	}
	if activate && e.MonthlyWindowStart == nil {
		t := now
		e.MonthlyWindowStart = &t
	}
	if e.DailyWindowStart != nil && e.ExpiresAt.After(e.StartsAt.AddDate(0, 0, 1)) {
		today := timezone.StartOfDay(now)
		if today.After(timezone.StartOfDay(*e.DailyWindowStart)) {
			e.DailyWindowStart = &today
			e.DailyUsageUSD = 0
		}
	}
	advance := func(start **time.Time, used *float64, duration time.Duration) {
		if *start == nil {
			return
		}
		anchor := e.windowResetAnchor(**start)
		next := anchor.Add(duration)
		if now.Before(next) || !next.Before(e.ExpiresAt) {
			return
		}
		periods := now.Sub(anchor) / duration
		maxPeriods := (e.ExpiresAt.Sub(anchor) - 1) / duration
		if periods > maxPeriods {
			periods = maxPeriods
		}
		t := anchor.Add(periods * duration)
		*start = &t
		*used = 0
	}
	advance(&e.WeeklyWindowStart, &e.WeeklyUsageUSD, 7*24*time.Hour)
	advance(&e.MonthlyWindowStart, &e.MonthlyUsageUSD, 30*24*time.Hour)
}

func Aggregate(lots []Lot, now time.Time) Summary {
	s := Summary{}
	sums := [3]float64{}
	unlimited := [3]bool{}
	for _, e := range lots {
		if !e.Active(now) {
			continue
		}
		e.Normalize(now, false)
		s.ActiveLotCount++
		limits := []*float64{e.DailyLimitUSD, e.WeeklyLimitUSD, e.MonthlyLimitUSD}
		for i, v := range limits {
			if !finite(v) {
				unlimited[i] = true
			} else {
				sums[i] = add(sums[i], *v)
			}
		}
		s.DailyUsageUSD = add(s.DailyUsageUSD, e.DailyUsageUSD)
		s.WeeklyUsageUSD = add(s.WeeklyUsageUSD, e.WeeklyUsageUSD)
		s.MonthlyUsageUSD = add(s.MonthlyUsageUSD, e.MonthlyUsageUSD)
		s.AvailableUSD += e.Capacity()
		earlier(&s.NextExpiryAt, e.ExpiresAt)
		if s.ExpiresAt == nil || e.ExpiresAt.After(*s.ExpiresAt) {
			t := e.ExpiresAt
			s.ExpiresAt = &t
		}
		if e.DailyWindowStart != nil {
			t := timezone.StartOfDay(*e.DailyWindowStart).AddDate(0, 0, 1)
			if !e.ExpiresAt.After(e.StartsAt.AddDate(0, 0, 1)) || t.After(e.ExpiresAt) {
				t = e.ExpiresAt
			}
			earlier(&s.DailyResetAt, t)
		}
		if e.WeeklyWindowStart != nil {
			t := e.windowResetAnchor(*e.WeeklyWindowStart).Add(7 * 24 * time.Hour)
			if t.After(e.ExpiresAt) {
				t = e.ExpiresAt
			}
			earlier(&s.WeeklyResetAt, t)
		}
		if e.MonthlyWindowStart != nil {
			t := e.windowResetAnchor(*e.MonthlyWindowStart).Add(30 * 24 * time.Hour)
			if t.After(e.ExpiresAt) {
				t = e.ExpiresAt
			}
			earlier(&s.MonthlyResetAt, t)
		}
	}
	if !unlimited[0] {
		s.DailyLimitUSD = &sums[0]
	}
	if !unlimited[1] {
		s.WeeklyLimitUSD = &sums[1]
	}
	if !unlimited[2] {
		s.MonthlyLimitUSD = &sums[2]
	}
	return s
}

func SortLots(lots []Lot) {
	sort.SliceStable(lots, func(i, j int) bool {
		if lots[i].ExpiresAt.Equal(lots[j].ExpiresAt) {
			return lots[i].ID < lots[j].ID
		}
		return lots[i].ExpiresAt.Before(lots[j].ExpiresAt)
	})
}

// Allocate records completed usage, including any overrun from an already
// admitted request. Rejecting post-response usage would silently lose billing.
// New requests are checked against Aggregate.AvailableUSD before forwarding.
func Allocate(lots []Lot, cost float64, now time.Time) ([]Lot, error) {
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
		return nil, errors.New("invalid subscription cost")
	}
	out := append([]Lot(nil), lots...)
	SortLots(out)
	remaining := cost
	last := -1
	for i := range out {
		e := &out[i]
		if !e.Active(now) {
			continue
		}
		e.Normalize(now, true)
		last = i
		portion := math.Min(remaining, e.Capacity())
		if portion <= 0 {
			continue
		}
		charge(e, portion)
		remaining = math.Max(0, add(remaining, -portion))
	}
	if remaining > 0 {
		if last < 0 {
			return nil, errors.New("no chargeable subscription entitlement")
		}
		charge(&out[last], remaining)
	}
	return out, nil
}
func charge(e *Lot, v float64) {
	e.DailyUsageUSD = add(e.DailyUsageUSD, v)
	e.WeeklyUsageUSD = add(e.WeeklyUsageUSD, v)
	e.MonthlyUsageUSD = add(e.MonthlyUsageUSD, v)
	e.LifetimeUsageUSD = add(e.LifetimeUsageUSD, v)
}

// SQL works with both ent transaction clients and database/sql transactions.
type SQL interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func LoadLots(ctx context.Context, q SQL, id int64, lock bool) ([]Lot, error) {
	query := `SELECT id,user_subscription_id,plan_id,source_order_id,lot_index,purchase_mode,status,starts_at,expires_at,daily_limit_usd,weekly_limit_usd,monthly_limit_usd,daily_window_start,weekly_window_start,monthly_window_start,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,lifetime_usage_usd,refunded_at,created_at FROM user_subscription_entitlements WHERE user_subscription_id=$1 ORDER BY expires_at,id`
	if lock {
		query += " FOR UPDATE"
	}
	rows, err := q.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []Lot
	for rows.Next() {
		var e Lot
		if err := rows.Scan(&e.ID, &e.UserSubscriptionID, &e.PlanID, &e.SourceOrderID, &e.LotIndex, &e.PurchaseMode, &e.Status, &e.StartsAt, &e.ExpiresAt, &e.DailyLimitUSD, &e.WeeklyLimitUSD, &e.MonthlyLimitUSD, &e.DailyWindowStart, &e.WeeklyWindowStart, &e.MonthlyWindowStart, &e.DailyUsageUSD, &e.WeeklyUsageUSD, &e.MonthlyUsageUSD, &e.LifetimeUsageUSD, &e.RefundedAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
func LockSubscription(ctx context.Context, q SQL, id int64) error {
	rows, err := q.QueryContext(ctx, `SELECT id FROM user_subscriptions WHERE id=$1 FOR UPDATE`, id)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if rows.Err() != nil {
			return rows.Err()
		}
		return sql.ErrNoRows
	}
	return nil
}
func SaveLots(ctx context.Context, q SQL, lots []Lot) error {
	for _, e := range lots {
		_, err := q.ExecContext(ctx, `UPDATE user_subscription_entitlements SET daily_window_start=$2,weekly_window_start=$3,monthly_window_start=$4,daily_usage_usd=$5,weekly_usage_usd=$6,monthly_usage_usd=$7,lifetime_usage_usd=$8,updated_at=NOW() WHERE id=$1`, e.ID, e.DailyWindowStart, e.WeeklyWindowStart, e.MonthlyWindowStart, e.DailyUsageUSD, e.WeeklyUsageUSD, e.MonthlyUsageUSD, e.LifetimeUsageUSD)
		if err != nil {
			return err
		}
	}
	return nil
}
func SyncAggregate(ctx context.Context, q SQL, id int64, lots []Lot, now time.Time) error {
	if len(lots) == 0 {
		return nil
	}
	s, latest, status := ParentProjection(lots, now)
	_, err := q.ExecContext(ctx, `UPDATE user_subscriptions SET expires_at=$2,status=CASE WHEN status IN ('suspended','revoked') THEN status ELSE $3 END,daily_usage_usd=$4,weekly_usage_usd=$5,monthly_usage_usd=$6,updated_at=NOW() WHERE id=$1`, id, latest, status, s.DailyUsageUSD, s.WeeklyUsageUSD, s.MonthlyUsageUSD)
	return err
}
func Debit(ctx context.Context, q SQL, id int64, cost float64, now time.Time) (bool, error) {
	if err := LockSubscription(ctx, q, id); err != nil {
		return false, err
	}
	lots, err := LoadLots(ctx, q, id, true)
	if err != nil {
		return false, err
	}
	if len(lots) == 0 {
		return false, nil
	}
	lots, err = Allocate(lots, cost, now)
	if err != nil {
		return true, err
	}
	if err = SaveLots(ctx, q, lots); err != nil {
		return true, err
	}
	return true, SyncAggregate(ctx, q, id, lots, now)
}

// ParentProjection is shared by SQL billing and Ent management persistence.
func ParentProjection(lots []Lot, now time.Time) (Summary, time.Time, string) {
	a := Aggregate(lots, now)
	latest := time.Time{}
	for _, e := range lots {
		if e.Status != "refunded" && e.Status != "revoked" && e.ExpiresAt.After(latest) {
			latest = e.ExpiresAt
		}
	}
	if a.ExpiresAt != nil {
		latest = *a.ExpiresAt
	}
	if latest.IsZero() {
		latest = now
	}
	state := "expired"
	if a.ActiveLotCount > 0 {
		state = "active"
	}
	return a, latest, state
}
