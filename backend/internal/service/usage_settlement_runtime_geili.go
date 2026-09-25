package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

func (s *GatewayService) UsesDurableUsageSettlement() bool {
	if s == nil {
		return false
	}
	_, ok := s.usageBillingRepo.(UsageSettlementRepository)
	return ok
}
func (s *OpenAIGatewayService) UsesDurableUsageSettlement() bool {
	if s == nil {
		return false
	}
	_, ok := s.usageBillingRepo.(UsageSettlementRepository)
	return ok
}

// Compatibility only. Financial receipts are delivered by the repository's own
// transaction; this fallback bypasses the legacy batch queues entirely.
func createUsageLogDirect(ctx context.Context, repo UsageLogRepository, row *UsageLog) (bool, error) {
	if direct, ok := repo.(interface {
		CreateDirect(context.Context, *UsageLog) (bool, error)
	}); ok {
		return direct.CreateDirect(ctx, row)
	}
	return repo.Create(ctx, row)
}

// Cache invalidation is replay-safe. Arithmetic is confined to the financial
// transaction, so delayed/repeated workers never increment cached quotas again.
func invalidateSettlementCaches(ctx context.Context, cache *BillingCacheService, cmd *UsageBillingCommand) {
	if cache == nil || cmd == nil {
		return
	}
	if cmd.SubscriptionID != nil {
		if err := cache.InvalidateSubscriptionByID(ctx, *cmd.SubscriptionID); err != nil {
			slog.Warn("settlement subscription cache invalidation failed", "subscription_id", *cmd.SubscriptionID, "error", err)
		}
		if cmd.UsageDetail != nil && cmd.UsageDetail.GroupID != nil {
			_ = cache.InvalidateSubscription(ctx, cmd.UserID, *cmd.UsageDetail.GroupID)
		}
	} else if cmd.BalanceCost > 0 {
		_ = cache.InvalidateUserBalance(ctx, cmd.UserID)
	}
	if cmd.APIKeyRateLimitCost > 0 {
		_ = cache.InvalidateAPIKeyRateLimit(ctx, cmd.APIKeyID)
	}
	if cmd.PlatformQuotaCost > 0 && cmd.Platform != "" && cache.cache != nil {
		if err := cache.cache.DeleteUserPlatformQuotaCache(ctx, cmd.UserID, cmd.Platform); err != nil {
			slog.Warn("settlement platform cache invalidation failed", "user_id", cmd.UserID, "platform", cmd.Platform, "error", err)
		}
	}
}

type UsageSettlementRuntimeHealth struct {
	Reconciliation      *UsageSettlementReconciliation `json:"reconciliation,omitempty"`
	ReconciliationError string                         `json:"reconciliation_error,omitempty"`
	Video               GrokVideoTaskHealth            `json:"video"`
	Ingress             UsageSettlementIngressHealth   `json:"ingress"`
	IngressError        string                         `json:"ingress_error,omitempty"`
	UsageSettlementHealth
	Running    bool   `json:"running"`
	Applied    uint64 `json:"applied"`
	Delivered  uint64 `json:"delivered"`
	Failures   uint64 `json:"failures"`
	StatsError string `json:"stats_error,omitempty"`
}

// Workers only own leases. All recoverable state is in PostgreSQL, so stopping
// or replacing any instance cannot lose a settlement or acknowledge a lost log.
type UsageSettlementWorker struct {
	reconcileMu    sync.RWMutex
	reconcile      *UsageSettlementReconciliation
	reconcileError string
	repo           UsageSettlementRepository
	cfg            *config.Config
	video          *OpenAIGatewayService
	cache          *BillingCacheService
	deferred       *DeferredService
	ctx            context.Context
	cancel         context.CancelFunc
	start          sync.Once
	stop           sync.Once
	wg             sync.WaitGroup
	running        atomic.Bool
	applied        atomic.Uint64
	delivered      atomic.Uint64
	failures       atomic.Uint64
}

