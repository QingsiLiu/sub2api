package subscription

import (
	"errors"
	"math"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

const (
	ContractModeV2     = "v2"
	ContractModeLegacy = "legacy_daily"
	ContractKindWeek   = "week"
	ContractKindMonth  = "month"
)

var (
	ErrAdmissionRequired     = infraerrors.Conflict("SUBSCRIPTION_ADMISSION_REQUIRED", "a subscription billing request must retain its server-owned admission identity")
	ErrContractCompatibility = infraerrors.Conflict("SUBSCRIPTION_COMPATIBILITY_MODE", "historical subscription rights must expire before buying a new contract")
	ErrContractType          = infraerrors.BadRequest("SUBSCRIPTION_TYPE_MISMATCH", "an active subscription can only change within its current week or month type")
	ErrContractTier          = infraerrors.BadRequest("SUBSCRIPTION_TIER_MISMATCH", "stack and renew require the current tier; upgrade requires a higher tier")
	ErrContractOperation     = infraerrors.BadRequest("INVALID_SUBSCRIPTION_OPERATION", "choose purchase, stack, renew or upgrade")
	ErrContractRefund        = infraerrors.Conflict("SUBSCRIPTION_REFUND_MANUAL_REVIEW", "subscription refund requires manual review because the contract has been used or changed")
)

// Contract is the current entitlement identity, independent of sale-plan edits.
// A natural expiry followed by purchase creates a new TermID, while changes made
// during an active term keep both SubscriptionID and TermID stable.
type Contract struct {
	SubscriptionID int64     `json:"subscription_id"`
	UserID         int64     `json:"user_id"`
	TermID         string    `json:"term_id"`
	Revision       int64     `json:"revision"`
	Mode           string    `json:"mode"`
	Kind           string    `json:"kind"`
	PlanID         int64     `json:"plan_id"`
	PlanName       string    `json:"plan_name"`
	UnitDailyUSD   float64   `json:"unit_daily_usd"`
	Quantity       int       `json:"quantity"`
	PeriodDays     int       `json:"period_days"`
	StartsAt       time.Time `json:"starts_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	Status         string    `json:"status"`
}

type Plan struct {
	ID         int64           `json:"id"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`
	DailyUSD   float64         `json:"daily_usd"`
	PeriodDays int             `json:"period_days"`
	Price      decimal.Decimal `json:"price"`
}

type Change struct {
	Before       *Contract       `json:"before"`
	After        Contract        `json:"after"`
	Operation    string          `json:"operation"`
	Units        int             `json:"units"`
	Periods      int             `json:"periods"`
	BillableDays int             `json:"billable_days"`
	Amount       decimal.Decimal `json:"amount"`
}

var beijing = time.FixedZone("CST", 8*60*60)

// DayStart is deliberately independent of the server's configurable timezone.
func DayStart(now time.Time) time.Time {
	t := now.In(beijing)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, beijing)
}

func RecognizePlan(days int, daily float64) string {
	if days == 7 && (daily == 90 || daily == 180) {
		return ContractKindWeek
	}
	if days == 30 && (daily == 45 || daily == 90 || daily == 180) {
		return ContractKindMonth
	}
	return ""
}

func (c *Contract) Active(now time.Time) bool {
	return c != nil && c.Status == "active" && !c.StartsAt.After(now) && c.ExpiresAt.After(now)
}

func (p Plan) valid() bool {
	return p.ID > 0 && RecognizePlan(p.PeriodDays, p.DailyUSD) == p.Kind && p.Kind != "" && p.Price.IsPositive()
}

func RemainingDays(expiresAt, now time.Time) int {
	remaining := expiresAt.Sub(now)
	if remaining <= 0 {
		return 0
	}
	return int(remaining/(24*time.Hour)) + boolInt(remaining%(24*time.Hour) != 0)
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// PreviewContract uses one consistent sales-price snapshot. Only final totals
// are rounded; a long prepaid expiry is never clamped to one sales period.
func PreviewContract(current *Contract, currentPlan, target Plan, operation string, units, periods int, now time.Time) (Change, error) {
	out := Change{Operation: operation, Units: units, Periods: periods}
	if !target.valid() {
		return out, ErrContractTier
	}
	if operation != "purchase" && operation != "stack" && operation != "renew" && operation != "upgrade" {
		return out, ErrContractOperation
	}
	if current != nil && current.ExpiresAt.After(now) && current.Mode == ContractModeLegacy {
		return out, ErrContractCompatibility
	}
	if current != nil && current.ExpiresAt.After(now) && current.Status != "active" {
		return out, ErrStateConflict
	}
	active := current.Active(now)
	if operation == "purchase" {
		if active {
			return out, ErrStateConflict
		}
		if units < 1 || units > 10 || periods != 0 {
			return out, ErrQuantity
		}
		out.After = Contract{Mode: ContractModeV2, Kind: target.Kind, PlanID: target.ID, PlanName: target.Name, UnitDailyUSD: target.DailyUSD, Quantity: units, PeriodDays: target.PeriodDays, StartsAt: now, ExpiresAt: now.Add(time.Duration(target.PeriodDays) * 24 * time.Hour), Status: "active", Revision: 1}
		out.BillableDays = target.PeriodDays
		out.Amount = target.Price.Mul(decimal.NewFromInt(int64(units))).Round(2)
		return out, nil
	}
	if !active || current.Mode != ContractModeV2 {
		return out, ErrStateConflict
	}
	if current.Kind != target.Kind || current.PeriodDays != target.PeriodDays {
		return out, ErrContractType
	}
	before := *current
	out.Before = &before
	out.After = before
	out.After.Revision++
	out.BillableDays = RemainingDays(current.ExpiresAt, now)
	switch operation {
	case "stack":
		if units < 1 || units > 10 || periods != 0 {
			return out, ErrQuantity
		}
		if target.ID != current.PlanID || target.DailyUSD != current.UnitDailyUSD {
			return out, ErrContractTier
		}
		out.After.Quantity += units
		out.Amount = target.Price.Mul(decimal.NewFromInt(int64(units))).Mul(decimal.NewFromInt(int64(out.BillableDays))).Div(decimal.NewFromInt(int64(current.PeriodDays))).Round(2)
	case "renew":
		if periods < 1 || periods > 10 || units != 0 {
			return out, ErrQuantity
		}
		if target.ID != current.PlanID || target.DailyUSD != current.UnitDailyUSD {
			return out, ErrContractTier
		}
		out.BillableDays = current.PeriodDays * periods
		out.After.ExpiresAt = current.ExpiresAt.Add(time.Duration(out.BillableDays) * 24 * time.Hour)
		out.Amount = target.Price.Mul(decimal.NewFromInt(int64(current.Quantity))).Mul(decimal.NewFromInt(int64(periods))).Round(2)
	case "upgrade":
		if units != 0 || periods != 0 {
			return out, ErrQuantity
		}
		if target.DailyUSD <= current.UnitDailyUSD || !currentPlan.valid() || currentPlan.ID != current.PlanID || currentPlan.Kind != current.Kind || currentPlan.DailyUSD != current.UnitDailyUSD {
			return out, ErrContractTier
		}
		difference := target.Price.Sub(currentPlan.Price)
		if !difference.IsPositive() {
			return out, ErrContractTier
		}
		out.After.PlanID, out.After.PlanName, out.After.UnitDailyUSD = target.ID, target.Name, target.DailyUSD
		out.Amount = difference.Mul(decimal.NewFromInt(int64(current.Quantity))).Mul(decimal.NewFromInt(int64(out.BillableDays))).Div(decimal.NewFromInt(int64(current.PeriodDays))).Round(2)
	}
	if out.After.ExpiresAt.After(MaxExpiry) || out.Amount.IsNegative() {
		return out, ErrStateConflict
	}
	return out, nil
}

// ContractSummary is the only quota projection for a migrated subscription.
// Legacy lots keep their individual expiry; the day ledger keeps all day usage
// even when one of those lots expires part way through the day.
func ContractSummary(c *Contract, lots []Lot, used float64, now time.Time) Summary {
	if c != nil && c.Mode == ContractModeLegacy {
		for _, lot := range lots {
			if lot.Status != "refunded" && lot.Status != "revoked" && !lot.ExpiresAt.After(now) && lot.DailyWindowStart != nil && DayStart(*lot.DailyWindowStart).Equal(DayStart(now)) {
				used = decimal.NewFromFloat(used).Sub(decimal.NewFromFloat(lot.DailyUsageUSD)).Round(10).InexactFloat64()
			}
		}
		used = math.Max(0, used)
	}
	s := Summary{DailyUsageUSD: used}
	if c == nil {
		return s
	}
	if c.Mode == ContractModeV2 {
		if c.Active(now) {
			limit := decimal.NewFromFloat(c.UnitDailyUSD).Mul(decimal.NewFromInt(int64(c.Quantity))).InexactFloat64()
			s.DailyLimitUSD, s.ActiveLotCount = &limit, c.Quantity
			expiry := c.ExpiresAt
			s.ExpiresAt, s.NextExpiryAt = &expiry, &expiry
		}
	} else {
		total := decimal.Zero
		unlimited := false
		for _, lot := range lots {
			if !lot.Active(now) {
				continue
			}
			s.ActiveLotCount++
			if finite(lot.DailyLimitUSD) {
				total = total.Add(decimal.NewFromFloat(*lot.DailyLimitUSD))
			} else {
				unlimited = true
			}
			earlier(&s.NextExpiryAt, lot.ExpiresAt)
			if s.ExpiresAt == nil || lot.ExpiresAt.After(*s.ExpiresAt) {
				expiry := lot.ExpiresAt
				s.ExpiresAt = &expiry
			}
		}
		if !unlimited {
			limit := total.InexactFloat64()
			s.DailyLimitUSD = &limit
		}
	}
	if s.ActiveLotCount == 0 {
		zero := float64(0)
		s.DailyLimitUSD = &zero
		s.RemainingUSD = &zero
		return s
	}
	reset := DayStart(now).Add(24 * time.Hour)
	s.DailyResetAt = &reset
	if s.DailyLimitUSD == nil {
		s.AvailableUSD = math.Inf(1)
	} else {
		remaining := decimal.NewFromFloat(*s.DailyLimitUSD).Sub(decimal.NewFromFloat(used)).Round(10)
		if remaining.IsNegative() {
			remaining = decimal.Zero
		}
		value := remaining.InexactFloat64()
		s.AvailableUSD = value
		s.RemainingUSD = &value
	}
	return s
}

// NormalizeContractLot maintains the historical per-lot audit mirror on the
// contract's fixed Beijing day boundary, including a stack lasting <24 hours.
// Weekly/monthly counters retain their historical windows but never gate V2.
func NormalizeContractLot(lot *Lot, now time.Time, activate bool) {
	if !lot.Active(now) {
		return
	}
	previous, used := lot.DailyWindowStart, lot.DailyUsageUSD
	lot.Normalize(now, activate)
	lot.DailyWindowStart, lot.DailyUsageUSD = previous, used
	day := DayStart(now)
	if previous == nil {
		if activate {
			lot.DailyWindowStart = &day
		}
		return
	}
	if day.After(DayStart(*previous)) {
		lot.DailyWindowStart = &day
		lot.DailyUsageUSD = 0
	}
}

// AllocateContract still records all completed cost, including the permitted
// overrun of requests admitted before quota exhaustion. Allocation among lots
// is audit attribution only; shared day-ledger admission is authoritative.
func AllocateContract(lots []Lot, cost float64, now time.Time) ([]Lot, error) {
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
		return nil, errors.New("invalid subscription cost")
	}
	out := append([]Lot(nil), lots...)
	SortLots(out)
	remaining := cost
	last := -1
	for i := range out {
		lot := &out[i]
		if !lot.Active(now) {
			continue
		}
		NormalizeContractLot(lot, now, true)
		last = i
		portion := math.Min(remaining, minCapacity(lot.DailyLimitUSD, lot.DailyUsageUSD))
		if portion <= 0 {
			continue
		}
		charge(lot, portion)
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
