package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

const SettingKeyOpenAIEncryptedRouteGeili = "openai_encrypted_route"

var encryptedRouteSettingsRefreshFailuresGeili atomic.Uint64

// OpenAIEncryptedRouteSettingsRefreshFailuresGeili counts failed repository reads,
// excluding an absent setting and peers that use the cached policy or exhaust
// their own wait budget.
func OpenAIEncryptedRouteSettingsRefreshFailuresGeili() uint64 {
	return encryptedRouteSettingsRefreshFailuresGeili.Load()
}

type OpenAIEncryptedRouteSettingsGeili struct {
	Mode                     string  `json:"mode"`
	ProtectedUserIDs         []int64 `json:"protected_user_ids"`
	ReasoningRecoveryEnabled bool    `json:"reasoning_recovery_enabled"`
}

func DefaultOpenAIEncryptedRouteSettingsGeili() OpenAIEncryptedRouteSettingsGeili {
	return OpenAIEncryptedRouteSettingsGeili{Mode: "off", ProtectedUserIDs: []int64{3885}}
}

func NormalizeOpenAIEncryptedRouteSettingsGeili(v OpenAIEncryptedRouteSettingsGeili) (OpenAIEncryptedRouteSettingsGeili, error) {
	switch v.Mode {
	case "off", "observe", "protect":
	default:
		return v, fmt.Errorf("mode must be off, observe or protect")
	}
	if len(v.ProtectedUserIDs) > 10000 {
		return v, fmt.Errorf("too many protected users")
	}
	seen := map[int64]bool{}
	for _, id := range v.ProtectedUserIDs {
		if id <= 0 || seen[id] {
			return v, fmt.Errorf("protected_user_ids must contain unique positive IDs")
		}
		seen[id] = true
	}
	v.ProtectedUserIDs = append([]int64{}, v.ProtectedUserIDs...)
	return v, nil
}
func ParseOpenAIEncryptedRouteSettingsGeili(raw string) OpenAIEncryptedRouteSettingsGeili {
	if raw == "" {
		return DefaultOpenAIEncryptedRouteSettingsGeili()
	}
	var v OpenAIEncryptedRouteSettingsGeili
	if json.Unmarshal([]byte(raw), &v) != nil {
		return DefaultOpenAIEncryptedRouteSettingsGeili()
	}
	normalized, err := NormalizeOpenAIEncryptedRouteSettingsGeili(v)
	if err != nil {
		return DefaultOpenAIEncryptedRouteSettingsGeili()
	}
	return normalized
}
func cloneEncryptedRouteSettingsGeili(v OpenAIEncryptedRouteSettingsGeili) OpenAIEncryptedRouteSettingsGeili {
	v.ProtectedUserIDs = append([]int64{}, v.ProtectedUserIDs...)
	return v
}

type cachedOpenAIEncryptedRouteSettingsGeili struct {
	settings   OpenAIEncryptedRouteSettingsGeili
	expires    time.Time
	refreshing chan struct{}
	waitUntil  time.Time
}

func (s *SettingService) invalidateOpenAIEncryptedRouteSettingsGeili() {
	for {
		old := s.encryptedRouteSettingsCache.Load()
		if old == nil {
			return
		}
		// Detach in-flight readers so a pre-write refresh cannot publish stale policy.
		next := &cachedOpenAIEncryptedRouteSettingsGeili{settings: old.settings}
		if s.encryptedRouteSettingsCache.CompareAndSwap(old, next) {
			return
		}
	}
}

