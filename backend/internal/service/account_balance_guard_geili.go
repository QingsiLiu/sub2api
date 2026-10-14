package service

// Supplier balance is an account-wide fact, independent of pool retry policies.
import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Only inspect supplier error fields, never echoed prompts or arbitrary HTML.
func supplierBalanceFailureGeili(body []byte) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	r := gjson.ParseBytes(body)
	code := strings.ToLower(r.Get("error.code").String())
	if code == "" {
		code = strings.ToLower(r.Get("response.error.code").String())
	}
	if code == "" {
		code = strings.ToLower(r.Get("code").String())
	}
	if code == "model_not_found" || code == "invalid_parameter" {
		return false
	}
	for _, path := range []string{"error.code", "response.error.code", "error.type", "code"} {
		switch strings.ToLower(r.Get(path).String()) {
		case "insufficient_balance", "insufficient_account_balance", "insufficient_credit", "insufficient_credits":
			return true
		}
	}
	for _, path := range []string{"error.message", "response.error.message", "message"} {
		v := r.Get(path)
		if v.Type != gjson.String {
			continue
		}
		msg := strings.ToLower(v.String())
		if strings.Contains(msg, "model") && (strings.Contains(msg, "not found") || strings.Contains(msg, "unknown model") || strings.Contains(msg, "does not exist")) {
			continue
		}
		if strings.Contains(msg, "模型") && (strings.Contains(msg, "不存在") || strings.Contains(msg, "不支持")) {
			continue
		}
		for _, marker := range []string{"insufficient balance", "insufficient account balance", "credit balance is too low", "credit balance exhausted", "balance is not enough", "no enough balance", "余额不足", "账户余额不足", "用户额度不足", "预扣费额度失败"} {
			if strings.Contains(msg, marker) {
				return true
			}
		}
	}
	return false
}

// Cline has independent credits, pass and free wallets. A wallet failure is
// not proof that every model or wallet on the credential is unavailable.
func supplierAccountBalanceFailureGeili(account *Account, status int, body []byte) bool {
	if account != nil && account.IsCline() {
		if status == http.StatusPaymentRequired {
			return false
		}
		if kind, _ := parseClineError(body); kind != clineErrorNone {
			return false
		}
		for _, path := range []string{"error.wallet.scope", "response.error.wallet.scope", "error.details.wallet.scope", "wallet.scope"} {
			scope := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, path).String()))
			switch scope {
			case "credits", "credit", "paid", "pass", "clinepass", "cline_pass", "cline-pass", "free", "cline_free", "cline-free":
				return false
			}
		}
	}
	return supplierBalanceFailureGeili(body)
}

func (s *RateLimitService) observeAccountResultGeili(ctx context.Context, c *gin.Context, a *Account, model string, err error) {
	if s == nil || a == nil || isAccountRecoveryProbeGeili(ctx) {
		return
	}
	// Debt is a supplier fact even if the customer disconnects after receiving
	// the error frame. Keep it outside health counters and their cancellation/
	// de-duplication gates; local admissions must not create a new observation.
	if isAccountRuntimeAdmissionFailureGeili(err) {
		return
	}
	var supplier *geiliUpstreamPayloadFailure
	if errors.As(err, &supplier) && s.stopSupplierBalanceGeili(ctx, a, supplier.status, supplier.payload) {
		return
	}
	var f *UpstreamFailoverError
	if errors.As(err, &f) && s.stopSupplierBalanceGeili(ctx, a, f.StatusCode, f.ResponseBody) {
		f.RetryableOnSameAccount = false
		f.NextAccountAction = NextAccountRetry
		return
	}
	if ctx.Err() != nil {
		return
	}
	if forwarded := AccountRuntimeForwardModelGeili(ctx); forwarded != "" {
		model = forwarded
	}
	model = strings.TrimSpace(model)
	r, ok := s.accountRepo.(AccountRuntimeRepositoryGeili)
	if !ok {
		return
	}
	if c != nil {
		key := "geili_health_observed_" + model
		if old, exists := c.Get(key); exists && old == a.ID {
			return
		}
		c.Set(key, a.ID)
	}
	if err == nil {
		_ = r.ResetAccountModelSuccessGeili(ctx, a.ID, model)
		return
	}
	// A local protocol validator rejection is not proof of supplier availability.
	if _, _, _, yes := AnthropicStreamDiagnosticGeili(err); yes {
		return
	}
	if errors.As(err, &f) {
		if gjson.GetBytes(f.ResponseBody, "error.code").String() == "upstream_tool_arguments_invalid" {
			return
		}
		if f.Scope == GatewayFailureScopeRequest || f.StatusCode < 500 {
			return
		}
		if !ClaimAccountModelFailureObservationGeili(ctx, a.ID, model) {
			return
		}
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = r.RecordModelFailureGeili(persist, a.ID, model, "temporary_upstream_failure", time.Now().Add(time.Minute))
	}
}

