package service

// Recovery tests use a separate synthetic request and validate the supplier's
// actual completion, without relying on the permissive admin test SSE envelope.
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const accountRecoveryProbeMaxBytesGeili = 1 << 20
const accountRecoveryProbePromptGeili = "Say OK."

type accountRecoveryProbeObservationKeyGeili struct{}

// Fields are filled synchronously by the probe transport and read only after
// ProbeAccountRecoveryGeili returns. Zero/nil mean no measured response value.
type AccountRecoveryProbeObservationGeili struct {
	HTTPStatus         int
	TimeToFirstTokenMs *int64
}

func WithAccountRecoveryProbeObservationGeili(ctx context.Context) (context.Context, *AccountRecoveryProbeObservationGeili) {
	observation := &AccountRecoveryProbeObservationGeili{}
	return context.WithValue(ctx, accountRecoveryProbeObservationKeyGeili{}, observation), observation
}

func accountRecoveryProbeObservationGeili(ctx context.Context) *AccountRecoveryProbeObservationGeili {
	observation, _ := ctx.Value(accountRecoveryProbeObservationKeyGeili{}).(*AccountRecoveryProbeObservationGeili)
	return observation
}

var errAccountRecoveryProbeIncompleteGeili = errors.New("account recovery probe did not produce a complete useful response")
var errAccountRecoveryProbeUnsupportedGeili = errors.New("account recovery probe requires a supported text HTTP endpoint")

func accountRecoveryProbeTextModelGeili(model string) bool {
	model = strings.ToLower(model)
	for _, marker := range []string{"image", "video", "audio", "voice", "realtime", "embedding", "tts", "stt"} {
		if strings.Contains(model, marker) {
			return false
		}
	}
	return true
}

type accountRecoveryProbeRepositoryGeili struct {
	AccountRepository
	account *Account
}

func (r *accountRecoveryProbeRepositoryGeili) GetByID(ctx context.Context, id int64) (*Account, error) {
	if id == r.account.ID {
		return r.account, nil
	}
	if r.AccountRepository == nil {
		return nil, ErrAccountNotFound
	}
	return r.AccountRepository.GetByID(ctx, id)
}

// Admin connection tests can reconcile errors, quota and capability state.
// The recovery runner alone owns state transitions for this synthetic test.
func (*accountRecoveryProbeRepositoryGeili) SetError(context.Context, int64, string) error {
	return nil
}
func (*accountRecoveryProbeRepositoryGeili) ClearError(context.Context, int64) error { return nil }
func (*accountRecoveryProbeRepositoryGeili) UpdateExtra(context.Context, int64, map[string]any) error {
	return nil
}
func (*accountRecoveryProbeRepositoryGeili) SetRateLimited(context.Context, int64, time.Time) error {
	return nil
}
func (*accountRecoveryProbeRepositoryGeili) ClearRateLimit(context.Context, int64) error { return nil }
func (*accountRecoveryProbeRepositoryGeili) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	return nil
}
func (*accountRecoveryProbeRepositoryGeili) ClearTempUnschedulable(context.Context, int64) error {
	return nil
}
func (*accountRecoveryProbeRepositoryGeili) SetSchedulable(context.Context, int64, bool) error {
	return nil
}

type accountRecoveryProbeCaptureGeili struct {
	path   string
	model  string
	status int
	body   []byte
}
type accountRecoveryProbeUpstreamGeili struct {
	HTTPUpstream
	pluginManager *PluginManager
	account       *Account
	captures      []accountRecoveryProbeCaptureGeili
	err           error
}

func accountRecoveryProbeProtocolGeili(path string) string {
	path = strings.TrimRight(path, "/")
	switch {
	case strings.HasSuffix(path, "/messages"):
		return "anthropic"
	case strings.HasSuffix(path, ":streamRawPredict"), strings.HasSuffix(path, ":rawPredict"):
		return "anthropic"
	case strings.Contains(path, "/model/") && strings.HasSuffix(path, "/invoke"):
		return "bedrock"
	case strings.HasSuffix(path, "/chat/completions"):
		return "chat"
	case strings.HasSuffix(path, "/responses"):
		return "responses"
	case strings.HasSuffix(path, ":streamGenerateContent"), strings.HasSuffix(path, ":generateContent"):
		return "gemini"
	default:
		return ""
	}
}