func NewUsageSettlementWorker(repo UsageBillingRepository, cache *BillingCacheService, deferred *DeferredService) *UsageSettlementWorker {
	ctx, cancel := context.WithCancel(context.Background())
	durable, _ := repo.(UsageSettlementRepository)
	return &UsageSettlementWorker{repo: durable, cache: cache, deferred: deferred, ctx: ctx, cancel: cancel}
}
func (w *UsageSettlementWorker) Start() {
	if w == nil || w.repo == nil {
		return
	}
	w.start.Do(func() {
		w.running.Store(true)
		// Separate consumers prevent a blocked detail FK from stalling settlement.
		for i := 0; i < 4; i++ {
			w.wg.Add(2)
			go w.run(false)
			go w.run(true)
		}
		w.wg.Add(1)
		go w.monitor()
		w.wg.Add(1)
		go w.replayIngress()
		if w.video != nil {
			w.wg.Add(1)
			go w.pollVideoTasks()
		}
	})
}
func (w *UsageSettlementWorker) Stop() {
	if w == nil {
		return
	}
	w.stop.Do(func() { w.cancel(); w.wg.Wait(); w.running.Store(false) })
}
func (w *UsageSettlementWorker) run(delivery bool) {
	defer w.wg.Done()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if w.ctx.Err() != nil {
			return
		}
		if err := w.process(w.ctx, delivery); err != nil && w.ctx.Err() == nil {
			w.failures.Add(1)
			slog.Error("usage settlement worker retry retained", "delivery", delivery, "error", err)
		}
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (w *UsageSettlementWorker) process(ctx context.Context, delivery bool) error {
	if delivery {
		stats, err := w.repo.DeliverSettledUsage(ctx, 32)
		w.delivered.Add(uint64(stats.Delivered))
		w.failures.Add(uint64(stats.Failed))
		return err
	}
	rows, err := w.repo.ProcessPendingSettlements(ctx, 32)
	for _, row := range rows {
		if row.Command == nil || row.Result == nil || !row.Result.Applied {
			continue
		}
		w.applied.Add(1)
		cacheCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		invalidateSettlementCaches(cacheCtx, w.cache, row.Command)
		cancel()
		if w.deferred != nil && row.Command.AccountID > 0 {
			w.deferred.ScheduleLastUsedUpdate(row.Command.AccountID)
		}
	}
	return err
}
func (w *UsageSettlementWorker) Health(ctx context.Context) UsageSettlementRuntimeHealth {
	var h UsageSettlementRuntimeHealth
	if w == nil {
		return h
	}
	var ingressErr error
	h.Ingress, ingressErr = usageSettlementIngressHealth(w.cfg)
	if ingressErr != nil {
		h.IngressError = ingressErr.Error()
	}
	if w.video != nil {
		h.Video = w.video.GrokVideoTaskHealth(ctx)
	}
	w.reconcileMu.RLock()
	if w.reconcile != nil {
		copy := *w.reconcile
		h.Reconciliation = &copy
	}
	h.ReconciliationError = w.reconcileError
	w.reconcileMu.RUnlock()
	h.Running = w.running.Load()
	h.Applied = w.applied.Load()
	h.Delivered = w.delivered.Load()
	h.Failures = w.failures.Load()
	if w.repo != nil {
		stats, err := w.repo.SettlementHealth(ctx)
		h.UsageSettlementHealth = stats
		if err != nil {
			h.StatsError = err.Error()
		}
	}
	return h
}
func (w *UsageSettlementWorker) monitor() {
	defer w.wg.Done()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		}
		w.reconcileRecent()
		ctx, cancel := context.WithTimeout(w.ctx, 5*time.Second)
		h := w.Health(ctx)
		cancel()
		switch {
		case h.ReconciliationError != "":
			slog.Error("ALERT settlement reconciliation unavailable", "error", h.ReconciliationError)
		case settlementReconciliationMismatch(h.Reconciliation):
			slog.Error("ALERT settlement evidence mismatch", "reconciliation", h.Reconciliation)
		case h.Video.Error != "":
			slog.Error("ALERT asynchronous video evidence unavailable", "health", h.Video)
		case h.IngressError != "":
			slog.Error("ALERT usage settlement ingress unavailable", "error", h.IngressError, "health", h)
		case h.StatsError != "":
			slog.Error("usage settlement monitoring unavailable", "error", h.StatsError)
		case h.Critical120Seconds > 0 || h.AmountMismatchCount > 0 || h.Ingress.OldestPendingSeconds > 120 || h.Ingress.Invalid > 0 || h.Video.IngressOldestSeconds > 120:
			slog.Error("ALERT usage settlement critical", "health", h)
		case h.Warning30Seconds > 0 || h.Ingress.OldestPendingSeconds > 30 || h.Video.IngressOldestSeconds > 30:
			slog.Warn("ALERT usage settlement delayed", "health", h)
		}
	}
}
func ProvideUsageSettlementWorker(repo UsageBillingRepository, cache *BillingCacheService, deferred *DeferredService, cfg *config.Config, video *OpenAIGatewayService) (*UsageSettlementWorker, error) {
	worker := NewUsageSettlementWorker(repo, cache, deferred)
	worker.cfg = cfg
	worker.video = video
	if worker.repo != nil {
		if cfg == nil || strings.TrimSpace(cfg.Pricing.DataDir) == "" {
			return nil, fmt.Errorf("durable settlement requires pricing.data_dir on a persistent volume")
		}
		if err := ensureUsageSettlementIngress(cfg); err != nil {
			return nil, err
		}
	}
	worker.Start()
	return worker, nil
}

