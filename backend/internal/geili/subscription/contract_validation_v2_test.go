package subscription

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// Independent money oracle: integer cents and rational arithmetic, not decimal.
func oracleCents(price int64, units, days, divisor int) string {
	n := new(big.Int).Mul(big.NewInt(price), big.NewInt(int64(units*days)))
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(n, big.NewInt(int64(divisor)), r)
	if r.Mul(r, big.NewInt(2)).Cmp(big.NewInt(int64(divisor))) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	v := q.Int64()
	return fmt.Sprintf("%d.%02d", v/100, v%100)
}
func TestContractValidationV2PricingMatrix(t *testing.T) {
	specs := []struct {
		days int
		tier float64
	}{{7, 90}, {7, 180}, {30, 45}, {30, 90}, {30, 180}}
	now := time.Date(2028, 2, 28, 15, 59, 59, 999999999, time.UTC)
	checks := 0
	for id, spec := range specs {
		for _, price := range []int64{1, 100, 789, 999, 199999} {
			plan := v2Plan(int64(id+1), spec.days, spec.tier, decimal.New(price, -2).String())
			for units := 1; units <= 10; units++ {
				got, err := PreviewContract(nil, Plan{}, plan, "purchase", units, 0, now)
				require.NoError(t, err)
				require.Equal(t, oracleCents(price, units, 1, 1), got.Amount.StringFixed(2))
				checks++
				require.Equal(t, now.Add(time.Duration(spec.days)*24*time.Hour), got.After.ExpiresAt)
				for days := 1; days <= 365; days++ {
					for _, edge := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
						current := v2Contract(now, plan, 1+(days%10), time.Duration(days)*24*time.Hour+edge)
						previous := *current
						got, err = PreviewContract(current, plan, plan, "stack", units, 0, now)
						if err != nil {
							t.Fatalf("stack plan=%d price=%d units=%d days=%d edge=%d: %v", id, price, units, days, edge, err)
						}
						charged := days
						if edge > 0 {
							charged++
						}
						expected := oracleCents(price, units, charged, spec.days)
						if got.Amount.StringFixed(2) != expected {
							t.Fatalf("price got=%s want=%s plan=%d price=%d units=%d days=%d edge=%d", got.Amount, expected, id, price, units, days, edge)
						}
						if got.BillableDays != charged || got.After.Quantity != previous.Quantity+units || !got.After.ExpiresAt.Equal(previous.ExpiresAt) || got.After.TermID != previous.TermID || got.After.Revision != previous.Revision+1 || *current != previous {
							t.Fatalf("stack identity changed %+v", got)
						}
						checks++
					}
				}
				for periods := 1; periods <= 10; periods++ {
					current := v2Contract(now, plan, units, 365*24*time.Hour)
					got, err = PreviewContract(current, plan, plan, "renew", 0, periods, now)
					require.NoError(t, err)
					require.Equal(t, oracleCents(price, units, periods, 1), got.Amount.StringFixed(2))
					require.Equal(t, current.ExpiresAt.Add(time.Duration(spec.days*periods)*24*time.Hour), got.After.ExpiresAt)
					require.Equal(t, units, got.After.Quantity)
					checks++
				}
			}
		}
	}
	require.Equal(t, 276500, checks)
	t.Logf("%d independent rational-money and entitlement identity checks", checks)
}
func TestContractValidationV2UpgradeMatrix(t *testing.T) {
	now := time.Date(2026, 9, 21, 23, 59, 59, 0, beijing)
	checks := 0
	for _, pair := range [][3]int{{7, 90, 180}, {30, 45, 90}, {30, 45, 180}, {30, 90, 180}} {
		for _, prices := range [][2]int64{{1, 2}, {789, 1234}, {10001, 20003}} {
			from := v2Plan(1, pair[0], float64(pair[1]), decimal.New(prices[0], -2).String())
			to := v2Plan(2, pair[0], float64(pair[2]), decimal.New(prices[1], -2).String())
			for units := 1; units <= 10; units++ {
				for days := 1; days <= 365; days++ {
					current := v2Contract(now, from, units, time.Duration(days)*24*time.Hour)
					got, err := PreviewContract(current, from, to, "upgrade", 0, 0, now)
					require.NoError(t, err)
					expected := oracleCents(prices[1]-prices[0], units, days, pair[0])
					if got.Amount.StringFixed(2) != expected || got.After.Quantity != units || got.After.UnitDailyUSD != to.DailyUSD || !got.After.ExpiresAt.Equal(current.ExpiresAt) || got.After.SubscriptionID != current.SubscriptionID || got.After.TermID != current.TermID {
						t.Fatalf("invalid upgrade %+v; expected amount %s", got, expected)
					}
					checks++
				}
			}
		}
	}
	require.Equal(t, 43800, checks)
	t.Logf("%d whole-contract upgrade checks", checks)
}
func TestContractValidationV2TierOperationMatrix(t *testing.T) {
	now := time.Now()
	plans := []Plan{v2Plan(1, 7, 90, "10"), v2Plan(2, 7, 180, "20"), v2Plan(3, 30, 45, "30"), v2Plan(4, 30, 90, "60"), v2Plan(5, 30, 180, "120")}
	checks := 0
	for _, from := range plans {
		for _, to := range plans {
			for _, op := range []string{"purchase", "stack", "renew", "upgrade"} {
				units, periods := 0, 0
				if op == "purchase" || op == "stack" {
					units = 1
				}
				if op == "renew" {
					periods = 1
				}
				_, err := PreviewContract(v2Contract(now, from, 2, 3*24*time.Hour), from, to, op, units, periods, now)
				allowed := op != "purchase" && from.Kind == to.Kind && ((op == "upgrade" && to.DailyUSD > from.DailyUSD) || ((op == "stack" || op == "renew") && from.ID == to.ID))
				if allowed && err != nil || !allowed && err == nil {
					t.Fatalf("%s tier%d->%d allowed=%v err=%v", op, from.ID, to.ID, allowed, err)
				}
				checks++
			}
		}
	}
	require.Equal(t, 100, checks)
	for _, value := range []int{-100, -1, 0, 11, 100} {
		for _, op := range []string{"purchase", "stack", "renew"} {
			c := v2Contract(now, plans[0], 2, 24*time.Hour)
			units, periods := value, 0
			if op == "purchase" {
				c = nil
			}
			if op == "renew" {
				units, periods = 0, value
			}
			_, err := PreviewContract(c, plans[0], plans[0], op, units, periods, now)
			require.ErrorIs(t, err, ErrQuantity)
		}
	}
}
func TestContractValidationV2SnapshotAndClockBoundaries(t *testing.T) {
	for _, now := range []time.Time{time.Date(2026, 9, 21, 15, 59, 59, 999999999, time.UTC), time.Date(2026, 9, 21, 16, 0, 0, 0, time.UTC), time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)} {
		for _, offset := range []int{-12, -8, 0, 8, 14} {
			shown := now.In(time.FixedZone("test", offset*3600))
			require.True(t, DayStart(shown).Equal(DayStart(now)))
			require.Equal(t, 24*time.Hour, DayStart(shown).Add(24*time.Hour).Sub(DayStart(shown)))
			for _, d := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond, 24 * time.Hour, 24*time.Hour + time.Nanosecond} {
				expected := 0
				if d > 0 {
					expected = 1
				}
				if d > 24*time.Hour {
					expected = 2
				}
				require.Equal(t, expected, RemainingDays(shown.Add(d), shown))
			}
		}
	}
	now := time.Now()
	p := v2Plan(1, 30, 45, "10")
	c := v2Contract(now, p, 2, 3*24*time.Hour)
	changed := p
	changed.Price = decimal.RequireFromString("20")
	got, err := PreviewContract(c, changed, changed, "stack", 1, 0, now)
	require.NoError(t, err)
	require.Equal(t, "2.00", got.Amount.StringFixed(2))
	require.Equal(t, 45.0, got.After.UnitDailyUSD)
	changed.DailyUSD = 90
	_, err = PreviewContract(c, changed, changed, "stack", 1, 0, now)
	require.ErrorIs(t, err, ErrContractTier)
	require.Equal(t, 45.0, c.UnitDailyUSD)
}
func TestContractValidationV2DailyAllocationConservesCost(t *testing.T) {
	now := DayStart(time.Now()).Add(10 * time.Hour)
	day := DayStart(now)
	limit := 45.0
	weekly := 1.0
	for count := 1; count <= 10; count++ {
		for _, spent := range []float64{0, 44.9999999999, 45, 45.0000000001, 100} {
			for _, cost := range []float64{0, .0000000001, .01, 1, 45, 450, 9000.123456789} {
				lots := make([]Lot, count)
				for i := range lots {
					lots[i] = Lot{ID: int64(count - i), Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Duration(i+1) * time.Hour), DailyWindowStart: &day, DailyLimitUSD: &limit, WeeklyLimitUSD: &weekly, DailyUsageUSD: spent, WeeklyUsageUSD: 100, MonthlyUsageUSD: 101, LifetimeUsageUSD: 200}
				}
				output, err := AllocateContract(lots, cost, now)
				require.NoError(t, err)
				recorded := decimal.Zero
				for _, lot := range output {
					recorded = recorded.Add(decimal.NewFromFloat(lot.LifetimeUsageUSD).Sub(decimal.NewFromInt(200)))
					require.GreaterOrEqual(t, lot.WeeklyUsageUSD, 100.0)
				}
				require.True(t, decimal.NewFromFloat(cost).Sub(recorded).Abs().LessThanOrEqual(decimal.New(1, -9)), "count=%d spent=%f cost=%f recorded=%s", count, spent, cost, recorded)
				for _, lot := range lots {
					require.Equal(t, spent, lot.DailyUsageUSD)
					require.Equal(t, 200.0, lot.LifetimeUsageUSD)
				}
			}
		}
	}
	for _, cost := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := AllocateContract(nil, cost, now)
		require.Error(t, err)
	}
}
func TestContractValidationV2LegacySummaryMatrix(t *testing.T) {
	now := DayStart(time.Now()).Add(10 * time.Hour)
	day := DayStart(now)
	yesterday := day.Add(-24 * time.Hour)
	limit := 45.0
	zero := 0.0
	cases := []struct {
		name, status  string
		start, expiry time.Time
		limit         *float64
		window        *time.Time
		used          float64
		active        bool
	}{
		{"active", "active", now.Add(-time.Hour), now.Add(time.Hour), &limit, &day, 4, true},
		{"starts_equal", "active", now, now.Add(time.Hour), &limit, &day, 4, true},
		{"future", "active", now.Add(time.Nanosecond), now.Add(time.Hour), &limit, &day, 0, false},
		{"expiry_equal", "active", now.Add(-time.Hour), now, &limit, &day, 4, false},
		{"refunded", "refunded", now.Add(-time.Hour), now.Add(time.Hour), &limit, &day, 0, false},
		{"revoked", "revoked", now.Add(-time.Hour), now.Add(time.Hour), &limit, &day, 0, false},
		{"pending", "refund_pending", now.Add(-time.Hour), now.Add(time.Hour), &limit, &day, 0, false},
		{"suspended", "suspended", now.Add(-time.Hour), now.Add(time.Hour), &limit, &day, 0, false},
		{"yesterday", "active", now.Add(-48 * time.Hour), now.Add(time.Hour), &limit, &yesterday, 4, true},
		{"unlimited_nil", "active", now.Add(-time.Hour), now.Add(time.Hour), nil, &day, 4, true},
		{"unlimited_zero", "active", now.Add(-time.Hour), now.Add(time.Hour), &zero, &day, 4, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lot := Lot{ID: 1, Status: tc.status, StartsAt: tc.start, ExpiresAt: tc.expiry, DailyLimitUSD: tc.limit, DailyWindowStart: tc.window, DailyUsageUSD: tc.used}
			used := 0.0
			if tc.window.Equal(day) {
				used = tc.used
			}
			got := ContractSummary(&Contract{Mode: ContractModeLegacy}, []Lot{lot}, used, now)
			require.Equal(t, boolInt(tc.active), got.ActiveLotCount)
			if !tc.active {
				require.Zero(t, got.AvailableUSD)
				return
			}
			if tc.limit == nil || *tc.limit == 0 {
				require.Nil(t, got.RemainingUSD)
				require.True(t, math.IsInf(got.AvailableUSD, 1))
			} else {
				require.Equal(t, 45-used, *got.RemainingUSD)
			}
			require.Nil(t, got.WeeklyLimitUSD)
			require.Nil(t, got.MonthlyLimitUSD)
		})
	}
}
func TestContractValidationV2MigrationMustKeepFutureRights(t *testing.T) {
	c, _ := v2Store(t)
	ctx := context.Background()
	now := time.Now()
	s, p := v2Parent(t, c, 45, 30, now)
	v2LegacyLot(t, c, s, p, 1, now)
	require.NoError(t, c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(s.ID).SetPlanID(p.ID).SetStartsAt(now.Add(time.Hour)).SetExpiresAt(s.ExpiresAt.Add(time.Hour)).SetDailyLimitUsd(45).Exec(ctx))
	contract, err := EnsureContract(ctx, c, s.ID, now)
	require.NoError(t, err)
	require.Equal(t, ContractModeLegacy, contract.Mode, "future purchased rights cannot disappear from a single-unit V2 contract")
	lots, err := ReadLots(ctx, c, s.ID)
	require.NoError(t, err)
	before := ContractSummary(contract, lots, 1, now)
	after := ContractSummary(contract, lots, 1, now.Add(2*time.Hour))
	require.Equal(t, 45.0, *before.DailyLimitUSD)
	require.Equal(t, 90.0, *after.DailyLimitUSD)
}
func TestContractValidationV2FutureContractBlocksAnotherPurchase(t *testing.T) {
	now := time.Now()
	plan := v2Plan(1, 7, 90, "10")
	c := v2Contract(now, plan, 1, 7*24*time.Hour)
	c.StartsAt = now.Add(time.Hour)
	_, err := PreviewContract(c, plan, plan, "purchase", 1, 0, now)
	require.ErrorIs(t, err, ErrStateConflict)
}
func TestContractValidationV2RuntimeMigrationClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*dbent.UserSubscriptionUpdateOne, *dbent.SubscriptionPlanUpdateOne)
		want string
	}{
		{"ordinary", func(s *dbent.UserSubscriptionUpdateOne, p *dbent.SubscriptionPlanUpdateOne) {}, ContractModeV2},
		{"suspended", func(s *dbent.UserSubscriptionUpdateOne, p *dbent.SubscriptionPlanUpdateOne) { s.SetStatus("suspended") }, ContractModeLegacy},
		{"revoked", func(s *dbent.UserSubscriptionUpdateOne, p *dbent.SubscriptionPlanUpdateOne) { s.SetStatus("revoked") }, ContractModeLegacy},
		{"free_special", func(s *dbent.UserSubscriptionUpdateOne, p *dbent.SubscriptionPlanUpdateOne) { p.SetPrice(0) }, ContractModeLegacy},
		{"marked_legacy", func(s *dbent.UserSubscriptionUpdateOne, p *dbent.SubscriptionPlanUpdateOne) {
			p.SetIsLegacyCompat(true)
		}, ContractModeLegacy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := v2Store(t)
			ctx := context.Background()
			now := time.Now()
			s, p := v2Parent(t, c, 45, 30, now)
			v2LegacyLot(t, c, s, p, 1, now)
			su, pu := c.UserSubscription.UpdateOneID(s.ID), c.SubscriptionPlan.UpdateOneID(p.ID)
			tc.edit(su, pu)
			require.NoError(t, su.Exec(ctx))
			require.NoError(t, pu.Exec(ctx))
			contract, err := EnsureContract(ctx, c, s.ID, now)
			require.NoError(t, err)
			require.Equal(t, tc.want, contract.Mode)
			first := *contract
			again, err := EnsureContract(ctx, c, s.ID, now)
			require.NoError(t, err)
			require.Equal(t, first, *again)
			used, err := ReadDailyUsage(ctx, c, s.ID, contract.TermID, now)
			require.NoError(t, err)
			require.Equal(t, 1.0, used)
		})
	}
}