func (u *accountRecoveryProbeUpstreamGeili) request(req *http.Request, proxy string, id int64, concurrency int, profile *tlsfingerprint.Profile, tls bool) (*http.Response, error) {
	observation := accountRecoveryProbeObservationGeili(req.Context())
	if observation != nil {
		observation.HTTPStatus = 0
		observation.TimeToFirstTokenMs = nil
	}
	protocol := accountRecoveryProbeProtocolGeili(req.URL.Path)
	if protocol == "" || req.Method != http.MethodPost {
		u.err = errAccountRecoveryProbeUnsupportedGeili
		return nil, u.err
	}
	if req.Body == nil {
		u.err = errAccountRecoveryProbeIncompleteGeili
		return nil, u.err
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, accountRecoveryProbeMaxBytesGeili+1))
	if req.Body != nil {
		_ = req.Body.Close()
	}
	if err != nil || len(body) > accountRecoveryProbeMaxBytesGeili {
		u.err = errAccountRecoveryProbeIncompleteGeili
		return nil, u.err
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return nil, errAccountRecoveryProbeIncompleteGeili
	}
	actualModel, _ := payload["model"].(string)
	if actualModel == "" {
		if protocol == "bedrock" {
			actualModel = strings.TrimSuffix(strings.SplitN(req.URL.Path, "/model/", 2)[1], "/invoke")
		} else if index := strings.LastIndex(req.URL.Path, "/models/"); index >= 0 {
			actualModel = req.URL.Path[index+len("/models/"):]
			if index := strings.LastIndex(actualModel, ":"); index >= 0 {
				actualModel = actualModel[:index]
			}
		}
	}
	if !accountRecoveryProbeTextModelGeili(actualModel) || (protocol == "gemini" && !accountRecoveryProbeTextModelGeili(req.URL.Path)) {
		u.err = errAccountRecoveryProbeUnsupportedGeili
		return nil, u.err
	}
	for _, key := range []string{"thinking", "reasoning", "output_config", "tools", "tool_choice", "modalities", "response_format", "audio", "n"} {
		delete(payload, key)
	}
	switch protocol {
	case "anthropic", "chat":
		payload["messages"] = []any{map[string]any{"role": "user", "content": accountRecoveryProbePromptGeili}}
		payload["max_tokens"] = 64
		delete(payload, "max_completion_tokens")
	case "responses":
		payload["input"] = []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": accountRecoveryProbePromptGeili}}}}
		if strings.HasSuffix(req.URL.Path, "/backend-api/codex/responses") {
			// Native Codex rejects max_output_tokens. Its fixed prompt, deadline,
			// capture limit and short-output validation provide the hard bounds.
			delete(payload, "max_output_tokens")
		} else {
			payload["max_output_tokens"] = 64
		}
	case "gemini":
		inner := payload
		if wrapped, ok := payload["request"].(map[string]any); ok {
			inner = wrapped
		}
		inner["contents"] = []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": accountRecoveryProbePromptGeili}}}}
		inner["generationConfig"] = map[string]any{"maxOutputTokens": 64, "responseModalities": []string{"TEXT"}}
		delete(inner, "tools")
		delete(inner, "toolConfig")
	case "bedrock":
		// AWS payload was already fixed before signing; never rewrite its bytes.
		if gjson.GetBytes(body, "max_tokens").Int() != 64 || gjson.GetBytes(body, "messages.0.content.0.text").String() != accountRecoveryProbePromptGeili {
			return nil, errAccountRecoveryProbeUnsupportedGeili
		}
	}
	if protocol != "bedrock" {
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, errAccountRecoveryProbeIncompleteGeili
		}
	}
	controlled := req.Clone(req.Context())
	controlled.Body = io.NopCloser(bytes.NewReader(body))
	controlled.ContentLength = int64(len(body))
	controlled.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	var resp *http.Response
	handled := false
	if u.pluginManager != nil {
		resp, handled, err = u.pluginManager.RoundTripOpenAIOAuth(controlled.Context(), controlled, proxy, u.account)
	}
	if !handled && tls {
		resp, err = u.HTTPUpstream.DoWithTLS(controlled, proxy, id, concurrency, profile)
	} else if !handled {
		resp, err = u.HTTPUpstream.Do(controlled, proxy, id, concurrency)
	}
	if err != nil {
		u.err = errAccountRecoveryProbeIncompleteGeili
		return nil, u.err
	}
	if resp == nil || resp.Body == nil {
		u.err = errAccountRecoveryProbeIncompleteGeili
		return nil, u.err
	}
	if observation != nil {
		observation.HTTPStatus = resp.StatusCode
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, accountRecoveryProbeMaxBytesGeili+1))
	_ = resp.Body.Close()
	if err != nil || len(body) > accountRecoveryProbeMaxBytesGeili {
		u.err = errAccountRecoveryProbeIncompleteGeili
		return nil, u.err
	}
	u.captures = append(u.captures, accountRecoveryProbeCaptureGeili{path: req.URL.Path, model: actualModel, status: resp.StatusCode, body: body})
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}
func (u *accountRecoveryProbeUpstreamGeili) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	return u.request(req, proxy, id, concurrency, nil, false)
}
func (u *accountRecoveryProbeUpstreamGeili) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.request(req, proxy, id, concurrency, profile, true)
}

