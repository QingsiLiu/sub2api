package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

const geminiOpenAIContinuationGeili = "Continue and complete the previous assistant turn from where it stopped. Do not repeat content already provided; use the tool results already present."

// buildGeminiOpenAIHistoryGeili runs after client tool lowering. It builds
// native turns without mutating the client input or inventing tool results.
func buildGeminiOpenAIHistoryGeili(req map[string]any) (map[string]any, error) {
	native := map[string]any{}
	contents := []any{}
	system := []any{}
	if instructions, _ := req["instructions"].(string); instructions != "" {
		system = append(system, map[string]any{"text": instructions})
	}
	calls := map[string]string{}
	results := map[string]bool{}
	pending := 0
	appendContent := func(role string, parts []any) {
		kept := make([]any, 0, len(parts))
		for _, raw := range parts {
			part := raw.(map[string]any)
			if text, ok := part["text"].(string); ok && strings.TrimSpace(text) == "" {
				continue
			}
			kept = append(kept, part)
		}
		parts = kept
		if len(parts) == 0 {
			return
		}
		if len(contents) > 0 {
			last := contents[len(contents)-1].(map[string]any)
			if last["role"] == role {
				last["parts"] = append(last["parts"].([]any), parts...)
				return
			}
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	var input []any
	switch v := req["input"].(type) {
	case string:
		input = []any{map[string]any{"role": "user", "content": v}}
	case []any:
		input = v
	default:
		return nil, fmt.Errorf("input must be a string or array")
	}
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("input items must be objects")
		}
		typ, _ := item["type"].(string)
		switch typ {
		case "", "message":
			role, _ := item["role"].(string)
			parts, err := geminiOpenAIContentGeili(item["content"])
			if err != nil {
				return nil, err
			}
			switch role {
			case "system", "developer":
				system = append(system, parts...)
			case "user":
				appendContent("user", parts)
			case "assistant":
				appendContent("model", parts)
			default:
				return nil, fmt.Errorf("unsupported message role %q", role)
			}
		case "function_call":
			id, _ := item["call_id"].(string)
			name, _ := item["name"].(string)
			if id == "" || name == "" {
				return nil, fmt.Errorf("function_call requires call_id and name")
			}
			argsText, _ := item["arguments"].(string)
			var args map[string]any
			if json.Unmarshal([]byte(argsText), &args) != nil || args == nil {
				return nil, fmt.Errorf("function_call arguments must be a JSON object")
			}
			if _, exists := calls[id]; exists {
				return nil, fmt.Errorf("function_call call_id must be unique")
			}
			calls[id] = name
			pending++
			part := map[string]any{"functionCall": map[string]any{"name": name, "args": args}}
			if !strings.HasPrefix(id, "call_geili_") {
				part["functionCall"].(map[string]any)["id"] = id
			}
			if sig, _ := item["gemini_thought_signature"].(string); sig != "" {
				part["thoughtSignature"] = sig
			}
			appendContent("model", []any{part})
		case "function_call_output":
			id, _ := item["call_id"].(string)
			name := calls[id]
			if name == "" {
				return nil, fmt.Errorf("function_call_output %q has no preceding function_call", id)
			}
			if results[id] {
				return nil, fmt.Errorf("function_call_output %q must occur exactly once", id)
			}
			output := item["output"]
			if output == nil {
				return nil, fmt.Errorf("function_call_output requires output")
			}
			// Media belongs in native FunctionResponse.parts, not an opaque
			// object under response.output where it would lose multimodal meaning.
			functionResponse := map[string]any{"name": name, "response": map[string]any{"output": output}}
			if !strings.HasPrefix(id, "call_geili_") {
				functionResponse["id"] = id
			}
			if arr, ok := output.([]any); ok {
				parts, err := geminiOpenAIContentGeili(arr)
				if err != nil {
					return nil, err
				}
				var texts []string
				var media []any
				for _, raw := range parts {
					part := raw.(map[string]any)
					if text, ok := part["text"].(string); ok {
						texts = append(texts, text)
					} else {
						if _, ok := part["inlineData"]; !ok {
							return nil, fmt.Errorf("tool result images must use inline base64 data")
						}
						media = append(media, part)
					}
				}
				functionResponse["response"] = map[string]any{"output": strings.Join(texts, "\n")}
				if len(media) > 0 {
					functionResponse["parts"] = media
				}
			}
			results[id] = true
			pending--
			appendContent("user", []any{map[string]any{"functionResponse": functionResponse}})
		case "reasoning":
			if enc, _ := item["encrypted_content"].(string); enc != "" {
				return nil, fmt.Errorf("OpenAI encrypted reasoning is unsupported for Gemini compatibility")
			}
			if rawSummary := item["summary"]; rawSummary != nil {
				summary, ok := rawSummary.([]any)
				if !ok {
					return nil, fmt.Errorf("reasoning summary must be an array")
				}
				for _, raw := range summary {
					entry, ok := raw.(map[string]any)
					if !ok || entry["type"] != "summary_text" {
						return nil, fmt.Errorf("unsupported reasoning summary")
					}
					_, ok = entry["text"].(string)
					if !ok {
						return nil, fmt.Errorf("reasoning summary requires text")
					}
					// A Responses summary is display metadata, not native Gemini
					// thinking state. Replaying it creates a spurious model turn.
				}
			}

		default:
			return nil, fmt.Errorf("unsupported Gemini input item type %q", typ)
		}
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("input must contain at least one message or function call")
	}
	if pending != 0 {
		return nil, fmt.Errorf("each function_call requires exactly one function_call_output")
	}
	last := contents[len(contents)-1].(map[string]any)
	if last["role"] == "model" {
		hasText := false
		for _, raw := range last["parts"].([]any) {
			text, _ := raw.(map[string]any)["text"].(string)
			hasText = hasText || strings.TrimSpace(text) != ""
		}
		if !hasText {
			return nil, fmt.Errorf("Gemini continuation requires assistant text or completed tool results")
		}
		// A new semantic continuation never substitutes for a missing tool result.
		appendContent("user", []any{map[string]any{"text": geminiOpenAIContinuationGeili}})
	}
	native["contents"] = contents
	if len(system) > 0 {
		native["systemInstruction"] = map[string]any{"parts": system}
	}
	return native, nil
}
