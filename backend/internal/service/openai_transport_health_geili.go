package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	OpenAITransportFailureThresholdGeili = 3
	OpenAITransportFailureTTLGeili       = 10 * time.Minute
	OpenAITransportCooldownGeili         = time.Minute
	openAITransportHealthMaxEntriesGeili = 4096
	openAITransportHealthTimeoutGeili    = 200 * time.Millisecond
	openAITransportHealthReasonGeili     = "openai_transport_health"
)

// OpenAITransportHealthDecisionGeili describes an atomic transport observation.
// Successful in-flight requests reset the streak but cannot lift an active block.
type OpenAITransportHealthDecisionGeili struct {
	Count      int64
	Tripped    bool
	Until      time.Time
	generation uint64
}

// OpenAITransportHealthCacheGeili is an optional TempUnschedCache extension.
// It is independent of advanced scheduling and the API-key pool breaker.
type OpenAITransportHealthCacheGeili interface {
	RecordOpenAITransportFailureGeili(context.Context, int64) (OpenAITransportHealthDecisionGeili, error)
	ResetOpenAITransportFailuresGeili(context.Context, int64) error
	OpenAITransportBlockedUntilGeili(context.Context, int64) (time.Time, error)
}

type openAITransportHealthEntryGeili struct {
	count       int64
	lastFailure time.Time
	until       time.Time
	generation  uint64
}

type openAITransportHealthStateGeili struct {
	mu       sync.Mutex
	entries  map[int64]openAITransportHealthEntryGeili
	sequence uint64
}

func (s *OpenAIGatewayService) transportHealthStateGeili() *openAITransportHealthStateGeili {
	s.openaiTransportHealthOnceGeili.Do(func() {
		s.openaiTransportHealthGeili = &openAITransportHealthStateGeili{entries: make(map[int64]openAITransportHealthEntryGeili)}
	})
	return s.openaiTransportHealthGeili
}

func (s *OpenAIGatewayService) transportHealthCacheGeili() OpenAITransportHealthCacheGeili {
	if s == nil || s.rateLimitService == nil {
		return nil
	}
	cache, _ := s.rateLimitService.tempUnschedCache.(OpenAITransportHealthCacheGeili)
	return cache
}

func isOpenAITransportHealthAccountGeili(account *Account) bool {
	return account != nil && account.ID > 0 && account.Platform == PlatformOpenAI &&
		(account.Type == AccountTypeOAuth || account.Type == AccountTypeAPIKey)
}

// record keeps an independent local streak. Shared observations only merge blocks;
// an older Redis reply must not resurrect failures cleared by a concurrent success.
func (h *openAITransportHealthStateGeili) record(id int64, now time.Time, shared *OpenAITransportHealthDecisionGeili, pending ...bool) OpenAITransportHealthDecisionGeili {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := h.entries[id]
	if _, exists := h.entries[id]; !exists && len(h.entries) >= openAITransportHealthMaxEntriesGeili {
		// Never evict an active quarantine to admit a new observation.
		var oldestID int64
		var oldest time.Time
		for key, value := range h.entries {
			if !now.Before(value.until) && (oldestID == 0 || value.lastFailure.Before(oldest)) {
				oldestID, oldest = key, value.lastFailure
			}
		}
		if oldestID == 0 {
			return OpenAITransportHealthDecisionGeili{}
		}
		delete(h.entries, oldestID)
	}
	decision := OpenAITransportHealthDecisionGeili{}
	if shared != nil {
		decision = *shared
		if shared.generation != 0 && shared.generation != entry.generation {
			return OpenAITransportHealthDecisionGeili{}
		}
		if shared.Tripped {
			entry.count = 0
		}
	} else if !now.Before(entry.until) {
		if now.Sub(entry.lastFailure) >= OpenAITransportFailureTTLGeili || now.Before(entry.lastFailure) {
			entry.count = 0
			entry.generation = 0
		}
		// Only success/expiry starts a new epoch. Another failure must not
		// invalidate a pending trip while its Redis call is in flight.
		if entry.generation == 0 {
			h.sequence++
			entry.generation = h.sequence
		}
		decision.generation = entry.generation
		entry.count++
		decision.Count = entry.count
		if entry.count >= OpenAITransportFailureThresholdGeili {
			decision.Tripped, decision.Until = true, now.Add(OpenAITransportCooldownGeili)
			entry.count = 0
		}
	}
	if (len(pending) == 0 || !pending[0]) && decision.Until.After(entry.until) {
		entry.until = decision.Until
	}
	entry.lastFailure = now
	h.entries[id] = entry
	if entry.until.After(decision.Until) {
		decision.Until = entry.until
	}
	return decision
}

