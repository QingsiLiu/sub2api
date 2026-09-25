package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const openAIToolProtocolFailureReason GatewayFailureReason = "responses_required"

type openAIToolProtocolContextKey struct{}

type openAIToolProtocolRequirement struct {
	hasTools             bool
	effort               string
	messagesModel        string
	messagesDefaultModel string
}

// WithOpenAIToolProtocolRequirement captures request semantics once, after group
// policy. Each candidate is checked against its mapped upstream model, so public
// aliases cannot hide GPT-6's Responses requirement from either scheduler.
func WithOpenAIToolProtocolRequirement(ctx context.Context, body []byte) context.Context {
	return context.WithValue(ctx, openAIToolProtocolContextKey{}, openAIToolProtocolRequirementFromBody(body))
}

// WithOpenAIChatToolProtocolRequirement follows the same body-shape detection
// as ForwardAsChatCompletions, including Responses-shaped compatibility clients.
func WithOpenAIChatToolProtocolRequirement(ctx context.Context, body []byte) context.Context {
	req := openAIToolProtocolRequirementFromBody(body)
	if gjson.GetBytes(body, "messages").Exists() || !gjson.GetBytes(body, "input").Exists() {
		req.effort = gjson.GetBytes(body, "reasoning_effort").String()
	}
	return context.WithValue(ctx, openAIToolProtocolContextKey{}, req)
}

// WithOpenAIMessagesToolProtocolRequirement mirrors the Messages bridge's
// channel -> normalized model -> account/default mapping and effort conversion.
func WithOpenAIMessagesToolProtocolRequirement(ctx context.Context, body []byte, forwardModel, defaultModel string) context.Context {
	req := openAIToolProtocolRequirementFromBody(body)
	anthropic := &apicompat.AnthropicRequest{Model: forwardModel}
	if effort := gjson.GetBytes(body, "output_config.effort").String(); effort != "" {
		anthropic.OutputConfig = &apicompat.AnthropicOutputConfig{Effort: effort}
	}
	applyOpenAICompatModelNormalization(anthropic)
	chat, _ := apicompat.AnthropicToChatCompletionsRequest(anthropic)
	req.effort = chat.ReasoningEffort
	req.messagesModel = anthropic.Model
	req.messagesDefaultModel = defaultModel
	return context.WithValue(ctx, openAIToolProtocolContextKey{}, req)
}

func openAIToolProtocolRequirementFromBody(body []byte) openAIToolProtocolRequirement {
	fields := gjson.GetManyBytes(body, "tools", "functions", "reasoning.effort", "reasoning_effort", "output_config.effort")
	req := openAIToolProtocolRequirement{
		hasTools: len(fields[0].Array()) > 0 || len(fields[1].Array()) > 0,
	}
	for _, field := range fields[2:] {
		if effort := strings.TrimSpace(field.String()); effort != "" {
			req.effort = effort
			break
		}
	}
	if !req.hasTools {
		// Codex can declare runtime tools in input[].additional_tools instead
		// of tools. These are lowered to functions by the Chat bridge too.
		gjson.GetBytes(body, "input").ForEach(func(_, item gjson.Result) bool {
			if item.Get("type").String() == "additional_tools" && len(item.Get("tools").Array()) > 0 {
				req.hasTools = true
			}
			return !req.hasTools
		})
	}
	return req
}

func (r openAIToolProtocolRequirement) requiresResponses(model string) bool {
	if !r.hasTools {
		return false
	}
	// Keep Astra tool calls on Responses without disabling client reasoning.
	// Preserve Sol/Luna's existing Chat exception for explicit effort=none.
	return isOpenAIGPT6AstraModel(model) ||
		(openai.IsGPT6SolOrLunaModelSpelling(model) && r.effort != "none")
}

func openAIAccountSupportsToolProtocol(ctx context.Context, account *Account, requestedModel string) bool {
	if account == nil || account.Platform != PlatformOpenAI {
		return true
	}
	req, ok := ctx.Value(openAIToolProtocolContextKey{}).(openAIToolProtocolRequirement)
	if !ok || !req.hasTools {
		return true
	}
	model := resolveOpenAIAccountUpstreamModelForRequest(account, requestedModel, false)
	if req.messagesModel != "" {
		model = normalizeOpenAIModelForUpstream(account, resolveOpenAIForwardModel(account, req.messagesModel, req.messagesDefaultModel))
		// Only none can allow a GPT-6 Chat route; resolve a Messages policy that
		// can map it back to reasoning before admitting that candidate.
		if req.effort == "none" {
			policyBody, _ := json.Marshal(map[string]string{"model": model, "reasoning_effort": req.effort})
			if normalized, _, err := ApplyOpenAIReasoningEffortPolicyFromContext(ctx, policyBody); err == nil {
				req.effort = gjson.GetBytes(normalized, "reasoning_effort").String()
			}
		}
	}
	return !req.requiresResponses(model) || account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses)
}

// guardOpenAIChatToolProtocol is the final wire-level check shared by raw Chat,
// Responses fallback and Messages fallback. Return before writing any response
// or sending a request, allowing the handler to select another account without
// changing the client's tools, model or reasoning effort.
func guardOpenAIChatToolProtocol(c *gin.Context, account *Account, body []byte) error {
	if account == nil || account.Platform != PlatformOpenAI {
		return nil
	}
	model := gjson.GetBytes(body, "model").String()
	req := openAIToolProtocolRequirementFromBody(body)
	req.effort = gjson.GetBytes(body, "reasoning_effort").String()
	if !req.requiresResponses(model) {
		return nil
	}
	const message = "This model requires Responses for tool calls; select a Responses-capable account"
	responseBody, _ := json.Marshal(map[string]any{"error": map[string]string{
		"type": "upstream_error", "code": "responses_required", "message": message,
	}})
	if c != nil {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
			Kind: "protocol_mismatch", Message: message,
		})
	}
	return &UpstreamFailoverError{
		StatusCode: http.StatusServiceUnavailable, ResponseBody: responseBody,
		Scope: GatewayFailureScopeRequest, Reason: openAIToolProtocolFailureReason, NextAccountAction: NextAccountRetry,
	}
}
