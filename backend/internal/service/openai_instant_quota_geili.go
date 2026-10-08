package service

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// isOpenAIInstantInferenceQuotaError recognizes the provider's instantaneous
// inference quota, not the downstream user's balance or a permanent auth error.
// Read error fields only: quoted user input elsewhere in the payload is not evidence.
func isOpenAIInstantInferenceQuotaError(message string, body []byte) bool {
	for _, msg := range []string{message, gjson.GetBytes(body, "error.message").String(), gjson.GetBytes(body, "response.error.message").String(), gjson.GetBytes(body, "message").String()} {
		if strings.TrimSuffix(strings.ToLower(strings.TrimSpace(msg)), ".") == "insufficient quota available for instant inference" {
			return true
		}
	}
	return false
}

// Bypass the pool/OAuth same-account deferral only for this exact provider quota.
// Explicit admin temporary rules run before this fallback at the call site.
func (s *OpenAIGatewayService) cooldownOpenAIInstantInferenceQuota(ctx context.Context, account *Account, headers http.Header, body []byte) bool {
	if !isOpenAIInstantInferenceQuotaError("", body) || s == nil || s.rateLimitService == nil || account == nil || account.Platform != PlatformOpenAI {
		return false
	}
	cooldown, enabled := s.rateLimitService.get429FallbackCooldown(ctx, account)
	if !enabled {
		return true
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(headers.Get("Retry-After"))); err == nil && seconds > 0 {
		requested := time.Duration(clampRateLimit429CooldownSeconds(seconds)) * time.Second
		if requested > cooldown {
			cooldown = requested
		}
	}
	until := time.Now().Add(cooldown)
	s.BlockAccountScheduling(account, until, "openai_instant_quota")
	if s.rateLimitService.accountRepo != nil {
		if err := s.rateLimitService.accountRepo.SetRateLimited(ctx, account.ID, until); err != nil {
			slog.Warn("instant_quota_cooldown_persist_failed", "account_id", account.ID, "error", err)
		}
	}
	return true
}
