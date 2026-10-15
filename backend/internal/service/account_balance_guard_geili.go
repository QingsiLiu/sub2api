package service

// Supplier balance is an account-wide fact, independent of pool retry policies.
import (
	"context"
	"errors"
	"net/http"
	"strings"

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

func accountRuntimeFailureGeili(reason string) error {
	payload := []byte(`{"error":{"type":"upstream_error","code":"account_unavailable","message":"Selected account is temporarily unavailable"}}`)
	if reason == "balance" {
		payload = []byte(`{"error":{"type":"upstream_error","code":"supplier_balance_exhausted","message":"Selected supplier account has insufficient balance"}}`)
	}
	return &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, ResponseBody: payload, SafeToFailoverAfterWrite: true, NextAccountAction: NextAccountRetry, Scope: GatewayFailureScopeAccount, Reason: accountRuntimeAdmissionReasonGeili}
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
