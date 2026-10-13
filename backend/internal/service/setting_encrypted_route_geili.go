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

// Hot reads are lock-free. One reader refreshes expired policy; peers use the
// last validated policy, or wait within their own 50 ms budget on cold startup.
// A database outage never serializes requests behind a slow refresh mutex.
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
			select {
			case <-old.refreshing:
				current := s.encryptedRouteSettingsCache.Load()
				if current != nil && current.settings.Mode != "" {
					return cloneEncryptedRouteSettingsGeili(current.settings)
				}
				return DefaultOpenAIEncryptedRouteSettingsGeili()
			case <-budget.Done():
				return DefaultOpenAIEncryptedRouteSettingsGeili()
			}
		}
		if budget.Err() != nil {
			if old != nil && old.settings.Mode != "" {
				return cloneEncryptedRouteSettingsGeili(old.settings)
			}
			return DefaultOpenAIEncryptedRouteSettingsGeili()
		}
		pending := &cachedOpenAIEncryptedRouteSettingsGeili{refreshing: make(chan struct{})}
		if old != nil {
			pending.settings = old.settings
		}
		if !s.encryptedRouteSettingsCache.CompareAndSwap(old, pending) {
			continue
		}
		raw, err := s.settingRepo.GetValue(budget, SettingKeyOpenAIEncryptedRouteGeili)
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
		s.encryptedRouteSettingsCache.CompareAndSwap(pending, ready)
		close(pending.refreshing)
		return cloneEncryptedRouteSettingsGeili(value)
	}
}