// ProbeAccountRecoveryGeili never admits customer traffic or records billable
// usage. A complete response is evidence for the runner, not a state mutation.
func (s *AccountTestService) ProbeAccountRecoveryGeili(ctx context.Context, account *Account, model string) (bool, error) {
	if s == nil || s.httpUpstream == nil || account == nil || account.IsOpenAIAgentIdentity() || account.IsSyntheticUITest() {
		return false, errAccountRecoveryProbeUnsupportedGeili
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return false, errAccountRecoveryProbeUnsupportedGeili
	}
	if !accountRecoveryProbeTextModelGeili(account.GetMappedModel(model)) {
		return false, errAccountRecoveryProbeUnsupportedGeili
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, accountRecoveryProbeContextGeili{}, true)
	if account.IsCredentialShadow() {
		if s.accountRepo == nil {
			return false, errAccountRecoveryProbeUnsupportedGeili
		}
		credentials, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil || credentials == nil || credentials.IsOpenAIAgentIdentity() {
			return false, errAccountRecoveryProbeUnsupportedGeili
		}
	}
	shadow := *account
	shadow.Credentials = cloneMapGeili(account.Credentials)
	shadow.Extra = cloneMapGeili(account.Extra)
	repo := &accountRecoveryProbeRepositoryGeili{AccountRepository: s.accountRepo, account: &shadow}
	upstream := &accountRecoveryProbeUpstreamGeili{HTTPUpstream: s.httpUpstream, pluginManager: s.pluginManager, account: &shadow}
	clone := NewAccountTestService(repo, s.geminiTokenProvider, s.claudeTokenProvider, s.grokTokenProvider, nil, upstream, s.cfg, s.tlsFPProfileService)
	clone.settingService = s.settingService
	// The shared plugin pointer is used inside the capture transport so plugin
	// traffic receives the same fixed prompt, bounds and strict validation.
	clone.openaiGatewayService = s.openaiGatewayService
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/internal/recovery-probe", nil).WithContext(ctx)
	c.Set(accountTestBackgroundKey, true)
	expectedModel := account.GetMappedModel(model)
	if account.IsBedrock() {
		resolved, ok := ResolveBedrockModelID(account, model)
		if !ok {
			return false, errAccountRecoveryProbeUnsupportedGeili
		}
		expectedModel = resolved
	} else if account.IsOAuth() && account.Platform == PlatformOpenAI {
		expectedModel = normalizeOpenAIModelForUpstream(account, expectedModel)
	} else if account.Platform == PlatformAnthropic && account.Type == AccountTypeServiceAccount {
		expectedModel = normalizeVertexAnthropicModelID(claude.NormalizeModelID(expectedModel))
	}
	if account.Platform == PlatformAntigravity && account.Type == AccountTypeOAuth {
		if s.antigravityGatewayService == nil {
			return false, errAccountRecoveryProbeUnsupportedGeili
		}
		expectedModel = s.antigravityGatewayService.getMappedModel(&shadow, model)
		if expectedModel == "" {
			return false, errAccountRecoveryProbeUnsupportedGeili
		}
		if err := s.probeAntigravityRawGeili(ctx, &shadow, expectedModel, upstream); err != nil {
			return false, err
		}
	} else if account.Platform == PlatformAntigravity && account.Type != AccountTypeAPIKey {
		return false, errAccountRecoveryProbeUnsupportedGeili
	} else if account.Platform == PlatformGemini {
		// Mapping was applied once above. Native Gemini paths take the canonical
		// model segment, never the public alias or a full resource name.
		expectedModel = normalizeAntigravityModelName(expectedModel)
		geminiAccount := shadow
		geminiAccount.Credentials = cloneMapGeili(shadow.Credentials)
		delete(geminiAccount.Credentials, "model_mapping")
		_ = clone.testGeminiAccountConnection(c, &geminiAccount, expectedModel, accountRecoveryProbePromptGeili)
	} else {
		_ = clone.TestAccountConnection(c, shadow.ID, model, accountRecoveryProbePromptGeili, AccountTestModeDefault)
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if upstream.err != nil {
		return false, upstream.err
	}
	if len(upstream.captures) == 0 {
		return false, errAccountRecoveryProbeIncompleteGeili
	}
	for _, capture := range upstream.captures {
		if supplierAccountBalanceFailureGeili(account, capture.status, capture.body) {
			return false, ErrAccountBalanceBlockedGeili
		}
		frames, _ := accountRecoveryProbeFramesGeili(capture.body)
		for _, frame := range frames {
			if supplierAccountBalanceFailureGeili(account, capture.status, []byte(frame.Raw)) {
				return false, ErrAccountBalanceBlockedGeili
			}
		}
	}
	last := upstream.captures[len(upstream.captures)-1]
	if account.Platform == PlatformGemini || account.Platform == PlatformAntigravity {
		if normalizeAntigravityModelName(last.model) != normalizeAntigravityModelName(expectedModel) {
			return false, errAccountRecoveryProbeIncompleteGeili
		}
	} else if last.model != expectedModel {
		return false, errAccountRecoveryProbeIncompleteGeili
	}
	if last.status < 200 || last.status >= 300 || !accountRecoveryProbeCompleteGeili(last) {
		return false, errAccountRecoveryProbeIncompleteGeili
	}
	return true, nil
}

func (s *AccountTestService) probeAntigravityRawGeili(ctx context.Context, account *Account, model string, upstream *accountRecoveryProbeUpstreamGeili) error {
	// Read existing credentials/cache only. Refresh and project backfill can
	// change scheduler state and belong to the separate credential lifecycle.
	token := strings.TrimSpace(account.GetCredential("access_token"))
	if provider := s.antigravityGatewayService.tokenProvider; provider != nil && provider.tokenCache != nil {
		if cached, err := provider.tokenCache.GetAccessToken(ctx, AntigravityTokenCacheKey(account)); err == nil && strings.TrimSpace(cached) != "" {
			token = cached
		}
	}
	if token == "" {
		return errAccountRecoveryProbeIncompleteGeili
	}
	project, err := resolveAntigravityProjectID(account)
	if err != nil {
		return errAccountRecoveryProbeIncompleteGeili
	}
	builder := NewAntigravityGatewayService(nil, nil, nil, nil, nil, upstream, s.antigravityGatewayService.settingService, nil)
	body, err := builder.buildGeminiTestRequest(project, model)
	if !strings.HasPrefix(normalizeAntigravityModelName(model), "gemini-") {
		body, err = builder.buildClaudeTestRequest(project, model)
	}
	if err != nil {
		return errAccountRecoveryProbeIncompleteGeili
	}
	req, err := antigravity.NewAPIRequestWithURL(ctx, resolveAntigravityForwardBaseURL(account), "streamGenerateContent", token, body)
	if err != nil {
		return errAccountRecoveryProbeIncompleteGeili
	}
	proxy := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	resp, err := upstream.Do(req, proxy, account.ID, account.Concurrency)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	return err
}

func cloneMapGeili(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func accountRecoveryProbeFramesGeili(body []byte) ([]gjson.Result, bool) {
	if gjson.ValidBytes(body) {
		value := gjson.ParseBytes(body)
		if value.IsArray() {
			return value.Array(), true
		}
		return []gjson.Result{value}, true
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), accountRecoveryProbeMaxBytesGeili)
	var frames []gjson.Result
	var data []string
	flush := func() bool {
		payload := strings.Join(data, "\n")
		data = nil
		if strings.TrimSpace(payload) == "" || payload == "[DONE]" {
			return true
		}
		if !gjson.Valid(payload) {
			return false
		}
		frames = append(frames, gjson.Parse(payload))
		return true
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if !flush() {
				return nil, false
			}
			continue
		}
		if value, ok := parseAnthropicSSEField(line, "data"); ok {
			data = append(data, value)
		}
	}
	if scanner.Err() != nil || !flush() {
		return nil, false
	}
	return frames, len(frames) > 0
}

