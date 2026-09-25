//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type billingStressFixtureGeili struct {
	kind                     string
	userID, keyID, accountID int64
	groupID                  *int64
	sub                      *entitlementFixture
	giftID                   int64
	units, requests          atomic.Int64
}

type billingStressReportGeili struct {
	RunID                                                          string `json:"run_id"`
	StartedAt, FinishedAt                                          time.Time
	ConfiguredDuration                                             string  `json:"configured_duration"`
	IngestionSeconds                                               float64 `json:"ingestion_seconds"`
	MeasuredProductionPeakRPM                                      int     `json:"measured_production_peak_rpm"`
	ScheduledRPM                                                   int     `json:"scheduled_rpm"`
	AchievedRPM                                                    float64 `json:"achieved_rpm"`
	Workers                                                        int     `json:"workers"`
	PeakConcurrentWorkers                                          int64   `json:"peak_concurrent_workers"`
	UniqueRequests, DuplicateAttempts, Delivered, DeliveryFailures int64
	PendingPeak, FinancialChecks                                   int64
	FinalDrainSeconds                                              float64        `json:"final_drain_seconds"`
	LockRecoverySeconds                                            []float64      `json:"lock_recovery_seconds"`
	Faults                                                         []string       `json:"faults"`
	Errors                                                         []string       `json:"errors"`
	Checks                                                         map[string]any `json:"checks"`
	Passed                                                         bool           `json:"passed"`
}

