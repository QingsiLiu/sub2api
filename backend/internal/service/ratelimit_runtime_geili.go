package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

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