func (h *openAITransportHealthStateGeili) blockedUntil(id int64, now time.Time) time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := h.entries[id]
	if now.Before(entry.until) {
		return entry.until
	}
	if now.Sub(entry.lastFailure) >= OpenAITransportFailureTTLGeili {
		delete(h.entries, id)
	}
	return time.Time{}
}

func (h *openAITransportHealthStateGeili) success(id int64, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := h.entries[id]
	if now.Before(entry.until) {
		entry.count = 0
		h.sequence++
		entry.generation = h.sequence
		h.entries[id] = entry
	} else {
		delete(h.entries, id)
	}
}

func (s *OpenAIGatewayService) observeOpenAITransportFailureGeili(ctx context.Context, account *Account, safeErr string) {
	if s == nil || !isOpenAITransportHealthAccountGeili(account) {
		return
	}
	now := time.Now()
	local := s.transportHealthStateGeili().record(account.ID, now, nil, true)
	decision := local
	var shared *OpenAITransportHealthDecisionGeili
	if cache := s.transportHealthCacheGeili(); cache != nil {
		cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAITransportHealthTimeoutGeili)
		decision, err := cache.RecordOpenAITransportFailureGeili(cacheCtx, account.ID)
		cancel()
		if err == nil {
			shared = &decision
		} else {
			logger.L().Warn("openai.transport_health_cache_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		}
	}
	if shared != nil {
		decision = *shared
		s.transportHealthStateGeili().record(account.ID, now, shared)
	} else if local.Tripped {
		decision = s.transportHealthStateGeili().record(account.ID, now, &local)
	}
	if !decision.Tripped {
		return
	}
	until := decision.Until
	if account.TempUnschedulableUntil != nil && account.TempUnschedulableUntil.After(until) {
		until = *account.TempUnschedulableUntil
	}
	s.transportHealthStateGeili().record(account.ID, now, &OpenAITransportHealthDecisionGeili{Until: until})
	state := &TempUnschedState{
		UntilUnix: until.Unix(), TriggeredAtUnix: now.Unix(), StatusCode: 0,
		MatchedKeyword: openAITransportHealthReasonGeili, RuleIndex: -1,
		ErrorMessage: "Repeated upstream transport failure",
		TriggerCount: decision.Count, TriggerThreshold: OpenAITransportFailureThresholdGeili,
		TriggerWindowMinutes: int(OpenAITransportFailureTTLGeili / time.Minute),
	}
	reason, _ := json.Marshal(state)
	// Update the selection copy immediately; the independent local state also
	// protects stale snapshots and failed database writes.
	account.TempUnschedulableUntil, account.TempUnschedulableReason = &until, string(reason)
	s.BlockAccountScheduling(account, until, openAITransportHealthReasonGeili)
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIAccountStateUpdateTimeout)
	defer cancel()
	if s.rateLimitService != nil && s.rateLimitService.tempUnschedCache != nil {
		if err := s.rateLimitService.tempUnschedCache.SetTempUnsched(persistCtx, account.ID, state); err != nil {
			logger.L().Warn("openai.transport_health_temp_cache_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		}
	}
	if s.accountRepo != nil {
		if err := s.accountRepo.SetTempUnschedulable(persistCtx, account.ID, until, string(reason)); err != nil {
			logger.L().Warn("openai.transport_health_persist_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		}
	}
	logger.L().Warn("openai.transport_health_cooled", zap.Int64("account_id", account.ID),
		zap.String("account_type", account.Type), zap.Int64("failure_count", decision.Count),
		zap.Time("until", until), zap.String("reason", openAITransportHealthReasonGeili))
}

func (s *OpenAIGatewayService) resetOpenAITransportHealthGeili(account *Account) {
	if s == nil || !isOpenAITransportHealthAccountGeili(account) {
		return
	}
	s.transportHealthStateGeili().success(account.ID, time.Now())
	if cache := s.transportHealthCacheGeili(); cache != nil {
		ctx, cancel := context.WithTimeout(context.Background(), openAITransportHealthTimeoutGeili)
		defer cancel()
		if err := cache.ResetOpenAITransportFailuresGeili(ctx, account.ID); err != nil {
			logger.L().Warn("openai.transport_health_reset_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		}
	}
}

// A selection shares one batched read and never waits once per account during a cache outage.
type openAITransportSelectionKeyGeili struct{}
type openAITransportSelectionGeili struct {
	until  map[int64]time.Time
	failed bool
}

func withOpenAITransportSelectionGeili(ctx context.Context) context.Context {
	return context.WithValue(ctx, openAITransportSelectionKeyGeili{}, &openAITransportSelectionGeili{until: make(map[int64]time.Time)})
}

// OpenAITransportHealthBatchCacheGeili avoids per-candidate Redis round trips.
type OpenAITransportHealthBatchCacheGeili interface {
	OpenAITransportBlocksGeili(context.Context, []int64) (map[int64]time.Time, error)
}

func (s *OpenAIGatewayService) prefetchOpenAITransportHealthGeili(ctx context.Context, accounts []Account) {
	memo, _ := ctx.Value(openAITransportSelectionKeyGeili{}).(*openAITransportSelectionGeili)
	cache, ok := s.transportHealthCacheGeili().(OpenAITransportHealthBatchCacheGeili)
	if memo == nil || memo.failed || !ok {
		return
	}
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		if isOpenAITransportHealthAccountGeili(&accounts[i]) {
			if _, exists := memo.until[accounts[i].ID]; !exists {
				ids = append(ids, accounts[i].ID)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	cacheCtx, cancel := context.WithTimeout(ctx, openAITransportHealthTimeoutGeili)
	defer cancel()
	blocks, err := cache.OpenAITransportBlocksGeili(cacheCtx, ids)
	if err != nil {
		memo.failed = true
		return
	}
	for _, id := range ids {
		memo.until[id] = blocks[id]
		if until := blocks[id]; time.Now().Before(until) {
			// Retain blocks learned by filtering, even if no selected-account
			// lookup occurs before the next Redis/database outage.
			s.transportHealthStateGeili().record(id, time.Now(), &OpenAITransportHealthDecisionGeili{Until: until})
		}
	}
}

func (s *OpenAIGatewayService) listSchedulableAccounts(ctx context.Context, groupID *int64, platform string) ([]Account, error) {
	accounts, err := s.listSchedulableAccountsWithoutTransportHealthGeili(ctx, groupID, platform)
	if err == nil {
		s.prefetchOpenAITransportHealthGeili(ctx, accounts)
	}
	return accounts, err
}

func (s *OpenAIGatewayService) openAITransportBlockedGeili(ctx context.Context, account *Account, fresh bool) bool {
	if s == nil || !isOpenAITransportHealthAccountGeili(account) {
		return false
	}
	if until := s.transportHealthStateGeili().blockedUntil(account.ID, time.Now()); !until.IsZero() {
		return true
	}
	cache := s.transportHealthCacheGeili()
	if cache == nil {
		return false
	}
	memo, _ := ctx.Value(openAITransportSelectionKeyGeili{}).(*openAITransportSelectionGeili)
	if memo != nil {
		if memo.failed {
			return false
		}
		if until, ok := memo.until[account.ID]; ok && !fresh {
			return time.Now().Before(until)
		}
	}
	cacheCtx, cancel := context.WithTimeout(ctx, openAITransportHealthTimeoutGeili)
	until, err := cache.OpenAITransportBlockedUntilGeili(cacheCtx, account.ID)
	cancel()
	if memo != nil {
		if err != nil {
			memo.failed = true
		} else {
			memo.until[account.ID] = until
		}
	}
	if err == nil && time.Now().Before(until) {
		s.transportHealthStateGeili().record(account.ID, time.Now(), &OpenAITransportHealthDecisionGeili{Until: until})
		return true
	}
	return false
}

// The independent transport block survives old snapshots and database failures.
func (s *OpenAIGatewayService) isOpenAIAccountRequestRuntimeBlockedGeili(ctx context.Context, account *Account, model string) bool {
	return s.openAITransportBlockedGeili(ctx, account, false) || s.isOpenAIAccountRequestRuntimeBlocked(account, model)
}