func (s *OpsService) GetUsageSettlementHealth(ctx context.Context) UsageSettlementRuntimeHealth {
	if s == nil || s.usageSettlementWorker == nil {
		return UsageSettlementRuntimeHealth{}
	}
	return s.usageSettlementWorker.Health(ctx)
}

// Replay files independently of SQL/delivery consumers so a blocked task cannot
// starve durable snapshots accepted while PostgreSQL was unavailable.
func (w *UsageSettlementWorker) replayIngress() {
	defer w.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if w.ctx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(w.ctx, 10*time.Second)
		stats, err := replayUsageSettlementIngress(ctx, w.cfg, w.repo, 32)
		cancel()
		if err != nil && w.ctx.Err() == nil {
			w.failures.Add(1)
			slog.Error("ALERT settlement ingress retry retained", "error", err, "stats", stats)
		}
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *BillingCacheService) checkSettlementPlatformQuota(ctx context.Context, userID int64, platform string) error {
	rec, err := s.userPlatformQuotaRepo.GetByUserPlatform(ctx, userID, platform)
	if err != nil {
		return ErrBillingServiceUnavailable
	}
	if rec == nil {
		return nil
	}
	now := time.Now()
	daily, weekly, monthly := rec.DailyUsageUSD, rec.WeeklyUsageUSD, rec.MonthlyUsageUSD
	if quotaWindowExpired(rec.DailyWindowStart, timezone.StartOfDay(now)) {
		daily = 0
	}
	if quotaWindowExpired(rec.WeeklyWindowStart, timezone.StartOfWeek(now)) {
		weekly = 0
	}
	if monthlyQuotaWindowExpired(rec.MonthlyWindowStart, now) {
		monthly = 0
	}
	if rec.DailyLimitUSD != nil && daily >= *rec.DailyLimitUSD {
		return withWindowResetsMetadata(ErrUserPlatformDailyQuotaExhausted, nextDailyReset(now))
	}
	if rec.WeeklyLimitUSD != nil && weekly >= *rec.WeeklyLimitUSD {
		return withWindowResetsMetadata(ErrUserPlatformWeeklyQuotaExhausted, nextWeeklyReset(now))
	}
	if rec.MonthlyLimitUSD != nil && monthly >= *rec.MonthlyLimitUSD {
		return withWindowResetsMetadata(ErrUserPlatformMonthlyQuotaExhausted, nextMonthlyResetFrom(rec.MonthlyWindowStart, now))
	}
	return nil
}

func (w *UsageSettlementWorker) pollVideoTasks() {
	defer w.wg.Done()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if w.ctx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(w.ctx, 60*time.Second)
		err := w.video.ProcessDurableGrokVideoTasks(ctx, 4)
		cancel()
		if err != nil && w.ctx.Err() == nil {
			w.failures.Add(1)
			slog.Error("ALERT asynchronous video evidence retry retained", "error", err)
		}
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *UsageSettlementWorker) reconcileRecent() {
	repo, ok := w.repo.(SettlementReconciliationRepository)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(w.ctx, 5*time.Second)
	report, err := repo.ReconcileRecentSettlements(ctx, 1000)
	cancel()
	w.reconcileMu.Lock()
	defer w.reconcileMu.Unlock()
	if err != nil {
		w.reconcileError = err.Error()
		return
	}
	w.reconcile = &report
	w.reconcileError = ""
}
func settlementReconciliationMismatch(r *UsageSettlementReconciliation) bool {
	return r != nil && (r.AmountMismatchCount > 0 || r.DetailMissingCount > 0 || r.IdentityMismatchCount > 0 || r.DedupMismatchCount > 0 || r.SubscriptionMismatchCount > 0)
}
