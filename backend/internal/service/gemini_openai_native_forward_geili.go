package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
)

// ShouldUseGeminiOpenAINativeGeili confines this adapter to text accounts.
// Media keeps its existing independent pricing and generation contract.
func ShouldUseGeminiOpenAINativeGeili(account *Account, model string) bool {
	return account != nil && account.Platform == PlatformGemini && !isImageGenerationModel(model) && !isImageGenerationModel(account.GetMappedModel(model))
}

// ForwardAsResponsesGeili uses Gemini native model actions for Responses clients.
func (s *GeminiMessagesCompatService) ForwardAsResponsesGeili(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	return s.forwardOpenAINativeGeili(ctx, c, account, body, false)
}
func (s *GeminiMessagesCompatService) forwardOpenAINativeGeili(ctx context.Context, c *gin.Context, account *Account, body []byte, chat bool) (out *ForwardResult, err error) {
	start := time.Now()
	if account == nil || account.Platform != PlatformGemini {
		return nil, s.writeChatCompletionsError(c, 400, "invalid_request_error", "Gemini account required")
	}
	var base struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if json.Unmarshal(body, &base) != nil {
		return nil, s.writeChatCompletionsError(c, 400, "invalid_request_error", "Invalid request body")
	}
	if !ShouldUseGeminiOpenAINativeGeili(account, base.Model) {
		return nil, s.writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "Gemini native OpenAI compatibility supports text models only")
	}
	mapped := base.Model
	if account.Type == AccountTypeAPIKey || account.Type == AccountTypeServiceAccount {
		mapped = account.GetMappedModel(base.Model)
	}
	request, err := parseGeminiOpenAIRequestGeili(body, chat, mapped)
	if err != nil {
		return nil, s.writeChatCompletionsError(c, 400, "invalid_request_error", err.Error())
	}
	ctx = context.WithValue(ctx, accountRuntimeForwardModelKeyGeili{}, mapped)
	finish := BeginTextForwardGuard(c, request.stream)
	defer func() {
		if out != nil && out.ClientDisconnect {
			GeiliMarkTextClientDisconnect(c)
		}
		err = finish(err)
		var failover *UpstreamFailoverError
		if errors.As(err, &failover) && failover.SafeToFailoverAfterWrite {
			out = nil
		}
	}()
	ctx, cancelRecovery := RequestRecoveryContext(ctx)
	defer cancelRecovery()
	proxy := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	useStream := request.stream || (account.Type == AccountTypeOAuth && strings.TrimSpace(account.GetCredential("project_id")) != "")
	build, _ := s.buildGeminiChatCompletionsUpstreamRequestFunc(account, mapped, request.body, request.stream, useStream)
	var resp *http.Response
	var upstreamURL, requestIDHeader string
	var checkedPolicy *ErrorPolicyResult
	for attempt := 1; attempt <= geminiMaxRetries; attempt++ {
		upstream, idHeader, buildErr := build(ctx)
		if buildErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if failover := s.handleVertexServiceAccountNotFound(ctx, c, account, buildErr); failover != nil {
				return nil, failover
			}
			return nil, s.writeChatCompletionsError(c, 502, "upstream_error", "Failed to construct Gemini upstream request")
		}
		upstreamURL = upstream.URL.String()
		requestIDHeader = idHeader
		SetActualOpenAIUpstreamEndpoint(c, upstream.URL.Path)
		SetGeminiActualUpstreamURLGeili(c, upstream.URL.String())
		resp, err = s.httpUpstream.Do(upstream, proxy, account.ID, account.Concurrency)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			return nil, s.handleUpstreamTransportError(ctx, c, account, err)
		}
		SetGeminiUpstreamResponseHeadersGeili(c, resp.Header)
		if failover, rebuilt := s.geminiSupplierFailureResponseGeili(ctx, c, account, resp, upstreamURL, mapped); failover != nil {
			return nil, failover
		} else {
			resp = rebuilt
		}
		checked, rebuilt := s.checkErrorPolicyInLoop(ctx, account, resp, mapped)
		resp, checkedPolicy = rebuilt, checked
		if checked != nil && *checked != ErrorPolicyNone {
			break
		}
		if resp.StatusCode < 400 || !s.shouldRetryGeminiUpstreamError(account, resp.StatusCode) || attempt == geminiMaxRetries {
			break
		}
		errorBody := s.readUpstreamErrorBody(resp)
		if resp.StatusCode == http.StatusForbidden && isGeminiInsufficientScope(resp.Header, errorBody) {
			resp.Body = io.NopCloser(strings.NewReader(string(errorBody)))
			break
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			s.handleGeminiUpstreamError(ctx, account, resp.StatusCode, resp.Header, errorBody)
		}
		_ = resp.Body.Close()
		setOpsUpstreamError(c, resp.StatusCode, sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(errorBody)), "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: account.Platform, AccountID: account.ID, AccountName: account.Name, UpstreamURL: safeUpstreamURL(upstreamURL), UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: resp.Header.Get(requestIDHeader), Kind: "retry", Message: sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(errorBody))})
		backoff := geminiRetryBaseDelay * time.Duration(1<<uint(attempt-1))
		if backoff > geminiRetryMaxDelay {
			backoff = geminiRetryMaxDelay
		}
		jitter := time.Duration(float64(backoff) * 0.2 * (mathrand.Float64()*2 - 1))
		delay := time.NewTimer(backoff + jitter)
		select {
		case <-ctx.Done():
			delay.Stop()
			return nil, ctx.Err()
		case <-delay.C:
		}
	}
	if resp == nil {
		return nil, fmt.Errorf("Gemini upstream returned no response")
	}
	defer resp.Body.Close()
	requestID := resp.Header.Get(requestIDHeader)
	if requestID == "" {
		requestID = resp.Header.Get("x-goog-request-id")
	}
	if requestID != "" {
		c.Header("x-request-id", requestID)
	}
	if resp.StatusCode >= 400 {
		errorBody := s.readUpstreamErrorBody(resp)
		errorBody = unwrapIfNeeded(account.Type == AccountTypeOAuth, errorBody)
		policy := ErrorPolicyNone
		if checkedPolicy != nil {
			policy = *checkedPolicy
		} else if s.rateLimitService != nil {
			policy = s.rateLimitService.CheckErrorPolicy(ctx, account, resp.StatusCode, errorBody, mapped)
		}
		if policy == ErrorPolicyNone || policy == ErrorPolicyMatched {
			s.handleGeminiUpstreamError(ctx, account, resp.StatusCode, resp.Header, errorBody)
		}
		if s.shouldFailoverGeminiUpstreamError(resp.StatusCode) {
			setOpsUpstreamError(c, resp.StatusCode, sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(errorBody)), "")
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: account.Platform, AccountID: account.ID, AccountName: account.Name, UpstreamURL: safeUpstreamURL(upstreamURL), UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: requestID, Kind: "http_error", Message: sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(errorBody))})
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: errorBody, RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)}
		}
		if policy == ErrorPolicySkipped && account.IsCustomErrorCodesEnabled() {
			return nil, s.writeGeminiCustomCodeSkippedError(c, account, resp.StatusCode, requestID, errorBody, func() { _ = s.writeChatCompletionsError(c, 500, "api_error", geminiCustomCodeSkippedClientMessage) })
		}
		return nil, s.writeGeminiChatCompletionsMappedError(c, account, resp.StatusCode, requestID, errorBody)
	}
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	var output *geminiOpenAIOutputGeili
	var emit func(apicompat.ResponsesStreamEvent) error
	if request.stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		restorer := apicompat.NewResponsesClientToolStreamRestorer(request.mapping)
		chatState := apicompat.NewResponsesEventToChatState()
		chatState.Model = request.model
		chatState.IncludeUsage = request.includeUsage
		emit = func(event apicompat.ResponsesStreamEvent) error {
			if geiliTextClientDisconnected(c) {
				return nil
			}
			events := []apicompat.ResponsesStreamEvent{event}
			if !chat {
				events = restorer.Restore(event)
			}
			for _, e := range events {
				if chat {
					// A provider may deliver its signature after the initial call
					// delta. Emit a metadata-only final delta so it survives replay.
					if e.Type == "response.output_item.done" && e.Item != nil && e.Item.Type == "function_call" {
						if sig := output.signatures[e.Item.CallID]; sig != "" {
							idx := chatState.OutputIndexToToolIndex[e.OutputIndex]
							extra := map[string]any{"id": output.response.ID, "object": "chat.completion.chunk", "created": output.response.CreatedAt, "model": request.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": idx, "gemini_thought_signature": sig}}}, "finish_reason": nil}}}
							data, _ := json.Marshal(extra)
							if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
								return err
							}
						}
					}
					for _, chunk := range apicompat.ResponsesEventToChatChunks(&e, chatState) {
						b, err := geminiOpenAISignedPayloadGeili(chunk, output.signatures)
						if err != nil {
							return err
						}
						if _, err = fmt.Fprintf(c.Writer, "data: %s\n\n", b); err != nil {
							return err
						}
					}
				} else {
					b, err := geminiOpenAISignedPayloadGeili(e, output.signatures)
					if err != nil {
						return err
					}
					if _, err = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", e.Type, b); err != nil {
						return err
					}
				}
			}
			c.Writer.Flush()
			return nil
		}
	}
	output = newGeminiOpenAIOutputGeili(request.model, start, emit)
	if err = output.begin(); err != nil {
		return nil, err
	}
	if useStream {
		err = s.readOpenAIGeminiSSEGeili(ctx, c, resp, mapped, request.stream, output)
	} else {
		var data []byte
		data, err = ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
		if err == nil {
			err = output.consume(data)
		}
	}
	result := func() *ForwardResult {
		return &ForwardResult{RequestID: requestID, UpstreamHeaders: resp.Header, Usage: output.usage, Model: request.model, UpstreamModel: mapped, Stream: request.stream, Duration: time.Since(start), FirstTokenMs: output.firstTokenMs, ReasoningEffort: extractGeminiReasoningEffortFromBody(request.body), ClientDisconnect: c.Request.Context().Err() != nil}
	}
	// geili hook: retain observed provider usage on client abort in our native
	// text adapter too. Never synthesize a successful terminal for a gone client.
	if geiliTextClientDisconnected(c) {
		GeiliMarkTextClientDisconnect(c)
		if output.usage.hasObservedTokens() {
			return result(), nil
		}
		if c.Request != nil && c.Request.Context().Err() != nil {
			return nil, c.Request.Context().Err()
		}
		return nil, err
	}
	if err == nil {
		err = output.finalize()
	}
	if err != nil {
		if ctx.Err() != nil {
			return result(), ctx.Err()
		}
		message := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, http.StatusBadGateway, message, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: account.Platform, AccountID: account.ID, AccountName: account.Name, ProxyID: opsUpstreamProxyID(account), ProxyName: opsUpstreamProxyName(account), UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: requestID, UpstreamURL: safeUpstreamURL(upstreamURL), Kind: "stream_error", Message: message})
		if !output.semantic || !request.stream {
			status := http.StatusBadGateway
			kind := "upstream_error"
			if output.signal != nil {
				status = output.signal.Status
				if status == http.StatusBadRequest {
					kind = "invalid_request_error"
				}
			}
			payload, _ := json.Marshal(map[string]any{"error": map[string]any{"type": kind, "code": "upstream_response_invalid", "message": message}})
			return nil, GeiliUpstreamErrorFailure(c, payload, status, resp.Header)
		}
		if request.stream && !chat {
			output.response.Status = "failed"
			output.response.Error = &apicompat.ResponsesError{Code: "upstream_stream_incomplete", Message: "Upstream response did not complete"}
			_ = output.event(apicompat.ResponsesStreamEvent{Type: "response.failed", Response: output.response})
			MarkOpsStreamFailure(c, "upstream_error", "upstream_stream_incomplete", "Upstream response did not complete", http.StatusBadGateway)
		}
		return result(), err
	}
	if request.stream {
		if chat {
			_, err = io.WriteString(c.Writer, "data: [DONE]\n\n")
			c.Writer.Flush()
		}
	} else {
		var value any = output.response
		if chat {
			value = apicompat.ResponsesToChatCompletions(output.response, request.model)
		}
		data, marshalErr := geminiOpenAISignedPayloadGeili(value, output.signatures)
		if marshalErr != nil {
			return result(), marshalErr
		}
		if !chat {
			data, _, err = apicompat.RestoreResponsesClientToolPayload(data, request.mapping)
			if err != nil {
				return result(), err
			}
		}
		c.Data(http.StatusOK, "application/json", data)
	}
	return result(), err
}

