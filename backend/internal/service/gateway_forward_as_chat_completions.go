package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// ForwardAsChatCompletions accepts an OpenAI Chat Completions API request body,
// converts it to Anthropic Messages format (chained via Responses format),
// forwards to the Anthropic upstream, and converts the response back to Chat
// Completions format. This enables Chat Completions clients to access Anthropic
// models through Anthropic platform groups.
func (s *GatewayService) ForwardAsChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *ParsedRequest,
) (out *ForwardResult, err error) {
	startTime := time.Now()

	// 1. Parse Chat Completions request
	var ccReq apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &ccReq); err != nil {
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := ccReq.Model
	clientStream := ccReq.Stream
	// geili hook: keep each failed attempt's prelude and headers private.
	recoveryModel := originalModel
	finish := BeginTextForwardGuard(c, clientStream)
	defer func() {
		if out != nil && out.ClientDisconnect {
			GeiliMarkTextClientDisconnect(c)
		}
		err = finish(err)
		s.recordTextSupplierFailureGeili(ctx, c, account, recoveryModel, err)
		var failover *UpstreamFailoverError
		if errors.As(err, &failover) && failover.SafeToFailoverAfterWrite {
			out = nil
		}
	}()

	// 2. Convert CC → Responses → Anthropic (chained conversion)
	responsesReq, err := apicompat.ChatCompletionsToResponses(&ccReq)
	if err != nil {
		return nil, fmt.Errorf("convert chat completions to responses: %w", err)
	}

	// Resolve the final upstream model before model-specific conversion.
	mappedModel := originalModel
	if account.Type == AccountTypeAPIKey || account.Type == AccountTypeServiceAccount {
		mappedModel = account.GetMappedModel(originalModel)
	}
	if mappedModel == originalModel && account.Platform == PlatformAnthropic && account.Type == AccountTypeServiceAccount {
		normalized := normalizeVertexAnthropicModelID(claude.NormalizeModelID(originalModel))
		if normalized != originalModel {
			mappedModel = normalized
		}
	} else if mappedModel == originalModel && account.Platform == PlatformAnthropic && account.Type != AccountTypeAPIKey {
		normalized := claude.NormalizeModelID(originalModel)
		if normalized != originalModel {
			mappedModel = normalized
		}
	}
	recoveryModel = mappedModel
	if err := validateClaude55Request(body, mappedModel); err != nil {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	responsesReq.Model = mappedModel
	anthropicReq, err := apicompat.ResponsesToAnthropicRequest(responsesReq)
	if err != nil {
		if isClaude55SignedThinkingModel(mappedModel) {
			writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		}
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}

	// 3. Force upstream streaming
	anthropicReq.Stream = true
	reqStream := true

	logger.L().Debug("gateway forward_as_chat_completions: model mapping applied",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("mapped_model", mappedModel),
		zap.Bool("client_stream", clientStream),
	)

	// 5. Marshal Anthropic request body
	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	// 6. Apply Claude Code mimicry for OAuth accounts.
	// Chat Completions 协议进来的请求永远不是 Claude Code 客户端，所以对 OAuth 账号
	// 必须完整执行 /v1/messages 主路径上的伪装链路（system 重写 + normalize + metadata 注入），
	// 否则会被 Anthropic 判为第三方应用并扣 extra usage。
	// 见 applyClaudeCodeOAuthMimicryToBody 的 godoc。
	isClaudeCode := false
	shouldMimicClaudeCode := account.IsOAuth() && !isClaudeCode

	if shouldMimicClaudeCode {
		anthropicBody = s.applyClaudeCodeOAuthMimicryToBody(ctx, c, account, anthropicBody, anthropicReq.System, mappedModel)
	}

	// 7. Enforce cache_control block limit
	anthropicBody = enforceCacheControlLimit(anthropicBody)

	// 8. Get access token
	token, tokenType, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	// 9. Get proxy URL
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	// 10. Build upstream request
	upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, reqStream)
	upstreamReq, forwardedBody, err := s.buildUpstreamRequest(upstreamCtx, c, account, anthropicBody, token, tokenType, mappedModel, reqStream, shouldMimicClaudeCode)
	defer releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	// Bill the final Anthropic effort after conversion and account normalization.
	// For example, OpenAI xhigh is forwarded as output_config.effort=max.
	reasoningEffort := NormalizeClaudeOutputEffort(gjson.GetBytes(forwardedBody, "output_config.effort").String())
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, forwardedBody, mappedModel)

	// 11. Send request
	resp, err := s.httpUpstream.DoWithTLS(upstreamReq, proxyURL, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, s.handleUpstreamTransportError(ctx, c, account, err, OpsUpstreamErrorEvent{
			UpstreamURL: safeUpstreamURL(upstreamReq.URL.String()),
		})
	}
	defer func() { _ = resp.Body.Close() }()

	// 12. Handle error response with failover
	if resp.StatusCode >= 400 {
		respBody, _ := s.readUpstreamErrorBody(resp)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)

		if s.shouldFailoverUpstreamError(resp.StatusCode) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				ProxyID:            opsUpstreamProxyID(account),
				ProxyName:          opsUpstreamProxyName(account),
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
			shouldDisable := false
			if s.rateLimitService != nil {
				shouldDisable = s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
			}
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
			}
		}

		writeGatewayCCError(c, mapUpstreamStatusCode(resp.StatusCode), "server_error", upstreamMsg)
		return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
	}

	// 13. Handle normal response
	// Read Anthropic SSE → convert to Responses events → convert to CC format
	var result *ForwardResult
	var handleErr error
	if clientStream {
		result, handleErr = s.handleCCStreamingFromAnthropic(resp, c, originalModel, mappedModel, reasoningEffort, startTime)
	} else {
		result, handleErr = s.handleCCBufferedFromAnthropic(resp, c, originalModel, mappedModel, reasoningEffort, startTime)
	}

	return result, handleErr
}