func TestContractValidationV2LegacyRetiredUsageNeverCrossesTerms(t *testing.T) {
	now := DayStart(time.Now()).Add(15 * time.Hour)
	day := DayStart(now)
	limit := 45.0
	old := Lot{Status: "active", StartsAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-2 * time.Hour), DailyLimitUSD: &limit, DailyWindowStart: &day, DailyUsageUSD: 20}
	current := Lot{Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(30 * 24 * time.Hour), DailyLimitUSD: &limit, DailyWindowStart: &day, DailyUsageUSD: 5}
	contract := &Contract{Mode: ContractModeLegacy, StartsAt: current.StartsAt}
	got := ContractSummary(contract, []Lot{old, current}, 5, now)
	require.Equal(t, 5.0, got.DailyUsageUSD, "a previous term's expired usage is absent from the current ledger and cannot be subtracted again")
	require.Equal(t, 40.0, *got.RemainingUSD)
}

func TestContractValidationV2MigrationDoesNotReinterpretPaidDuration(t *testing.T) {
	c, _ := v2Store(t)
	ctx := context.Background()
	now := time.Now()
	s, p := v2Parent(t, c, 90, 30, now)
	order := v2Order(t, c, s)
	require.NoError(t, c.PaymentOrder.UpdateOneID(order.ID).SetSubscriptionDays(30).Exec(ctx))
	lot := v2LegacyLot(t, c, s, p, 1, now)
	require.NoError(t, c.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetSourceOrderID(order.ID).SetSourceType("payment").Exec(ctx))
	require.NoError(t, c.SubscriptionPlan.UpdateOneID(p.ID).SetValidityDays(7).Exec(ctx))
	contract, err := EnsureContract(ctx, c, s.ID, now)
	require.NoError(t, err)
	require.Equal(t, ContractModeLegacy, contract.Mode, "a paid monthly order must not turn into a weekly product after catalog edits")
	require.True(t, s.ExpiresAt.Equal(contract.ExpiresAt))
	require.Equal(t, 1.0, mustReadValidationDaily(t, c, contract, now))
}
func mustReadValidationDaily(t *testing.T, c *dbent.Client, contract *Contract, now time.Time) float64 {
	t.Helper()
	used, err := ReadDailyUsage(context.Background(), c, contract.SubscriptionID, contract.TermID, now)
	require.NoError(t, err)
	return used
}