type geminiOpenAIScanGeili struct {
	line string
	err  error
}

func (s *GeminiMessagesCompatService) readOpenAIGeminiSSEGeili(ctx context.Context, c *gin.Context, resp *http.Response, model string, clientStream bool, out *geminiOpenAIOutputGeili) error {
	events := make(chan geminiOpenAIScanGeili, 1)
	done := make(chan struct{})
	defer close(done)
	maxLine := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLine = s.cfg.Gateway.MaxLineSize
	}
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), maxLine)
		send := func(e geminiOpenAIScanGeili) bool {
			select {
			case events <- e:
				return true
			case <-done:
				return false
			}
		}
		for scanner.Scan() {
			if !send(geminiOpenAIScanGeili{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			send(geminiOpenAIScanGeili{err: err})
		}
	}()
	interval := time.Duration(0)
	if s.cfg != nil {
		interval = longThinkingStreamInterval(s.cfg, model, time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout)*time.Second)
	}
	var timer, firstDeadline *time.Timer
	var idleCh, firstCh <-chan time.Time
	if interval > 0 {
		timer = time.NewTimer(interval)
		firstDeadline = time.NewTimer(interval)
		idleCh = timer.C
		firstCh = firstDeadline.C
		defer timer.Stop()
		defer firstDeadline.Stop()
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	var data []string
	process := func() error {
		if len(data) == 0 {
			return nil
		}
		raw := strings.Join(data, "\n")
		data = nil
		if raw == "[DONE]" {
			return nil
		}
		if len(raw) > openAIFirstOutputStageMaxBytes && !out.semantic {
			return fmt.Errorf("Gemini prelude exceeds staging limit")
		}
		return out.consume([]byte(raw))
	}
	canceled := ctx.Done()
	for {
		select {
		case <-canceled:
			if !out.semantic && !out.usage.hasObservedTokens() {
				return ctx.Err()
			}
			GeiliMarkTextClientDisconnect(c)
			canceled, firstCh = nil, nil
			if timer == nil {
				interval = anthropicCompatDisconnectDrainInterval
				timer = time.NewTimer(interval)
				idleCh = timer.C
				defer timer.Stop()
			}
		case <-idleCh:
			return fmt.Errorf("Gemini stream idle timeout")
		case <-firstCh:
			if !out.semantic {
				return fmt.Errorf("Gemini first content deadline exceeded")
			}
		case <-heartbeat.C:
			if clientStream && !geiliTextClientDisconnected(c) {
				if _, err := io.WriteString(c.Writer, ": ping\n\n"); err != nil {
					return err
				}
				c.Writer.Flush()
			}
		case event, open := <-events:
			if !open {
				return process()
			}
			if event.err != nil {
				return event.err
			}
			line := strings.TrimSuffix(event.line, "\r")
			if line == "" {
				if err := process(); err != nil {
					return err
				}
			} else if strings.HasPrefix(line, "data:") {
				if timer != nil {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(interval)
				}
				data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
				n := 0
				for _, s := range data {
					n += len(s)
				}
				if n > maxLine {
					return fmt.Errorf("Gemini SSE event exceeds size limit")
				}
			}
		}
	}
}
