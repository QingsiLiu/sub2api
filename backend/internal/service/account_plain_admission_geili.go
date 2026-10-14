package service

import (
	"context"

	"github.com/tidwall/gjson"
)

type finalAccountRuntimeAdmissionGeili interface {
	RequiresFinalAccountRuntimeAdmissionGeili() bool
}

// Plain Anthropic API keys with an unchanged model and no tools reach central
// HTTP dispatch without token refresh, model aliasing, plugins or web-search
// emulation. Its mandatory fresh PostgreSQL/Redis barrier is sufficient; do not
// perform the same PostgreSQL read once more before constructing that request.
// Every other path keeps the original early check, including WS/OAuth/tools.
func (s *GatewayService) checkPlainAnthropicAdmissionGeili(ctx context.Context, account *Account, parsed *ParsedRequest) error {
	model := ""
	if parsed != nil {
		model = parsed.Model
	}
	terminal, ok := s.httpUpstream.(finalAccountRuntimeAdmissionGeili)
	if ok && terminal.RequiresFinalAccountRuntimeAdmissionGeili() && account != nil &&
		account.Platform == PlatformAnthropic && account.Type == AccountTypeAPIKey && !account.IsBedrock() &&
		parsed != nil && parsed.Body != nil && model != "" && account.GetMappedModel(model) == model {
		body := parsed.Body.Bytes()
		if gjson.GetBytes(body, "model").String() == model && !gjson.GetBytes(body, "tools").Exists() {
			return nil
		}
	}
	return checkRuntimeBeforeForwardGeili(ctx, s.accountRepo, account, model)
}