func TestContractValidationV2PurchaseRejectsBeyondSupportedExpiry(t *testing.T) {
	now := MaxExpiry.Add(-6 * 24 * time.Hour)
	plan := v2Plan(1, 7, 90, "10")
	_, err := PreviewContract(nil, Plan{}, plan, "purchase", 1, 0, now)
	require.ErrorIs(t, err, ErrStateConflict, "invalid expiry must fail before a payable quote exists")
}

func TestContractValidationV2AcceptedSnapshotSurvivesCatalogEdits(t *testing.T) {
	c, _ := v2Store(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	s, p := v2Parent(t, c, 45, 30, now)
	v2LegacyLot(t, c, s, p, 7, now)
	contract, err := EnsureContract(ctx, c, s.ID, now)
	require.NoError(t, err)
	plan := PlanFromEntity(p)
	change, err := PreviewContract(contract, plan, plan, "stack", 2, 0, now)
	require.NoError(t, err)
	quoted := change.Amount
	require.NoError(t, c.SubscriptionPlan.UpdateOneID(p.ID).SetPrice(999).SetDailyLimitUsd(180).SetValidityDays(7).Exec(ctx))
	result, err := ApplyContractChange(ctx, c, change, v2Order(t, c, s).ID, "payment", "locked quote", 0, now)
	require.NoError(t, err)
	require.Equal(t, 45.0, result.UnitDailyUSD)
	require.Equal(t, 30, result.PeriodDays)
	require.Equal(t, 3, result.Quantity)
	require.True(t, result.ExpiresAt.Equal(contract.ExpiresAt))
	require.Equal(t, quoted, change.Amount)
	lots, err := ReadLots(ctx, c, s.ID)
	require.NoError(t, err)
	for _, lot := range lots {
		require.Equal(t, 45.0, *lot.DailyLimitUSD)
	}
	require.Equal(t, 7.0, mustReadValidationDaily(t, c, result, now))
}

func TestContractValidationV2FailedChangeRollsBackAllRights(t *testing.T) {
	c, db := v2Store(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	s, p := v2Parent(t, c, 45, 30, now)
	v2LegacyLot(t, c, s, p, 7, now)
	contract, err := EnsureContract(ctx, c, s.ID, now)
	require.NoError(t, err)
	plan := PlanFromEntity(p)
	beforeLots, err := ReadLots(ctx, c, s.ID)
	require.NoError(t, err)
	change, err := PreviewContract(contract, plan, plan, "stack", 2, 0, now)
	require.NoError(t, err)
	order := v2Order(t, c, s)
	_, err = db.Exec(`CREATE TRIGGER fail_contract_change BEFORE INSERT ON subscription_contract_changes BEGIN SELECT RAISE(ABORT,'synthetic final journal failure'); END`)
	require.NoError(t, err)
	tx, err := c.Tx(ctx)
	require.NoError(t, err)
	tc := dbent.NewTxContext(ctx, tx)
	_, err = ApplyContractChange(tc, tx.Client(), change, order.ID, "payment", "rollback proof", 0, now)
	require.ErrorContains(t, err, "synthetic final journal failure")
	require.NoError(t, tx.Rollback())
	after, err := LoadContract(ctx, c, s.ID)
	require.NoError(t, err)
	require.Equal(t, *contract, *after)
	afterLots, err := ReadLots(ctx, c, s.ID)
	require.NoError(t, err)
	require.Equal(t, beforeLots, afterLots)
	require.Equal(t, 7.0, mustReadValidationDaily(t, c, after, now))
	var changes int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM subscription_contract_changes`).Scan(&changes))
	require.Zero(t, changes)
}