func (s *RateLimitService) stopSupplierBalanceGeili(ctx context.Context, account *Account, status int, body []byte) bool {
	if s == nil || account == nil || (status != 400 && status != 401 && status != 402 && status != 403 && status != 429 && status < 500) || !supplierAccountBalanceFailureGeili(account, status, body) {
		return false
	}
	ownerID := account.ID
	if account.ParentAccountID != nil && *account.ParentAccountID > 0 {
		ownerID = *account.ParentAccountID
	}
	r, hasRuntime := s.accountRepo.(AccountRuntimeRepositoryGeili)
	latch, hasLatch := s.tempUnschedCache.(AccountBalanceLatchCacheGeili)
	if !hasRuntime && !hasLatch {
		return false
	}
	alreadyBlocked := account.RuntimeState != nil && account.RuntimeState.BalanceBlocked
	alreadyPersisted := alreadyBlocked && account.RuntimeState.BalanceReason == "supplier_insufficient_balance"
	persisted := alreadyPersisted
	// The independent latch survives a failed PostgreSQL write. Duplicate hooks
	// in this attempt keep its token; a new observation from a fresh account
	// gets a new token so an older recovery grant cannot erase it.
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if hasLatch && !alreadyBlocked {
		if _, err := latch.SetAccountBalanceLatchGeili(persist, ownerID); err != nil {
			logger.LegacyPrintf("service.ratelimit", "supplier balance latch persistence failed account_id=%d", ownerID)
		}
	}
	// A failed database write leaves BalanceReason unset; duplicate hooks retry
	// it. Successful duplicates retain the version used by recovery's CAS.
	// Store only a fixed reason, never supplier/customer text.
	if hasRuntime && !alreadyPersisted {
		durable, cancelDurable := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancelDurable()
		if err := r.MarkAccountBalanceBlockedGeili(durable, ownerID, "supplier_insufficient_balance"); err != nil {
			logger.LegacyPrintf("service.ratelimit", "supplier balance runtime persistence failed account_id=%d", ownerID)
		} else {
			persisted = true
		}
	}
	account.Status, account.Schedulable = StatusError, false
	if account.RuntimeState == nil {
		account.RuntimeState = &AccountRuntimeStateGeili{AccountID: account.ID}
	}
	account.RuntimeState.BalanceBlocked = true
	if persisted {
		account.RuntimeState.BalanceReason = "supplier_insufficient_balance"
	} else {
		account.RuntimeState.BalanceReason = ""
	}
	account.RuntimeState.StopReason = "balance"
	return true
}

func accountRuntimeFailureGeili(reason string) error {
	payload := []byte(`{"error":{"type":"upstream_error","code":"account_unavailable","message":"Selected account is temporarily unavailable"}}`)
	if reason == "balance" {
		payload = []byte(`{"error":{"type":"upstream_error","code":"supplier_balance_exhausted","message":"Selected supplier account has insufficient balance"}}`)
	}
	return &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, ResponseBody: payload, SafeToFailoverAfterWrite: true, NextAccountAction: NextAccountRetry, Scope: GatewayFailureScopeAccount, Reason: accountRuntimeAdmissionReasonGeili}
}

func checkRuntimeBeforeForwardGeili(ctx context.Context, repo AccountRepository, account *Account, model string) error {
	if account == nil || isAccountRecoveryProbeGeili(ctx) {
		return nil
	}
	r, ok := repo.(AccountRuntimeRepositoryGeili)
	if !ok {
		return nil
	}
	verdict, err := r.CheckAccountRuntimeAdmissionGeili(ctx, account.ID, model)
	if err != nil || verdict == nil {
		return NewAccountRuntimeAdmissionErrorGeili("verification_unavailable")
	}
	if verdict != nil && !verdict.Allowed {
		return NewAccountRuntimeAdmissionErrorGeili(verdict.Reason)
	}
	return nil
}

// These context keys are private. HTTP clients cannot supply a probe exemption
// or a routing model through request JSON/headers.
type accountRuntimeForwardModelKeyGeili struct{}

func IsAccountRecoveryProbeGeili(ctx context.Context) bool { return isAccountRecoveryProbeGeili(ctx) }
func AccountRuntimeForwardModelGeili(ctx context.Context) string {
	model, _ := ctx.Value(accountRuntimeForwardModelKeyGeili{}).(string)
	return model
}
func NewAccountRuntimeAdmissionErrorGeili(reason string) error {
	if reason != "verification_unavailable" {
		return accountRuntimeFailureGeili(reason)
	}
	return &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, ResponseBody: []byte(`{"error":{"code":"account_verification_unavailable","message":"Account availability verification unavailable"}}`), Scope: GatewayFailureScopeRequest, NextAccountAction: NextAccountStop, Reason: accountRuntimeAdmissionReasonGeili}
}

const accountRuntimeAdmissionReasonGeili GatewayFailureReason = "account_runtime_admission"

func isAccountRuntimeAdmissionFailureGeili(err error) bool {
	var failure *UpstreamFailoverError
	return errors.As(err, &failure) && failure.Reason == accountRuntimeAdmissionReasonGeili
}

// A transport adapter must preserve an already classified local or supplier
// decision, including its scope and retry action, instead of inventing a 502.
func isUpstreamFailoverErrorGeili(err error) bool {
	var failure *UpstreamFailoverError
	return errors.As(err, &failure)
}