func accountRecoveryProbeCompleteGeili(capture accountRecoveryProbeCaptureGeili) bool {
	frames, valid := accountRecoveryProbeFramesGeili(capture.body)
	if !valid {
		return false
	}
	for _, frame := range frames {
		if frame.Get("error").Exists() || frame.Get("type").String() == "error" || frame.Get("type").String() == "response.failed" || frame.Get("type").String() == "response.incomplete" {
			return false
		}
	}
	// Provider envelopes may contain headers and usage, but this fixed prompt
	// must not produce an unexpectedly long answer or argument payload.
	outputBytes := 0
	for _, frame := range frames {
		switch frame.Get("type").String() {
		case "content_block_start":
			outputBytes += len(frame.Get("content_block.text").String()) + len(frame.Get("content_block.input").Raw)
		case "content_block_delta":
			outputBytes += len(frame.Get("delta.text").String()) + len(frame.Get("delta.partial_json").String())
		case "response.output_text.delta", "response.function_call_arguments.delta":
			outputBytes += len(frame.Get("delta").String())
		}
		for _, choice := range frame.Get("choices").Array() {
			for _, field := range []string{"delta.content", "message.content"} {
				outputBytes += len(choice.Get(field).String())
			}
			for _, field := range []string{"delta.tool_calls", "message.tool_calls"} {
				for _, tool := range choice.Get(field).Array() {
					outputBytes += len(tool.Get("function.arguments").String())
				}
			}
		}
		candidateFrame := frame
		if wrapped := frame.Get("response"); wrapped.Exists() {
			candidateFrame = wrapped
		}
		for _, item := range candidateFrame.Get("output").Array() {
			outputBytes += len(item.Get("arguments").String())
			for _, content := range item.Get("content").Array() {
				outputBytes += len(content.Get("text").String())
			}
		}
		for _, candidate := range candidateFrame.Get("candidates").Array() {
			for _, part := range candidate.Get("content.parts").Array() {
				outputBytes += len(part.Get("text").String()) + len(part.Get("functionCall").Raw)
			}
		}
	}
	if outputBytes > 2048 {
		return false
	}
	switch accountRecoveryProbeProtocolGeili(capture.path) {
	case "bedrock":
		var message apicompat.AnthropicResponse
		if json.Unmarshal(capture.body, &message) != nil || message.Type != "message" || (&anthropicCompletionGeili{response: &message}).complete() != nil {
			return false
		}
		for _, block := range message.Content {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				return true
			}
			if (block.Type == "tool_use" || block.Type == "server_tool_use") && json.Valid(block.Input) {
				return true
			}
		}
		return false
	case "anthropic":
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(capture.body))}
		tracker, err := readAnthropicCompletionGeili(c, resp, nil, "", false, nil)
		if err != nil || tracker.response == nil {
			return false
		}
		for _, block := range tracker.response.Content {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				return true
			}
			if (block.Type == "tool_use" || block.Type == "server_tool_use") && json.Valid(block.Input) {
				return true
			}
		}
	case "responses":
		useful, completed := false, false
		toolDeltas := map[string]string{}
		toolCompleted := map[string]bool{}
		for _, frame := range frames {
			if frame.Get("type").String() == "response.function_call_arguments.delta" {
				key := "id:" + frame.Get("item_id").String()
				if key == "id:" {
					key = "index:" + strconv.FormatInt(frame.Get("output_index").Int(), 10)
				}
				toolDeltas[key] += frame.Get("delta").String()
			}
			if frame.Get("type").String() == "response.output_text.delta" && strings.TrimSpace(frame.Get("delta").String()) != "" {
				useful = true
			}
			response := frame.Get("response")
			if frame.Get("type").String() == "response.completed" || frame.Get("status").String() == "completed" {
				if !response.Exists() {
					response = frame
				}
				if status := response.Get("status").String(); status != "" && status != "completed" {
					return false
				}
				completed = true
				for index, item := range response.Get("output").Array() {
					if item.Get("type").String() == "function_call" {
						args := item.Get("arguments").String()
						if !json.Valid([]byte(args)) {
							return false
						}
						for _, key := range []string{"id:" + item.Get("id").String(), "index:" + strconv.Itoa(index)} {
							if delta, exists := toolDeltas[key]; exists {
								if !json.Valid([]byte(delta)) || strings.TrimSpace(delta) != strings.TrimSpace(args) {
									return false
								}
								toolCompleted[key] = true
							}
						}
						useful = true
					}
					for _, content := range item.Get("content").Array() {
						if content.Get("type").String() == "output_text" && strings.TrimSpace(content.Get("text").String()) != "" {
							useful = true
						}
					}
				}
			}
		}
		for key := range toolDeltas {
			if !toolCompleted[key] || !json.Valid([]byte(toolDeltas[key])) {
				return false
			}
		}
		return useful && completed
	case "chat":
		type choiceState struct {
			complete, useful bool
			tools            map[int]string
		}
		choices := map[int]*choiceState{}
		for _, frame := range frames {
			for _, choice := range frame.Get("choices").Array() {
				index := int(choice.Get("index").Int())
				state := choices[index]
				if state == nil {
					state = &choiceState{tools: map[int]string{}}
					choices[index] = state
				}
				message := choice.Get("delta")
				if !message.Exists() {
					message = choice.Get("message")
				}
				state.useful = state.useful || strings.TrimSpace(message.Get("content").String()) != ""
				for _, tool := range message.Get("tool_calls").Array() {
					state.tools[int(tool.Get("index").Int())] += tool.Get("function.arguments").String()
				}
				if call := message.Get("function_call"); call.Exists() {
					state.tools[-1] += call.Get("arguments").String()
				}
				if reason := choice.Get("finish_reason").String(); reason != "" {
					if reason != "stop" && reason != "tool_calls" && reason != "function_call" {
						return false
					}
					state.complete = true
				}
			}
		}
		useful := false
		for _, state := range choices {
			if !state.complete {
				return false
			}
			for _, args := range state.tools {
				if !json.Valid([]byte(args)) {
					return false
				}
				state.useful = true
			}
			useful = useful || state.useful
		}
		return len(choices) > 0 && useful
	case "gemini":
		type candidateState struct{ useful, finished bool }
		candidates := map[int]*candidateState{}
		for _, frame := range frames {
			if wrapped := frame.Get("response"); wrapped.Exists() {
				frame = wrapped
			}
			for position, candidate := range frame.Get("candidates").Array() {
				index := position
				if candidate.Get("index").Exists() {
					index = int(candidate.Get("index").Int())
				}
				state := candidates[index]
				if state == nil {
					state = &candidateState{}
					candidates[index] = state
				}
				if reason := candidate.Get("finishReason").String(); reason != "" {
					if reason != "STOP" {
						return false
					}
					state.finished = true
				}
				for _, part := range candidate.Get("content.parts").Array() {
					if !part.Get("thought").Bool() && strings.TrimSpace(part.Get("text").String()) != "" {
						state.useful = true
					}
					if call := part.Get("functionCall"); call.IsObject() && call.Get("args").IsObject() {
						state.useful = true
					}
				}
			}
		}
		useful := false
		for _, state := range candidates {
			if !state.finished {
				return false
			}
			useful = useful || state.useful
		}
		return len(candidates) > 0 && useful
	}
	return false
}
