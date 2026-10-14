package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type accountModelFailureObservationKeyGeili struct{}
type accountModelFailureObservationGeili struct {
	mu      sync.Mutex
	claimed map[accountModelFailureKeyGeili]bool
}
type accountModelFailureKeyGeili struct {
	id    int64
	model string
}

// Each public account forward gets its own receipt; it is never a header or
// request-body field. Direct callers without a receipt retain one event/call.
func WithAccountModelFailureObservationGeili(ctx context.Context) context.Context {
	return context.WithValue(ctx, accountModelFailureObservationKeyGeili{}, &accountModelFailureObservationGeili{claimed: make(map[accountModelFailureKeyGeili]bool)})
}

func ClaimAccountModelFailureObservationGeili(ctx context.Context, id int64, model string) bool {
	receipt, _ := ctx.Value(accountModelFailureObservationKeyGeili{}).(*accountModelFailureObservationGeili)
	if receipt == nil {
		return true
	}
	receipt.mu.Lock()
	defer receipt.mu.Unlock()
	key := accountModelFailureKeyGeili{id, model}
	if receipt.claimed[key] {
		return false
	}
	receipt.claimed[key] = true
	return true
}

func (s *RateLimitService) usesModelRuntimeHTTPFailureGeili(ctx context.Context, account *Account, status int) bool {
	if s == nil || account == nil || isAccountRecoveryProbeGeili(ctx) || status < 500 || status > 599 {
		return false
	}
	_, ok := s.accountRepo.(AccountRuntimeRepositoryGeili)
	return ok
}

func (s *RateLimitService) recordRuntimeHTTPFailureGeili(ctx context.Context, account *Account, model string) {
	if forwarded := AccountRuntimeForwardModelGeili(ctx); forwarded != "" {
		model = forwarded
	}
	if strings.TrimSpace(model) == "" {
		model = tempUnschedulableModel(ctx, nil)
	}
	model = strings.TrimSpace(model)
	if !ClaimAccountModelFailureObservationGeili(ctx, account.ID, model) {
		return
	}
	writer, ok := s.accountRepo.(AccountRuntimeRepositoryGeili)
	if !ok {
		return
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := writer.RecordModelFailureGeili(persist, account.ID, model, "temporary_upstream_failure", time.Now().Add(time.Minute)); err != nil {
		slog.Warn("account_runtime_http_failure_persist_failed_geili", "account_id", account.ID)
	}
}

// Keep explicit provider/quota windows intact. Only expired or generic
// transport/5xx temporary stops can be cleared by a complete current probe.
func AccountGenericTemporaryStopGeili(account *Account, now time.Time) bool {
	if account == nil || account.TempUnschedulableUntil == nil {
		return false
	}
	if !account.TempUnschedulableUntil.After(now) {
		return true
	}
	var payload TempUnschedState
	if json.Unmarshal([]byte(account.TempUnschedulableReason), &payload) == nil {
		return payload.Scope != "model" && payload.StatusCode >= 500 && payload.StatusCode <= 599
	}
	reason := strings.ToLower(account.TempUnschedulableReason)
	for _, marker := range []string{"rate limit", "quota", "usage window", "billing", "balance", "额度", "余额", "限流"} {
		if strings.Contains(reason, marker) {
			return false
		}
	}
	for _, marker := range []string{"transport", "connection", "timeout", "upstream", "temporary", "overload", "proxy", "dns", "tls", "eof", "超时", "连接", "临时", "过载"} {
		if strings.Contains(reason, marker) {
			return true
		}
	}
	return false
}

// Repository activation already cleared the generic PostgreSQL fields. This
// narrow cache callback does not clear rate windows or any model's state.
func (s *RateLimitService) ClearRecoveredGenericTemporaryCacheGeili(ctx context.Context, id int64) error {
	if s == nil {
		return nil
	}
	if s.tempUnschedCache != nil {
		if err := s.tempUnschedCache.DeleteTempUnsched(ctx, id); err != nil {
			return err
		}
	}
	s.notifyAccountSchedulingBlockCleared(id)
	return nil
}

// ResetAccountModelFailureObservationGeili starts a new serialized WebSocket
// turn without changing the context captured by the relay's two goroutines.
// Call only after the previous turn's health callbacks have settled.
func ResetAccountModelFailureObservationGeili(ctx context.Context) {
	receipt, _ := ctx.Value(accountModelFailureObservationKeyGeili{}).(*accountModelFailureObservationGeili)
	if receipt == nil {
		return
	}
	receipt.mu.Lock()
	defer receipt.mu.Unlock()
	clear(receipt.claimed)
}