// extractCCReasoningEffortFromBody reads reasoning effort from a Chat Completions
// request body. It checks both nested (reasoning.effort) and flat (reasoning_effort)
// formats used by OpenAI-compatible clients.
func extractCCReasoningEffortFromBody(body []byte, modelCandidates ...string) *string {
	raw := strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String())
	if raw == "" {
		raw = strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	}
	if raw == "" {
		return nil
	}
	model := firstNonEmpty(modelCandidates...)
	if model == "" {
		model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	}
	normalized := normalizeOpenAIReasoningEffortForModel(raw, model)
	if normalized == "" {
		return nil
	}
	return &normalized
}

// handleCCBufferedFromAnthropic reads Anthropic SSE events, assembles the full
// response, then converts Anthropic → Responses → Chat Completions.
func (s *GatewayService) handleCCBufferedFromAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
) (*ForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	// geili hook: validate completion before constructing a successful JSON body.
	tracker, err := readAnthropicCompletionGeili(c, resp, s.cfg, mappedModel, false, nil)
	if err != nil {
		return nil, err
	}
	finalResp := tracker.response
	usage := tracker.usage

	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		finalResp.Usage = apicompat.AnthropicUsage{
			InputTokens:              usage.InputTokens,
			OutputTokens:             usage.OutputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
		}
	}

	// Chain: Anthropic → Responses → Chat Completions
	responsesResp := apicompat.AnthropicToResponsesResponse(finalResp)
	ccResp := apicompat.ResponsesToChatCompletions(responsesResp, originalModel)

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	// 非流式响应必须是 application/json。上游被强制流式后会返回
	// Content-Type: text/event-stream，经 WriteFilteredHeaders 透传后会污染
	// 响应头；而 c.Data/c.JSON 走 Gin 的 writeContentType（仅当头不存在时才设置），
	// 无法覆盖已存在的 SSE 头。这里显式 Set 强制改回 JSON，避免下游中间层
	// （如 new-api）按 Content-Type 误判为流式。
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Marshal then bytes-replace so tool name mapping is reversed at byte level
	// (parity with Parrot non-stream flow that marshals → restore → emit).
	if respBytes, err := json.Marshal(ccResp); err == nil {
		respBytes = reverseToolNamesIfPresent(c, respBytes)
		c.Data(http.StatusOK, "application/json; charset=utf-8", respBytes)
	} else {
		c.JSON(http.StatusOK, ccResp)
	}

	return &ForwardResult{
		RequestID:       requestID,
		UpstreamHeaders: resp.Header,
		Usage:           usage,
		Model:           originalModel,
		UpstreamModel:   mappedModel,
		ReasoningEffort: reasoningEffort,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

// handleCCStreamingFromAnthropic reads Anthropic SSE events, converts each
// to Responses events, then to Chat Completions chunks, and writes them.
func (s *GatewayService) handleCCStreamingFromAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
) (*ForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	// Use Anthropic→Responses state machine, then convert Responses→CC
	anthState := apicompat.NewAnthropicEventToResponsesState()
	anthState.Model = originalModel
	ccState := apicompat.NewResponsesEventToChatState()
	ccState.Model = originalModel

	var usage ClaudeUsage
	var firstTokenMs *int
	firstChunk := true
	clientDisconnected := false

	resultWithUsage := func() *ForwardResult {
		return &ForwardResult{
			RequestID:        requestID,
			UpstreamHeaders:  resp.Header,
			Usage:            usage,
			Model:            originalModel,
			UpstreamModel:    mappedModel,
			ReasoningEffort:  reasoningEffort,
			Stream:           true,
			Duration:         time.Since(startTime),
			FirstTokenMs:     firstTokenMs,
			ClientDisconnect: clientDisconnected,
		}
	}

	writeChunk := func(chunk apicompat.ChatCompletionsChunk) bool {
		if clientDisconnected || (c.Request != nil && errors.Is(c.Request.Context().Err(), context.Canceled)) {
			clientDisconnected = true
			GeiliMarkTextClientDisconnect(c)
			return true
		}
		sse, err := apicompat.ChatChunkToSSE(chunk)
		if err != nil {
			return false
		}
		// Reverse tool name mapping: fake → real, per-chunk bytes.Replace.
		// c 可能持有请求侧注入的 ToolNameRewrite；无则仅做静态前缀还原。
		out := string(reverseToolNamesIfPresent(c, []byte(sse)))
		if _, err := fmt.Fprint(c.Writer, out); err != nil {
			clientDisconnected = true
			GeiliMarkTextClientDisconnect(c)
			return true // client disconnected
		}
		return false
	}

	processAnthropicEvent := func(event *apicompat.AnthropicStreamEvent) bool {
		if event == nil {
			return false
		}
		// Drop Anthropic keepalive pings before OpenAI conversion:
		// leaking `event: ping` frames crashes OpenAI-stream clients.
		// Error events must still forward — they carry upstream failures.
		if event.Type == "ping" {
			return false
		}
		if firstChunk && anthropicEventHasSemanticOutputGeili(event) {
			firstChunk = false
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}

		// Extract usage from message_delta
		if event.Type == "message_delta" && event.Usage != nil {
			mergeAnthropicUsage(&usage, *event.Usage)
		}
		// Also capture usage from message_start (carries cache fields)
		if event.Type == "message_start" && event.Message != nil {
			mergeAnthropicUsage(&usage, event.Message.Usage)
		}

		// Keep the outward Responses/Chat usage on the same normalized buckets used
		// for billing, including converter handlers that consume event usage.
		syncAnthropicResponsesUsage(anthState, usage)
		normalizeAnthropicEventUsageForResponses(event, usage)

		// Chain: Anthropic event → Responses events → CC chunks
		responsesEvents := apicompat.AnthropicEventToResponsesEvents(event, anthState)
		for _, resEvt := range responsesEvents {
			ccChunks := apicompat.ResponsesEventToChatChunks(&resEvt, ccState)
			for _, chunk := range ccChunks {
				if disconnected := writeChunk(chunk); disconnected {
					return true
				}
			}
		}
		c.Writer.Flush()
		return false
	}

	// geili hook: transport errors and truncated streams must not be finalized.
	_, readErr := readAnthropicCompletionGeili(c, resp, s.cfg, mappedModel, true, func(event *apicompat.AnthropicStreamEvent, raw string) error {
		ccState.IncludeUsage = ccState.IncludeUsage || anthropicChatStreamHasUsage(event, raw)

		processAnthropicEvent(event)
		return nil
	})
	if readErr != nil {
		return resultWithUsage(), readErr
	}

	// Finalize both state machines
	finalResEvents := apicompat.FinalizeAnthropicResponsesStream(anthState)
	for _, resEvt := range finalResEvents {
		ccChunks := apicompat.ResponsesEventToChatChunks(&resEvt, ccState)
		for _, chunk := range ccChunks {
			writeChunk(chunk) //nolint:errcheck
		}
	}
	finalCCChunks := apicompat.FinalizeResponsesChatStream(ccState)
	for _, chunk := range finalCCChunks {
		writeChunk(chunk) //nolint:errcheck
	}

	// Write [DONE] marker
	if !clientDisconnected {
		if _, err := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); err != nil {
			clientDisconnected = true
			GeiliMarkTextClientDisconnect(c)
		} else {
			c.Writer.Flush()
		}
	}

	return resultWithUsage(), nil
}

// writeGatewayCCError writes an error in OpenAI Chat Completions format for
// the Anthropic-upstream CC forwarding path.
func writeGatewayCCError(c *gin.Context, statusCode int, errType, message string) {
	MarkResponseCommitted(c)
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// anthropicChatStreamHasUsage distinguishes an explicit zero-valued usage object
// from omitted/null usage before the Anthropic→Responses conversion loses that distinction.
func anthropicChatStreamHasUsage(event *apicompat.AnthropicStreamEvent, payload string) bool {
	switch event.Type {
	case "message_start":
		return event.Message != nil && gjson.Get(payload, "message.usage").IsObject()
	case "message_delta":
		return event.Usage != nil
	default:
		return false
	}
}
