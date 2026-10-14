package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"
)

type accountWSBalanceObservationKeyGeili struct{}
type accountWSBalanceObservationGeili struct{ blocked atomic.Bool }

func withAccountWSBalanceObservationGeili(ctx context.Context) context.Context {
	return context.WithValue(ctx, accountWSBalanceObservationKeyGeili{}, &accountWSBalanceObservationGeili{})
}

// Native WebSocket dispatch does not cross HTTPUpstream. Check the shared debt
// latch and current database verdict immediately before every request write,
// including retries on an existing connection.
func (s *OpenAIGatewayService) checkOpenAIWSRuntimeGeili(ctx context.Context, account *Account, payload []byte, fallbackModel string) error {
	if s == nil || account == nil || isAccountRecoveryProbeGeili(ctx) {
		return nil
	}
	if observation, ok := ctx.Value(accountWSBalanceObservationKeyGeili{}).(*accountWSBalanceObservationGeili); ok && observation.blocked.Load() {
		return NewAccountRuntimeAdmissionErrorGeili("balance")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if s.rateLimitService != nil {
		if latch, ok := s.rateLimitService.tempUnschedCache.(AccountBalanceLatchCacheGeili); ok {
			ownerID := account.ID
			if account.ParentAccountID != nil && *account.ParentAccountID > 0 {
				ownerID = *account.ParentAccountID
			}
			token, err := latch.GetAccountBalanceLatchGeili(checkCtx, ownerID)
			if err != nil {
				return NewAccountRuntimeAdmissionErrorGeili("verification_unavailable")
			}
			if token != "" {
				return NewAccountRuntimeAdmissionErrorGeili("balance")
			}
		}
	}
	model := AccountRuntimeForwardModelGeili(ctx)
	if model == "" {
		model = strings.TrimSpace(gjson.GetBytes(payload, "model").String())
	}
	if model == "" {
		model = strings.TrimSpace(fallbackModel)
	}
	return checkRuntimeBeforeForwardGeili(checkCtx, s.accountRepo, account, model)
}

// Inspect supplier-owned error fields before any downstream write. A customer
// disconnect cannot erase a debt observation and no existing output is replayed.
func (s *OpenAIGatewayService) observeOpenAIWSBalanceGeili(ctx context.Context, account *Account, payload []byte) bool {
	if s == nil || s.rateLimitService == nil || isAccountRecoveryProbeGeili(ctx) {
		return false
	}
	eventType := gjson.GetBytes(payload, "type").String()
	if eventType != "error" && eventType != "response.failed" {
		return false
	}
	status := openAIWSErrorHTTPStatus(payload)
	if status < 400 {
		status = http.StatusBadGateway
	}
	blocked := s.rateLimitService.stopSupplierBalanceGeili(ctx, account, status, payload)
	if blocked {
		if observation, ok := ctx.Value(accountWSBalanceObservationKeyGeili{}).(*accountWSBalanceObservationGeili); ok {
			observation.blocked.Store(true)
		}
	}
	return blocked
}

// The upgrade response can itself carry the supplier debt error before there
// is a WebSocket frame. Preserve classified admissions and store real debt.
func (s *OpenAIGatewayService) openAIWSDialAdmissionGeili(ctx context.Context, account *Account, err error) error {
	if isUpstreamFailoverErrorGeili(err) {
		return err
	}
	var dialErr *openAIWSDialError
	if s == nil || s.rateLimitService == nil || !errors.As(err, &dialErr) || dialErr == nil {
		return nil
	}
	if s.rateLimitService.stopSupplierBalanceGeili(ctx, account, dialErr.StatusCode, dialErr.ResponseBody) {
		return NewAccountRuntimeAdmissionErrorGeili("balance")
	}
	return nil
}
