package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

type geminiOpenAIOutputGeili struct {
	response              *apicompat.ResponsesResponse
	usage                 ClaudeUsage
	signatures            map[string]string
	sequence              int
	emit                  func(apicompat.ResponsesStreamEvent) error
	openText, openThought int
	pendingTools          map[string]int
	completedTools        map[string]int
	thoughtTokens         int
	toolParts             map[int]int
	semantic              bool
	businessContent       bool
	signal                *geminiResponseSignal
	finish                string
	firstTokenMs          *int
	started               time.Time
}

func newGeminiOpenAIOutputGeili(model string, start time.Time, emit func(apicompat.ResponsesStreamEvent) error) *geminiOpenAIOutputGeili {
	return &geminiOpenAIOutputGeili{response: &apicompat.ResponsesResponse{ID: "resp_" + randomHex(12), Object: "response", CreatedAt: start.Unix(), Model: model, Status: "in_progress", Output: []apicompat.ResponsesOutput{}}, signatures: map[string]string{}, emit: emit, openText: -1, openThought: -1, pendingTools: map[string]int{}, completedTools: map[string]int{}, toolParts: map[int]int{}, started: start}
}
func (s *geminiOpenAIOutputGeili) event(e apicompat.ResponsesStreamEvent) error {
	e.SequenceNumber = s.sequence
	s.sequence++
	if s.emit != nil {
		return s.emit(e)
	}
	return nil
}
func (s *geminiOpenAIOutputGeili) begin() error {
	r := *s.response
	r.Output = []apicompat.ResponsesOutput{}
	if err := s.event(apicompat.ResponsesStreamEvent{Type: "response.created", Response: &r}); err != nil {
		return err
	}
	return s.event(apicompat.ResponsesStreamEvent{Type: "response.in_progress", Response: &r})
}
func (s *geminiOpenAIOutputGeili) markSemantic() {
	if !s.semantic {
		ms := int(time.Since(s.started).Milliseconds())
		s.firstTokenMs = &ms
	}
	s.semantic = true
}
func (s *geminiOpenAIOutputGeili) add(item apicompat.ResponsesOutput) (int, error) {
	idx := len(s.response.Output)
	s.response.Output = append(s.response.Output, item)
	copy := item
	return idx, s.event(apicompat.ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: idx, Item: &copy})
}
func (s *geminiOpenAIOutputGeili) text(value string, thought bool) error {
	if value == "" {
		return nil
	}
	idx := s.openText
	if thought {
		idx = s.openThought
	}
	seen := ""
	if idx >= 0 {
		if thought {
			seen = s.response.Output[idx].Summary[0].Text
		} else {
			seen = s.response.Output[idx].Content[0].Text
		}
	}
	value, _ = computeGeminiTextDelta(seen, value)
	if value == "" {
		return nil
	}
	s.markSemantic()
	if !thought {
		s.businessContent = true
	}
	if idx < 0 {
		item := apicompat.ResponsesOutput{Type: "message", ID: "msg_" + randomHex(10), Role: "assistant", Status: "in_progress", Content: []apicompat.ResponsesContentPart{}}
		if thought {
			item = apicompat.ResponsesOutput{Type: "reasoning", ID: "rs_" + randomHex(10), Summary: []apicompat.ResponsesSummary{{Type: "summary_text", Text: ""}}}
		}
		var err error
		idx, err = s.add(item)
		if err != nil {
			return err
		}
		if thought {
			s.openThought = idx
			if err = s.event(apicompat.ResponsesStreamEvent{Type: "response.reasoning_summary_part.added", OutputIndex: idx, ItemID: item.ID, Part: &apicompat.ResponsesContentPart{Type: "summary_text", Text: ""}}); err != nil {
				return err
			}
		} else {
			s.openText = idx
			s.response.Output[idx].Content = []apicompat.ResponsesContentPart{{Type: "output_text", Text: ""}}
			if err = s.event(apicompat.ResponsesStreamEvent{Type: "response.content_part.added", OutputIndex: idx, ItemID: item.ID, Part: &apicompat.ResponsesContentPart{Type: "output_text", Text: ""}}); err != nil {
				return err
			}
		}
	}
	item := &s.response.Output[idx]
	typ := "response.output_text.delta"
	if thought {
		item.Summary[0].Text += value
		typ = "response.reasoning_summary_text.delta"
	} else {
		item.Content[0].Text += value
	}
	return s.event(apicompat.ResponsesStreamEvent{Type: typ, OutputIndex: idx, ItemID: item.ID, Delta: value})
}
func (s *geminiOpenAIOutputGeili) consume(raw []byte) error {
	if inner, err := unwrapGeminiResponse(raw); err == nil {
		raw = inner
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("invalid Gemini stream JSON: %w", err)
	}
	if signal, ok := detectGeminiResponseSignal(raw); ok {
		if signal.Kind == geminiSignalError {
			s.signal = &signal
			return fmt.Errorf("Gemini upstream error: %s", sanitizeUpstreamErrorMessage(signal.Message))
		}
		if signal.Kind == geminiSignalPromptBlocked || signal.Kind == geminiSignalContentFilter {
			s.finish = "SAFETY"
		}
	}
	if u := extractGeminiUsage(raw); u != nil {
		s.usage = *u
	}
	if usage, ok := payload["usageMetadata"].(map[string]any); ok {
		if n, ok := asInt(usage["thoughtsTokenCount"]); ok {
			s.thoughtTokens = n
		}
	}

	if reason := extractGeminiFinishReason(payload); reason != "" {
		s.finish = reason
	}
	for position, p := range extractGeminiParts(payload) {
		if text, _ := p["text"].(string); text != "" {
			thought, _ := p["thought"].(bool)
			if err := s.text(text, thought); err != nil {
				return err
			}
		}
		if _, exists := p["inlineData"]; exists {
			return fmt.Errorf("Gemini text compatibility does not support inline media output")
		}
		if _, exists := p["fileData"]; exists {
			return fmt.Errorf("Gemini text compatibility does not support file media output")
		}

		if fc, ok := p["functionCall"].(map[string]any); ok {
			name, _ := fc["name"].(string)
			if name == "" {
				return fmt.Errorf("Gemini function call has no name")
			}
			args := "{}"
			switch v := fc["args"].(type) {
			case string:
				args = v
			case nil:
			default:
				b, err := json.Marshal(v)
				if err != nil {
					return err
				}
				args = string(b)
			}
			id, _ := fc["id"].(string)
			key := id
			if key == "" {
				key = fmt.Sprintf("%d:%s", position, name)
			}
			idx, exists := s.pendingTools[key]
			if !exists && id == "" {
				if previous, ok := s.completedTools[key]; ok && s.response.Output[previous].Arguments == args {
					idx = previous
					exists = true
				}
			}
			if !exists {
				item := apicompat.ResponsesOutput{Type: "function_call", ID: "fc_" + randomHex(10), CallID: "call_geili_" + randomHex(12), Name: name, Arguments: "", Status: "in_progress"}
				if id != "" {
					item.CallID = id
				}
				if sig, _ := p["thoughtSignature"].(string); sig != "" {
					s.signatures[item.CallID] = sig
				}
				var err error
				idx, err = s.add(item)
				if err != nil {
					return err
				}
				s.pendingTools[key] = idx
				s.toolParts[position] = idx
				s.openText = -1
				s.openThought = -1
			}
			item := &s.response.Output[idx]
			if item.Name != name {
				return fmt.Errorf("Gemini reused a function call id for a different function")
			}
			if sig, _ := p["thoughtSignature"].(string); sig != "" {
				s.signatures[item.CallID] = sig
			}
			delta := args
			if item.Arguments != "" && strings.HasPrefix(args, item.Arguments) {
				delta = strings.TrimPrefix(args, item.Arguments)
			}
			item.Arguments += delta
			s.markSemantic()
			if delta != "" {
				if err := s.event(apicompat.ResponsesStreamEvent{Type: "response.function_call_arguments.delta", OutputIndex: idx, ItemID: item.ID, CallID: item.CallID, Name: name, Delta: delta}); err != nil {
					return err
				}
			}
			s.businessContent = true
			if json.Valid([]byte(item.Arguments)) && id == "" {
				delete(s.pendingTools, key)
				s.completedTools[key] = idx
			}
		} else if sig, _ := p["thoughtSignature"].(string); sig != "" {
			if idx, ok := s.toolParts[position]; ok {
				s.signatures[s.response.Output[idx].CallID] = sig
			}
		}
	}
	return nil
}
func (s *geminiOpenAIOutputGeili) finalize() error {
	if s.finish == "" {
		return fmt.Errorf("Gemini stream ended without finishReason")
	}
	switch s.finish {
	case "STOP":
		s.response.Status = "completed"
	case "MAX_TOKENS":
		s.response.Status = "incomplete"
		s.response.IncompleteDetails = &apicompat.ResponsesIncompleteDetails{Reason: "max_output_tokens"}
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY":
		s.response.Status = "incomplete"
		s.response.IncompleteDetails = &apicompat.ResponsesIncompleteDetails{Reason: "content_filter"}
	default:
		return fmt.Errorf("Gemini generation failed: %s", s.finish)
	}
	if !s.businessContent && s.response.Status == "completed" {
		return fmt.Errorf("Gemini returned empty content")
	}
	for idx := range s.response.Output {
		item := &s.response.Output[idx]
		switch item.Type {
		case "message":
			for ci, p := range item.Content {
				if err := s.event(apicompat.ResponsesStreamEvent{Type: "response.output_text.done", OutputIndex: idx, ContentIndex: ci, ItemID: item.ID, Text: p.Text}); err != nil {
					return err
				}
				copy := p
				if err := s.event(apicompat.ResponsesStreamEvent{Type: "response.content_part.done", OutputIndex: idx, ContentIndex: ci, ItemID: item.ID, Part: &copy}); err != nil {
					return err
				}
			}
		case "reasoning":
			for _, p := range item.Summary {
				if err := s.event(apicompat.ResponsesStreamEvent{Type: "response.reasoning_summary_text.done", OutputIndex: idx, ItemID: item.ID, Text: p.Text}); err != nil {
					return err
				}
				if err := s.event(apicompat.ResponsesStreamEvent{Type: "response.reasoning_summary_part.done", OutputIndex: idx, ItemID: item.ID, Part: &apicompat.ResponsesContentPart{Type: "summary_text", Text: p.Text}}); err != nil {
					return err
				}
			}
		case "function_call":
			if !json.Valid([]byte(item.Arguments)) && s.finish != "MAX_TOKENS" {
				return fmt.Errorf("Gemini function call arguments are incomplete")
			}
			if err := s.event(apicompat.ResponsesStreamEvent{Type: "response.function_call_arguments.done", OutputIndex: idx, ItemID: item.ID, CallID: item.CallID, Name: item.Name, Arguments: item.Arguments}); err != nil {
				return err
			}
		}
		item.Status = s.response.Status
		copy := *item
		if err := s.event(apicompat.ResponsesStreamEvent{Type: "response.output_item.done", OutputIndex: idx, Item: &copy}); err != nil {
			return err
		}
	}
	input := s.usage.InputTokens + s.usage.CacheReadInputTokens
	s.response.Usage = &apicompat.ResponsesUsage{InputTokens: input, OutputTokens: s.usage.OutputTokens, TotalTokens: input + s.usage.OutputTokens, InputTokensDetails: &apicompat.ResponsesInputTokensDetails{CachedTokens: s.usage.CacheReadInputTokens}}
	if s.thoughtTokens > 0 {
		s.response.Usage.OutputTokensDetails = &apicompat.ResponsesOutputTokensDetails{ReasoningTokens: s.thoughtTokens}
	}

	return s.event(apicompat.ResponsesStreamEvent{Type: "response." + s.response.Status, Response: s.response})
}

// Signatures are an explicit Gemini extension, independent of OpenAI ciphertext.
// Clients replay the field beside each function call in a stateless next turn.
func geminiOpenAISignedPayloadGeili(value any, signatures map[string]string) ([]byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(signatures) == 0 {
		return b, nil
	}
	var obj any
	if err = json.Unmarshal(b, &obj); err != nil {
		return nil, err
	}
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, v := range x {
				visit(v)
			}
		case map[string]any:
			if id, _ := x["call_id"].(string); id != "" {
				if sig := signatures[id]; sig != "" {
					x["gemini_thought_signature"] = sig
				}
			}
			if typ, _ := x["type"].(string); typ == "function" {
				if id, _ := x["id"].(string); id != "" {
					if sig := signatures[id]; sig != "" {
						x["gemini_thought_signature"] = sig
					}
				}
			}
			for _, v := range x {
				visit(v)
			}
		}
	}
	visit(obj)
	return json.Marshal(obj)
}