// Hot reads are lock-free. One bounded background reader refreshes expired
// policy. Caller waits never exceed 50 ms; cold peers stop waiting after the
// shared initial window, even when the database read is still in flight.
func (s *SettingService) GetOpenAIEncryptedRouteSettingsGeili(ctx context.Context) OpenAIEncryptedRouteSettingsGeili {
	if s == nil || s.settingRepo == nil {
		return DefaultOpenAIEncryptedRouteSettingsGeili()
	}
	if cached := s.encryptedRouteSettingsCache.Load(); cached != nil && time.Now().Before(cached.expires) {
		return cloneEncryptedRouteSettingsGeili(cached.settings)
	}
	budget, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	for {
		old := s.encryptedRouteSettingsCache.Load()
		if old != nil && time.Now().Before(old.expires) {
			return cloneEncryptedRouteSettingsGeili(old.settings)
		}
		if old != nil && old.refreshing != nil {
			if old.settings.Mode != "" {
				return cloneEncryptedRouteSettingsGeili(old.settings)
			}
			return s.waitOpenAIEncryptedRouteSettingsGeili(budget, old)
		}
		if budget.Err() != nil || !s.encryptedRouteSettingsRefreshInFlight.CompareAndSwap(false, true) {
			return encryptedRouteSettingsFallbackGeili(old)
		}
		pending := &cachedOpenAIEncryptedRouteSettingsGeili{refreshing: make(chan struct{}), waitUntil: time.Now().Add(50 * time.Millisecond)}
		if old != nil {
			pending.settings = old.settings
		}
		if !s.encryptedRouteSettingsCache.CompareAndSwap(old, pending) {
			s.encryptedRouteSettingsRefreshInFlight.Store(false)
			continue
		}
		// A canceled request cannot cancel the shared administrative settings read.
		// Its independent deadline bounds both the worker and repository operation.
		refreshCtx, stopRefresh := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		go s.refreshOpenAIEncryptedRouteSettingsGeili(refreshCtx, stopRefresh, pending)
		return s.waitOpenAIEncryptedRouteSettingsGeili(budget, pending)
	}
}
func encryptedRouteSettingsFallbackGeili(cached *cachedOpenAIEncryptedRouteSettingsGeili) OpenAIEncryptedRouteSettingsGeili {
	if cached != nil && cached.settings.Mode != "" {
		return cloneEncryptedRouteSettingsGeili(cached.settings)
	}
	return DefaultOpenAIEncryptedRouteSettingsGeili()
}
func (s *SettingService) waitOpenAIEncryptedRouteSettingsGeili(ctx context.Context, pending *cachedOpenAIEncryptedRouteSettingsGeili) OpenAIEncryptedRouteSettingsGeili {
	remaining := time.Until(pending.waitUntil)
	if remaining <= 0 {
		return encryptedRouteSettingsFallbackGeili(pending)
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-pending.refreshing:
		return encryptedRouteSettingsFallbackGeili(s.encryptedRouteSettingsCache.Load())
	case <-ctx.Done():
	case <-timer.C:
	}
	return encryptedRouteSettingsFallbackGeili(pending)
}
func (s *SettingService) refreshOpenAIEncryptedRouteSettingsGeili(ctx context.Context, cancel context.CancelFunc, pending *cachedOpenAIEncryptedRouteSettingsGeili) {
	defer func() {
		cancel()
		s.encryptedRouteSettingsRefreshInFlight.Store(false)
		close(pending.refreshing)
	}()
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAIEncryptedRouteGeili)
	value := pending.settings
	if err == nil {
		value = ParseOpenAIEncryptedRouteSettingsGeili(raw)
	} else {
		if !errors.Is(err, ErrSettingNotFound) {
			encryptedRouteSettingsRefreshFailuresGeili.Add(1)
			slog.Warn("encrypted_route.settings_refresh_failed", "error_class", encryptedPersistenceFailureClassGeili(err))
		}
		if value.Mode == "" {
			value = DefaultOpenAIEncryptedRouteSettingsGeili()
		}
	}
	ready := &cachedOpenAIEncryptedRouteSettingsGeili{settings: value, expires: time.Now().Add(5 * time.Second)}
	// Invalidation detaches pending. Never publish a pre-update read into the new generation.
	s.encryptedRouteSettingsCache.CompareAndSwap(pending, ready)
}
