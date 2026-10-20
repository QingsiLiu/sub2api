package service

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/tidwall/gjson"
)

func isOpenAILunaModelGeili(model string) bool {
	switch normalizeKnownOpenAICodexModel(model) {
	case "gpt-6-luna", "gpt-5.6-luna":
		return true
	default:
		return false
	}
}

// Official Luna with reasoning disabled supports native Chat parameters, including
// caps below the Responses minimum of 16. Keep explicit protocol overrides and
// Responses-shaped requests on their configured path.
func useNativeLunaChatGeili(account *Account, body []byte, defaultMappedModel string) bool {
	if !account.IsOpenAIApiKey() || !isOfficialOpenAIModelsBaseURL(account.GetOpenAIBaseURL()) ||
		!gjson.GetBytes(body, "messages").Exists() || gjson.GetBytes(body, "reasoning_effort").String() != "none" {
		return false
	}
	mode, _ := account.Extra[openai_compat.ExtraKeyResponsesMode].(string)
	if openai_compat.NormalizeResponsesSupportMode(mode) != openai_compat.ResponsesSupportModeAuto {
		return false
	}
	model := normalizeOpenAIModelForUpstream(account, resolveOpenAIForwardModel(account, gjson.GetBytes(body, "model").String(), defaultMappedModel))
	return isOpenAILunaModelGeili(model)
}

func chatCompletionsToResponsesForAccountGeili(cfg *config.Config, account *Account, req *apicompat.ChatCompletionsRequest) (*apicompat.ResponsesRequest, error) {
	floor := configuredMinOutputTokens(cfg)
	if account.IsOpenAIApiKey() && req.MaxCompletionTokens != nil && *req.MaxCompletionTokens > 0 {
		// An explicit completion budget is a ceiling. Let the upstream validate
		// its minimum instead of silently increasing the client's cost budget.
		floor = 0
	}
	out, err := apicompat.ChatCompletionsToResponsesWithMinOutputTokens(req, floor)
	if err == nil && account.IsOpenAIApiKey() && isOpenAILunaModelGeili(req.Model) && req.ReasoningEffort == "none" {
		out.Temperature = req.Temperature
		out.TopP = req.TopP
	}
	return out, err
}