// TestBillingReliabilityMixedStressGeili is an opt-in, real PostgreSQL experiment.
// The integration harness creates a disposable local database and applies every
// current migration; no upstream request or production connection is involved.
func TestBillingReliabilityMixedStressGeili(t *testing.T) {
	if os.Getenv("GEILI_BILLING_STRESS") != "1" {
		t.Skip("opt-in stress: set GEILI_BILLING_STRESS=1")
	}
	duration := 30 * time.Minute
	if value := os.Getenv("GEILI_BILLING_STRESS_DURATION"); value != "" {
		var err error
		duration, err = time.ParseDuration(value)
		require.NoError(t, err)
	}
	require.GreaterOrEqual(t, duration, 45*time.Second, "allow an actual delivery timeout and recovery")
	rpm := 660 // Headroom above twice the observed 315/minute peak.
	if value := os.Getenv("GEILI_BILLING_STRESS_RPM"); value != "" {
		var err error
		rpm, err = strconv.Atoi(value)
		require.NoError(t, err)
	}
	require.GreaterOrEqual(t, rpm, 630)
	const workers = 128
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	integrationDB.SetMaxOpenConns(64)
	integrationDB.SetMaxIdleConns(64)
	report := billingStressReportGeili{RunID: uuid.NewString(), ConfiguredDuration: duration.String(), MeasuredProductionPeakRPM: 315, ScheduledRPM: rpm, Workers: workers, Checks: map[string]any{}}
	var reportMu sync.Mutex
	recordError := func(err error) {
		if err == nil {
			return
		}
		reportMu.Lock()
		if len(report.Errors) < 100 {
			report.Errors = append(report.Errors, err.Error())
		}
		reportMu.Unlock()
	}
	defer func() {
		report.FinishedAt = time.Now().UTC()
		report.Passed = !t.Failed() && len(report.Errors) == 0
		bytes, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Errorf("marshal stress report: %v", err)
			return
		}
		if output := os.Getenv("GEILI_BILLING_STRESS_REPORT"); output != "" {
			if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
				t.Errorf("report directory: %v", err)
				return
			}
			if err := os.WriteFile(output, append(bytes, '\n'), 0600); err != nil {
				t.Errorf("write report: %v", err)
			}
		}
		t.Logf("BILLING_STRESS_REPORT %s", bytes)
	}()

	// Four balance, four paid V2, and four paid+gift subscriptions. Half of each
	// category's traffic uses a single hot account; the rest spans three users.
	fixtures := make([]*billingStressFixtureGeili, 0, 12)
	for i := 0; i < 4; i++ {
		_, cmd, _ := settlementFixture(t)
		fixtures = append(fixtures, &billingStressFixtureGeili{kind: "balance", userID: cmd.UserID, keyID: cmd.APIKeyID, accountID: cmd.AccountID})
	}
	for i := 0; i < 8; i++ {
		f, _ := v2StandardFixture(t)
		item := &billingStressFixtureGeili{kind: "paid", userID: f.user.ID, keyID: f.key.ID, accountID: f.account.ID, groupID: f.key.GroupID, sub: &f}
		if i >= 4 {
			item.kind = "gift"
			now := time.Now().Truncate(time.Microsecond)
			gift, err := f.c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(f.sub.ID).SetSourceType("campaign").SetStatus("active").SetStartsAt(now.Add(-time.Hour)).SetExpiresAt(now.Add(7 * 24 * time.Hour)).SetDailyLimitUsd(45).SetDailyWindowStart(geilisub.DayStart(now)).Save(ctx)
			require.NoError(t, err)
			item.giftID = gift.ID
			require.NoError(t, geilisub.RefreshParent(ctx, f.c, f.sub.ID, now))
			f.sub, err = NewUserSubscriptionRepository(f.c).GetByID(ctx, f.sub.ID)
			require.NoError(t, err)
		}
		fixtures = append(fixtures, item)
		keyID := f.key.ID
		t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM usage_settlement_receipts WHERE api_key_id=$1`, keyID) })
	}
	// Workers only see these generated fixtures. Observations use the same SQL
	// snapshot for financial rows and receipts, so in-flight commits cannot create
	// false mismatches during the experiment.
	fixtureFor := func(seq int64) *billingStressFixtureGeili {
		kind := int(seq % 3)
		slot := 0
		if (seq/3)%2 == 1 {
			slot = 1 + int((seq/6)%3)
		}
		return fixtures[kind*4+slot]
	}
	var active, peak, unique, duplicates, delivered, deliveryFailures, pendingPeak, financialChecks atomic.Int64
	increasePeak := func(target *atomic.Int64, value int64) {
		for old := target.Load(); value > old; old = target.Load() {
			if target.CompareAndSwap(old, value) {
				break
			}
		}
	}
	var bg sync.WaitGroup
	var fatalErrors atomic.Int64
	startLoop := func(fn func()) { bg.Add(1); go func() { defer bg.Done(); fn() }() }
	for replica := 0; replica < 2; replica++ {
		replica := replica
		startLoop(func() {
			repository := &usageBillingRepository{db: integrationDB}
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				// Recreate each replica from persisted state every 1000 operations; this
				// exercises lack of in-memory ownership, without claiming an OS restart.
				if unique.Load() > 0 && unique.Load()%1000 == 0 {
					repository = &usageBillingRepository{db: integrationDB}
				}
				_, err := repository.ProcessPendingSettlements(ctx, 4)
				if err != nil && ctx.Err() == nil {
					fatalErrors.Add(1)
					recordError(fmt.Errorf("settlement replica %d: %w", replica, err))
				}
			}
		})
		startLoop(func() {
			repository := &usageBillingRepository{db: integrationDB}
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				stats, err := repository.DeliverSettledUsage(ctx, 4)
				delivered.Add(int64(stats.Delivered))
				deliveryFailures.Add(int64(stats.Failed))
				// An injected log-table lock is expected to produce timeout failures.
				// Every such failure remains persisted and must subsequently drain.
				if err != nil && stats.Failed == 0 && ctx.Err() == nil {
					fatalErrors.Add(1)
					recordError(fmt.Errorf("delivery replica %d: %w", replica, err))
				}
			}
		})
	}
	startLoop(func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		repo := newDashboardAggregationRepositoryWithSQL(integrationDB)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if err := repo.SyncGroupUsageRollups(ctx, service.GroupUsageTodayStart(time.Now())); err != nil && ctx.Err() == nil {
				fatalErrors.Add(1)
				recordError(fmt.Errorf("rollup worker: %w", err))
			}
		}
	})
	startLoop(func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			var pending, receipts, projection int64
			var receiptAmount, projectionAmount float64
			err := integrationDB.QueryRowContext(ctx, `SELECT
    (SELECT COUNT(*) FROM usage_settlement_receipts WHERE state='pending' OR delivered_at IS NULL),
    (SELECT COUNT(*) FROM usage_settlement_receipts WHERE state='settled'),
    (SELECT COALESCE(SUM(charged_amount),0) FROM usage_settlement_receipts WHERE state='settled'),
    (SELECT COUNT(*) FROM usage_financial_records),
    (SELECT COALESCE(SUM(actual_cost),0) FROM usage_financial_records)`).Scan(&pending, &receipts, &receiptAmount, &projection, &projectionAmount)
			if err != nil {
				if ctx.Err() == nil {
					fatalErrors.Add(1)
					recordError(fmt.Errorf("financial monitor: %w", err))
				}
				continue
			}
			increasePeak(&pendingPeak, pending)
			if receipts != projection || absStressAmountGeili(receiptAmount-projectionAmount) > 1e-8 {
				fatalErrors.Add(1)
				recordError(fmt.Errorf("financial projection mismatch receipts=%d/%0.10f projected=%d/%0.10f", receipts, receiptAmount, projection, projectionAmount))
			}
			financialChecks.Add(1)
		}
	})
	report.StartedAt = time.Now().UTC()
	t.Logf("STRESS_STARTED at=%s duration=%s workers=%d scheduled_rpm=%d", report.StartedAt.Format(time.RFC3339Nano), duration, workers, rpm)
	start := time.Now()
	sleepUntil := func(at time.Duration) bool {
		timer := time.NewTimer(time.Until(start.Add(at)))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	// A long rollup publisher lock and an actual usage-log write timeout overlap
	// with ongoing admissions/debits. Both fault transactions only touch this DB.
	faultDone := make(chan struct{})
	startLoop(func() {
		defer close(faultDone)
		when := duration / 5
		if when > 2*time.Minute {
			when = 2 * time.Minute
		}
		if !sleepUntil(when) {
			return
		}
		tx, err := integrationDB.BeginTx(ctx, nil)
		if err != nil {
			recordError(err)
			return
		}
		_, err = tx.ExecContext(ctx, `SELECT id FROM usage_group_rollup_state WHERE id=1 FOR UPDATE`)
		if err != nil {
			_ = tx.Rollback()
			recordError(err)
			return
		}
		reportMu.Lock()
		report.Faults = append(report.Faults, "rollup publisher state row held for 5 seconds")
		reportMu.Unlock()
		if !sleepUntil(when + 5*time.Second) {
			_ = tx.Rollback()
			return
		}
		if err := tx.Rollback(); err != nil {
			recordError(err)
		}
		when += 6 * time.Second
		if !sleepUntil(when) {
			return
		}
		tx, err = integrationDB.BeginTx(ctx, nil)
		if err != nil {
			recordError(err)
			return
		}
		_, err = tx.ExecContext(ctx, `LOCK TABLE usage_logs IN SHARE MODE`)
		if err != nil {
			_ = tx.Rollback()
			recordError(err)
			return
		}
		reportMu.Lock()
		report.Faults = append(report.Faults, "usage_logs SHARE lock held for 30 seconds; writes must time out and retry")
		reportMu.Unlock()
		if !sleepUntil(when + 30*time.Second) {
			_ = tx.Rollback()
			return
		}
		if err := tx.Rollback(); err != nil {
			recordError(err)
		}
		released := time.Now()
		var highWater int64
		if err := integrationDB.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM usage_settlement_receipts`).Scan(&highWater); err != nil {
			recordError(err)
			return
		}
		for time.Since(released) < 120*time.Second {
			var remaining int64
			err := integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_settlement_receipts WHERE id<=$1 AND (state<>'settled' OR delivered_at IS NULL)`, highWater).Scan(&remaining)
			if err != nil {
				if ctx.Err() == nil {
					recordError(err)
				}
				return
			}
			if remaining == 0 {
				reportMu.Lock()
				report.LockRecoverySeconds = append(report.LockRecoverySeconds, time.Since(released).Seconds())
				reportMu.Unlock()
				return
			}
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		recordError(fmt.Errorf("injected log-lock backlog did not drain within 120s"))
	})
	jobs := make(chan int64, workers)
	var consumers sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		consumers.Add(1)
		go func() {
			defer consumers.Done()
			repo := &usageBillingRepository{db: integrationDB}
			for seq := range jobs {
				a := active.Add(1)
				increasePeak(&peak, a)
				func() {
					defer active.Add(-1)
					f := fixtureFor(seq)
					units := 1 + seq%5
					amount := float64(units) / 10000
					now := time.Now().Truncate(time.Microsecond)
					cmd := &service.UsageBillingCommand{RequestID: fmt.Sprintf("stress-fin-%s-%d", report.RunID, seq), APIKeyID: f.keyID, UserID: f.userID, AccountID: f.accountID, AccountType: service.AccountTypeAPIKey, Model: "stress-model", InputTokens: 20, OutputTokens: 5, APIKeyQuotaCost: amount, CompletedAt: now}
					detail := &service.UsageLog{RequestID: fmt.Sprintf("stress-log-%s-%d", report.RunID, seq), APIKeyID: f.keyID, UserID: f.userID, AccountID: f.accountID, GroupID: f.groupID, Model: "stress-model", RequestedModel: "stress-model", InputTokens: 20, OutputTokens: 5, ActualCost: amount, TotalCost: amount * 2, RateMultiplier: 0.5, CreatedAt: now, Stream: seq%2 == 0}
					if f.sub == nil {
						cmd.BalanceCost = amount
					} else {
						admitted, err := f.sub.svc.AdmitConsumption(ctx, f.sub.sub, f.keyID)
						if err != nil {
							recordError(fmt.Errorf("admission seq%d: %w", seq, err))
							return
						}
						cmd.SubscriptionID = &f.sub.sub.ID
						cmd.SubscriptionAdmissionKey = admitted.AdmissionKey
						cmd.SubscriptionCost = amount
						cmd.BillingType = service.BillingTypeSubscription
						detail.SubscriptionID = cmd.SubscriptionID
						detail.BillingType = cmd.BillingType
					}
					if err := repo.PrepareSettlement(ctx, cmd, detail); err != nil {
						recordError(fmt.Errorf("prepare seq%d: %w", seq, err))
						return
					}
					// Persisted pending work is deliberately left to replicas for 1/5 calls.
					if seq%5 != 0 {
						if _, err := repo.Apply(ctx, cmd); err != nil {
							recordError(fmt.Errorf("apply seq%d: %w", seq, err))
							return
						}
					}
					if seq%7 == 0 {
						// First replay may win a pending settlement; the second MUST be a no-op.
						if _, err := repo.Apply(ctx, cmd); err != nil {
							recordError(fmt.Errorf("replay seq%d: %w", seq, err))
							return
						}
						result, err := repo.Apply(ctx, cmd)
						if err != nil {
							recordError(fmt.Errorf("second replay seq%d: %w", seq, err))
							return
						}
						if result.Applied {
							recordError(fmt.Errorf("duplicate debit seq%d", seq))
							return
						}
						duplicates.Add(2)
					}
					f.units.Add(units)
					f.requests.Add(1)
					unique.Add(1)
				}()
			}
		}()
	}
	burstInterval := time.Minute * workers / time.Duration(rpm)
	var seq int64
	for at := time.Duration(0); at < duration; at += burstInterval {
		if !sleepUntil(at) {
			break
		}
		for i := 0; i < workers; i++ {
			jobs <- seq
			seq++
		}
	}
	// Keep injection/monitoring active through the complete configured duration.
	_ = sleepUntil(duration)
	close(jobs)
	consumers.Wait()
	report.IngestionSeconds = time.Since(start).Seconds()
	report.UniqueRequests = unique.Load()
	report.AchievedRPM = float64(report.UniqueRequests) * 60 / report.IngestionSeconds
	report.DuplicateAttempts = duplicates.Load()
	report.PeakConcurrentWorkers = peak.Load()
	drainStart := time.Now()
	var pending int64
	for {
		require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_settlement_receipts WHERE state<>'settled' OR delivered_at IS NULL`).Scan(&pending))
		if pending == 0 || time.Since(drainStart) >= 120*time.Second {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	report.FinalDrainSeconds = time.Since(drainStart).Seconds()
	select {
	case <-faultDone:
	case <-time.After(2 * time.Second):
		recordError(fmt.Errorf("fault recovery did not complete before final drain"))
	}
	stop()
	bg.Wait()
	report.Delivered = delivered.Load()
	report.DeliveryFailures = deliveryFailures.Load()
	report.PendingPeak = pendingPeak.Load()
	report.FinancialChecks = financialChecks.Load()
	require.Zero(t, pending, "permanent pending settlement or missing detail")
	require.Zero(t, fatalErrors.Load(), "unexpected background errors; see report")
	require.Empty(t, report.Errors)
	require.GreaterOrEqual(t, report.AchievedRPM, 630.0)
	require.Equal(t, int64(workers), report.PeakConcurrentWorkers, "128 workers must overlap")
	require.Positive(t, report.DeliveryFailures, "30s table lock must exercise the 15s delivery timeout")
	require.Len(t, report.LockRecoverySeconds, 1)
	require.Less(t, report.LockRecoverySeconds[0], 120.0)
	require.GreaterOrEqual(t, report.IngestionSeconds, duration.Seconds())

	// Four-way reconciliation by user, including real entitlement allocations and
	// finance API semantics. These checks neither modify nor reconstruct amounts.
	var totalExpected int64
	api := &usageLogRepository{sql: integrationDB}
	for _, f := range fixtures {
		expected := float64(f.units.Load()) / 10000
		totalExpected += f.units.Load()
		var count, logCount, dedupCount int64
		var amount, logAmount, delta, quota float64
		require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*),COALESCE(SUM(charged_amount),0) FROM usage_settlement_receipts WHERE user_id=$1 AND state='settled' AND delivered_at IS NOT NULL`, f.userID).Scan(&count, &amount))
		require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*),COALESCE(SUM(actual_cost),0) FROM usage_logs WHERE user_id=$1`, f.userID).Scan(&logCount, &logAmount))
		require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_billing_dedup WHERE api_key_id=$1`, f.keyID).Scan(&dedupCount))
		require.NoError(t, integrationDB.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, f.keyID).Scan(&quota))
		require.Equal(t, f.requests.Load(), count)
		require.Equal(t, count, logCount)
		require.Equal(t, count, dedupCount)
		require.InDelta(t, expected, amount, 1e-8)
		require.InDelta(t, expected, logAmount, 1e-8)
		require.InDelta(t, expected, quota, 1e-8)
		if f.sub == nil {
			require.NoError(t, integrationDB.QueryRow(`SELECT 100-balance FROM users WHERE id=$1`, f.userID).Scan(&delta))
			require.InDelta(t, expected, delta, 1e-8)
		} else {
			var allocations, daily, settled, remainingBalance float64
			require.NoError(t, integrationDB.QueryRow(`SELECT COALESCE(SUM(a.cost_usd),0) FROM subscription_usage_allocations a JOIN subscription_requests r ON r.request_key=a.request_key WHERE r.subscription_id=$1`, f.sub.sub.ID).Scan(&allocations))
			require.NoError(t, integrationDB.QueryRow(`SELECT COALESCE(SUM(used_usd),0) FROM subscription_daily_usage WHERE subscription_id=$1`, f.sub.sub.ID).Scan(&daily))
			require.NoError(t, integrationDB.QueryRow(`SELECT COALESCE(SUM(cost_usd),0) FROM subscription_requests WHERE subscription_id=$1 AND status='settled'`, f.sub.sub.ID).Scan(&settled))
			require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.userID).Scan(&remainingBalance))
			require.InDelta(t, expected, allocations, 1e-8)
			require.InDelta(t, expected, daily, 1e-8)
			require.InDelta(t, expected, settled, 1e-8)
			require.Zero(t, remainingBalance)
			if f.giftID > 0 {
				var giftUsed, paidUsed float64
				require.NoError(t, integrationDB.QueryRow(`SELECT COALESCE(SUM(lifetime_usage_usd) FILTER(WHERE id=$2),0),COALESCE(SUM(lifetime_usage_usd) FILTER(WHERE id<>$2),0) FROM user_subscription_entitlements WHERE user_subscription_id=$1`, f.sub.sub.ID, f.giftID).Scan(&giftUsed, &paidUsed))
				require.InDelta(t, expected, giftUsed, 1e-8)
				require.Zero(t, paidUsed, "gift-first rule unchanged")
			}
		}
		stats, err := api.GetFinancialUsageStats(context.Background(), usagestats.UsageLogFilters{UserID: f.userID, DateBasis: usagestats.FinancialDateAccounting})
		require.NoError(t, err)
		require.Equal(t, count, stats.TotalRequests)
		require.InDelta(t, expected, stats.TotalActualCost, 1e-8)
		require.Zero(t, stats.DetailPendingCount)
		require.Zero(t, stats.UnknownAmountCount)
		report.Checks[fmt.Sprintf("%s_user_%d", f.kind, f.userID)] = map[string]any{"requests": count, "amount": amount, "logs": logCount, "dedup": dedupCount, "finance_api_amount": stats.TotalActualCost}
	}
	var mismatches int64
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_settlement_receipts r LEFT JOIN usage_logs u ON u.id=r.usage_log_id WHERE u.id IS NULL OR u.request_id<>r.usage_request_id OR u.api_key_id<>r.api_key_id OR u.user_id<>r.user_id OR u.actual_cost<>r.charged_amount`).Scan(&mismatches))
	require.Zero(t, mismatches)
	report.Checks["expected_total"] = float64(totalExpected) / 10000
	report.Checks["permanent_missing_details"] = pending
	report.Checks["receipt_log_identity_or_amount_mismatch"] = mismatches
	report.Checks["production_connections_or_external_calls"] = 0
}

func absStressAmountGeili(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
