package service

import (
	"context"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// The Images API also wraps explicit supplier balance codes in error.cause.
// Preserve that existing strict classification while storing the shared debt
// fact; arbitrary message text and request echoes are never normalized here.
func (s *OpenAIGatewayService) observeOpenAIImagesSupplierBalanceGeili(ctx context.Context, account *Account, status int, body []byte) bool {
	if s == nil || s.rateLimitService == nil || !isOpenAIImagesInsufficientBalance(body) {
		return false
	}
	if s.rateLimitService.stopSupplierBalanceGeili(ctx, account, status, body) {
		return true
	}
	// Cline's rejected wallet signals must keep their provider-specific policy.
	// Also retain top-level model/request exclusions before adding that field.
	if account != nil && account.IsCline() {
		return false
	}
	switch strings.ToLower(gjson.GetBytes(body, "code").String()) {
	case "model_not_found", "invalid_parameter":
		return false
	}
	// Preserve scoped wallet/model exclusions in the original error. Replacing
	// the whole body would discard those fields and overstate account-wide debt.
	normalized, err := sjson.SetBytes(body, "code", OpenAIImagesInsufficientBalanceCode)
	if err != nil {
		return false
	}
	return s.rateLimitService.stopSupplierBalanceGeili(ctx, account, status, normalized)
}
