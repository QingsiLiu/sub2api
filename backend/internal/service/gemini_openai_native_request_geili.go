package service

// Gemini OpenAI compatibility uses one native request representation. Keeping
// this conversion outside the Anthropic bridge preserves budgets and formats.
import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

type geminiOpenAIRequestGeili struct {
	body                       []byte
	model                      string
	stream, chat, includeUsage bool
	mapping                    apicompat.ResponsesClientToolMapping
}

func parseGeminiOpenAIRequestGeili(body []byte, chat bool, mappedModel string) (*geminiOpenAIRequestGeili, error) {
	var source map[string]any
	if err := json.Unmarshal(body, &source); err != nil {
		return nil, fmt.Errorf("invalid request JSON: %w", err)
	}
	if source == nil {
		return nil, fmt.Errorf("request must be an object")
	}
	// Optional SDK fields explicitly set to null have the same meaning as
	// omission in the previous typed request DTOs. Keep this top-level list
	// narrow: zero, false and malformed nested fields still require validation.
	for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "tools", "functions", "reasoning_effort", "temperature", "top_p"} {
		if value, exists := source[key]; exists && value == nil {
			delete(source, key)
		}
	}
	model, _ := source["model"].(string)
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	for _, key := range []string{"stream", "store", "background", "parallel_tool_calls"} {
		if value, ok := source[key]; ok && value != nil {
			if _, ok := value.(bool); !ok {
				return nil, fmt.Errorf("%s must be a boolean", key)
			}
		}
	}
	for _, key := range []string{"reasoning", "text"} {
		if value, ok := source[key]; ok && value != nil {
			if _, ok := value.(map[string]any); !ok {
				return nil, fmt.Errorf("%s must be an object", key)
			}
		}
	}
	if value, ok := source["tools"]; ok && value != nil {
		if _, ok := value.([]any); !ok {
			return nil, fmt.Errorf("tools must be an array")
		}
	}
	if value, ok := source["include"]; ok && value != nil {
		if _, ok := value.([]any); !ok {
			return nil, fmt.Errorf("include must be an array")
		}
	}
	stream, _ := source["stream"].(bool)
	req := source
	if chat {
		var err error
		req, err = geminiChatToResponsesGeili(source)
		if err != nil {
			return nil, err
		}
	}
	if !chat {
		if input, exists := source["input"]; !exists || input == nil {
			if _, legacy := source["messages"]; legacy {
				var err error
				req, err = geminiChatToResponsesGeili(source)
				if err != nil {
					return nil, err
				}
				if budget, exists := source["max_output_tokens"]; exists {
					req["max_output_tokens"] = budget
				}
				if text, exists := source["text"]; exists {
					req["text"] = text
				}
			} else if prompt, ok := source["prompt"].(string); ok {
				req["input"] = prompt
			}
		}
		if _, ok := req["prompt"].(string); ok {
			delete(req, "prompt")
		}
	}

	for _, key := range []string{"conversation", "max_tool_calls", "prompt", "context_management"} {
		if value, ok := req[key]; ok && value != nil {
			return nil, fmt.Errorf("%s is unsupported for Gemini compatibility", key)
		}
	}
	if tier, ok := req["service_tier"].(string); ok && tier != "" && tier != "auto" {
		return nil, fmt.Errorf("service_tier is unsupported for Gemini compatibility")
	}
	if truncation, ok := req["truncation"].(string); ok && truncation != "" && truncation != "disabled" {
		return nil, fmt.Errorf("automatic truncation is unsupported; provide the complete conversation")
	}
	if id, _ := req["previous_response_id"].(string); id != "" {
		return nil, fmt.Errorf("previous_response_id is unsupported; provide the complete conversation in input")
	}
	if store, _ := req["store"].(bool); store {
		return nil, fmt.Errorf("store=true is unsupported for Gemini compatibility")
	}
	if bg, _ := req["background"].(bool); bg {
		return nil, fmt.Errorf("background=true is unsupported for Gemini compatibility")
	}
	if inc, ok := req["include"].([]any); ok {
		for _, v := range inc {
			if v == "reasoning.encrypted_content" {
				return nil, fmt.Errorf("OpenAI encrypted reasoning is unsupported for Gemini compatibility")
			}
		}
	}
	if _, err := liftResponsesAdditionalTools(req); err != nil {
		return nil, err
	}
	if tools, ok := req["tools"].([]any); ok {
		for i, tool := range tools {
			if name, ok := tool.(string); ok {
				if strings.TrimSpace(name) == "" {
					return nil, fmt.Errorf("custom tool names cannot be empty")
				}
				tools[i] = map[string]any{"type": "custom", "name": name}
			}
		}
	}
	mapping, adapted, err := apicompat.AdaptResponsesClientTools(req)
	if err != nil {
		return nil, err
	}
	if adapted {
		encoded, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		req = nil
		if err = json.Unmarshal(encoded, &req); err != nil {
			return nil, err
		}
	}
	native := map[string]any{}
	contents := []any{}
	system := []any{}
	if instructions, _ := req["instructions"].(string); instructions != "" {
		system = append(system, map[string]any{"text": instructions})
	}
	calls := map[string]string{}
	appendContent := func(role string, parts []any) {
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
			appendContent("user", []any{map[string]any{"functionResponse": functionResponse}})
		case "reasoning":
			if enc, _ := item["encrypted_content"].(string); enc != "" {
				return nil, fmt.Errorf("OpenAI encrypted reasoning is unsupported for Gemini compatibility")
			}
			if summary, ok := item["summary"].([]any); ok {
				for _, raw := range summary {
					entry, ok := raw.(map[string]any)
					if !ok || entry["type"] != "summary_text" {
						return nil, fmt.Errorf("unsupported reasoning summary")
					}
					text, ok := entry["text"].(string)
					if !ok {
						return nil, fmt.Errorf("reasoning summary requires text")
					}
					part := map[string]any{"text": text, "thought": true}
					if sig, _ := item["gemini_thought_signature"].(string); sig != "" {
						part["thoughtSignature"] = sig
					}
					appendContent("model", []any{part})
				}
			}

		default:
			return nil, fmt.Errorf("unsupported Gemini input item type %q", typ)
		}
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("input must contain at least one message or function call")
	}
	native["contents"] = contents
	if len(system) > 0 {
		native["systemInstruction"] = map[string]any{"parts": system}
	}
	cfg := map[string]any{"maxOutputTokens": 8192}
	if v, ok := req["max_output_tokens"]; ok {
		n, err := geminiPositiveIntGeili(v, "max_output_tokens")
		if err != nil {
			return nil, err
		}
		cfg["maxOutputTokens"] = n
	}
	for _, pair := range [][2]string{{"temperature", "temperature"}, {"top_p", "topP"}} {
		if v, ok := req[pair[0]]; ok {
			n, ok := v.(float64)
			if !ok {
				return nil, fmt.Errorf("%s must be numeric", pair[0])
			}
			cfg[pair[1]] = n
		}
	}
	if stop, ok := req["stop"]; ok && stop != nil {
		var seq []any
		switch v := stop.(type) {
		case string:
			seq = []any{v}
		case []any:
			seq = v
		default:
			return nil, fmt.Errorf("stop must be a string or string array")
		}
		if len(seq) > 5 {
			return nil, fmt.Errorf("Gemini supports at most 5 stop sequences")
		}
		for _, v := range seq {
			s, ok := v.(string)
			if !ok || s == "" {
				return nil, fmt.Errorf("stop sequences must be nonempty strings")
			}
		}
		if len(seq) > 0 {
			cfg["stopSequences"] = seq
		}
	}
	if text, ok := req["text"].(map[string]any); ok {
		if verbosity, _ := text["verbosity"].(string); verbosity != "" {
			return nil, fmt.Errorf("text.verbosity is unsupported for Gemini compatibility")
		}
		if raw, exists := text["format"]; exists {
			format, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("text.format must be an object")
			}
			if err := geminiOpenAIFormatGeili(cfg, format); err != nil {
				return nil, err
			}
		}
	}
	if r, ok := req["reasoning"].(map[string]any); ok {
		if raw, exists := r["effort"]; exists {
			effort, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("reasoning.effort must be a string")
			}
			if effort != "" {
				thinking, err := geminiOpenAIThinkingGeili(mappedModel, effort)
				if err != nil {
					return nil, err
				}
				cfg["thinkingConfig"] = thinking
			}
		}
		if raw, exists := r["summary"]; exists {
			summary, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("reasoning.summary must be a string")
			}
			switch summary {
			case "":
			case "auto":
				if _, err := geminiOpenAIThinkingGeili(mappedModel, "high"); err != nil {
					return nil, fmt.Errorf("reasoning.summary=auto requires an explicit supported Gemini upstream model")
				}
				thinking, ok := cfg["thinkingConfig"].(map[string]any)
				if !ok {
					thinking = map[string]any{}
					cfg["thinkingConfig"] = thinking
				}
				thinking["includeThoughts"] = true
			default:
				return nil, fmt.Errorf("reasoning.summary %q is unsupported for Gemini compatibility", summary)
			}
		}
	}
	native["generationConfig"] = cfg
	decls := []any{}
	strictTools := false
	names := map[string]bool{}
	if tools, ok := req["tools"].([]any); ok {
		for _, v := range tools {
			tool, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("tools must be objects")
			}
			typ, _ := tool["type"].(string)
			if typ != "function" {
				return nil, fmt.Errorf("unsupported Gemini tool type %q", typ)
			}
			if raw, exists := tool["strict"]; exists && raw != nil {
				strict, ok := raw.(bool)
				if !ok {
					return nil, fmt.Errorf("tool.strict must be a boolean")
				}
				strictTools = strictTools || strict
			}
			name, _ := tool["name"].(string)
			if name == "" || names[name] {
				return nil, fmt.Errorf("function names must be nonempty and unique")
			}
			names[name] = true
			d := map[string]any{"name": name}
			if desc, _ := tool["description"].(string); desc != "" {
				d["description"] = desc
			}
			if raw, exists := tool["parameters"]; exists {
				if _, ok := raw.(map[string]any); !ok {
					return nil, fmt.Errorf("tool %s parameters must be a JSON schema object", name)
				}
			}
			if schema, ok := tool["parameters"].(map[string]any); ok {
				if err := geminiToolJSONSchemaGeili(schema); err != nil {
					return nil, fmt.Errorf("tool %s: %w", name, err)
				}
				d["parametersJsonSchema"] = schema
			} else {
				d["parametersJsonSchema"] = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			decls = append(decls, d)
		}
	}
	if len(decls) > 0 {
		native["tools"] = []any{map[string]any{"functionDeclarations": decls}}
	}
	if choice, ok := req["tool_choice"]; ok && choice != nil {
		tc := map[string]any{}
		switch v := choice.(type) {
		case string:
			switch v {
			case "auto":
				tc["mode"] = "AUTO"
			case "none":
				tc["mode"] = "NONE"
			case "required":
				tc["mode"] = "ANY"
			default:
				return nil, fmt.Errorf("unsupported tool_choice %q", v)
			}
		case map[string]any:
			name, _ := v["name"].(string)
			if v["type"] != "function" || !names[name] {
				return nil, fmt.Errorf("tool_choice must select a declared function")
			}
			tc["mode"] = "ANY"
			tc["allowedFunctionNames"] = []string{name}
		default:
			return nil, fmt.Errorf("invalid tool_choice")
		}
		native["toolConfig"] = map[string]any{"functionCallingConfig": tc}
	}
	if strictTools {
		config, ok := native["toolConfig"].(map[string]any)
		if !ok {
			config = map[string]any{}
			native["toolConfig"] = config
		}
		callConfig, ok := config["functionCallingConfig"].(map[string]any)
		if !ok {
			callConfig = map[string]any{}
			config["functionCallingConfig"] = callConfig
		}
		if mode, _ := callConfig["mode"].(string); mode == "" || mode == "AUTO" {
			callConfig["mode"] = "VALIDATED"
		}
	}
	if parallel, ok := req["parallel_tool_calls"].(bool); ok && !parallel {
		return nil, fmt.Errorf("parallel_tool_calls=false is unsupported by Gemini native function calling")
	}
	data, err := json.Marshal(native)
	if err != nil {
		return nil, err
	}
	data = ensureGeminiFunctionCallThoughtSignatures(data)
	usage := false
	if options, ok := source["stream_options"].(map[string]any); ok {
		usage, _ = options["include_usage"].(bool)
	}
	return &geminiOpenAIRequestGeili{body: data, model: model, stream: stream, chat: chat, includeUsage: usage, mapping: mapping}, nil
}

func geminiPositiveIntGeili(v any, key string) (int, error) {
	n, ok := v.(float64)
	if !ok || n <= 0 || n != float64(int(n)) {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return int(n), nil
}

func geminiOpenAIContentGeili(value any) ([]any, error) {
	if s, ok := value.(string); ok {
		return []any{map[string]any{"text": s}}, nil
	}
	arr, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("message content must be a string or array")
	}
	out := []any{}
	for _, v := range arr {
		part, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("content parts must be objects")
		}
		typ, _ := part["type"].(string)
		switch typ {
		case "input_text", "output_text", "text":
			text, ok := part["text"].(string)
			if !ok {
				return nil, fmt.Errorf("text part requires text")
			}
			out = append(out, map[string]any{"text": text})
		case "input_image":
			if detail, ok := part["detail"]; ok && detail != nil && detail != "auto" {
				return nil, fmt.Errorf("explicit image detail is unsupported for Gemini compatibility")
			}
			uri, _ := part["image_url"].(string)
			p, err := geminiOpenAIImageGeili(uri)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		default:
			return nil, fmt.Errorf("unsupported Gemini content part %q", typ)
		}
	}
	return out, nil
}
func geminiOpenAIImageGeili(uri string) (map[string]any, error) {
	if strings.HasPrefix(uri, "data:") {
		header, data, ok := strings.Cut(strings.TrimPrefix(uri, "data:"), ",")
		if !ok || !strings.HasSuffix(header, ";base64") {
			return nil, fmt.Errorf("image data must use base64")
		}
		mime := strings.TrimSuffix(header, ";base64")
		if !isGeminiInlineImageMIMEType(mime) {
			return nil, fmt.Errorf("unsupported image MIME type")
		}
		if _, err := base64.StdEncoding.DecodeString(data); err != nil || data == "" {
			return nil, fmt.Errorf("invalid base64 image")
		}
		return map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": data}}, nil
	}
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http" && parsed.Scheme != "gs") {
		return nil, fmt.Errorf("image_url must be a data URI or HTTP/GS URL")
	}
	return map[string]any{"fileData": map[string]any{"fileUri": uri}}, nil
}
func geminiOpenAIFormatGeili(cfg, format map[string]any) error {
	typ, _ := format["type"].(string)
	switch typ {
	case "", "text":
		return nil
	case "json_object":
		cfg["responseMimeType"] = "application/json"
		return nil
	case "json_schema":
		schema, ok := format["schema"].(map[string]any)
		if !ok {
			return fmt.Errorf("text.format.schema must be an object")
		}
		if err := geminiJSONSchemaGeili(schema); err != nil {
			return err
		}
		cfg["responseMimeType"] = "application/json"
		cfg["responseJsonSchema"] = schema
		return nil
	default:
		return fmt.Errorf("unsupported text.format type %q", typ)
	}
}

// JSON-schema is sent through Gemini's JSON-schema field, never silently pruned.
func geminiJSONSchemaGeili(schema map[string]any) error {
	return geminiJSONSchemaModeGeili(schema, false)
}
func geminiToolJSONSchemaGeili(schema map[string]any) error {
	return geminiJSONSchemaModeGeili(schema, true)
}
func geminiJSONSchemaModeGeili(schema map[string]any, tool bool) error {
	for key, value := range schema {
		switch key {
		case "type", "title", "description", "enum", "minimum", "maximum", "minItems", "maxItems", "format", "required", "propertyOrdering":
		case "pattern", "minLength", "maxLength":
			if !tool {
				return fmt.Errorf("unsupported Gemini JSON schema keyword %q", key)
			}
		case "properties":
			m, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("schema %s must be an object", key)
			}
			for _, v := range m {
				sub, ok := v.(map[string]any)
				if !ok {
					return fmt.Errorf("schema child must be an object")
				}
				if err := geminiJSONSchemaModeGeili(sub, tool); err != nil {
					return err
				}
			}
		case "items", "additionalProperties":
			if b, ok := value.(bool); ok {
				_ = b
				continue
			}
			m, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("schema %s must be an object or boolean", key)
			}
			if err := geminiJSONSchemaModeGeili(m, tool); err != nil {
				return err
			}
		case "anyOf":
			arr, ok := value.([]any)
			if !ok {
				return fmt.Errorf("schema %s must be an array", key)
			}
			for _, v := range arr {
				m, ok := v.(map[string]any)
				if !ok {
					return fmt.Errorf("schema branch must be an object")
				}
				if err := geminiJSONSchemaModeGeili(m, tool); err != nil {
					return err
				}
			}
		case "$ref":
			return fmt.Errorf("schema references are unsupported; inline the schema")
		default:
			return fmt.Errorf("unsupported Gemini JSON schema keyword %q", key)
		}
	}
	return nil
}
func geminiOpenAIThinkingGeili(model, effort string) (map[string]any, error) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	id := strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(id, "gemini-2.5-pro") || strings.HasPrefix(id, "gemini-2.5-flash") {
		// Preserve the established effort budgets. Do not invent a zero
		// budget for "none" or pretend that 2.5 accepts thinkingLevel.
		budgets := map[string]int{"low": 1024, "medium": 4096, "high": 10240}
		budget, ok := budgets[effort]
		if !ok {
			return nil, fmt.Errorf("upstream model %s does not support reasoning effort %q", model, effort)
		}
		return map[string]any{"thinkingBudget": budget, "includeThoughts": true}, nil
	}
	supported := map[string]bool{}
	switch {
	case strings.HasPrefix(id, "gemini-3.1-pro"):
		supported = map[string]bool{"low": true, "medium": true, "high": true}
	case strings.HasPrefix(id, "gemini-3-pro"):
		supported = map[string]bool{"low": true, "high": true}
	case strings.HasPrefix(id, "gemini-3-flash"), strings.HasPrefix(id, "gemini-3.1-flash"):
		supported = map[string]bool{"minimal": true, "low": true, "medium": true, "high": true}
	default:
		return nil, fmt.Errorf("reasoning effort requires an explicit supported Gemini upstream model")
	}
	if !supported[effort] {
		return nil, fmt.Errorf("upstream model %s does not support reasoning effort %q", model, effort)
	}
	return map[string]any{"thinkingLevel": strings.ToUpper(effort), "includeThoughts": true}, nil
}

// Convert Chat's shape to the shared native input representation without an
// Anthropic request or a bridge that changes explicit budgets and reasoning.
func geminiChatToResponsesGeili(source map[string]any) (map[string]any, error) {
	if value, ok := source["seed"]; ok && value != nil {
		return nil, fmt.Errorf("seed is unsupported for Gemini compatibility")
	}
	if n, ok := source["n"]; ok && n != float64(1) {
		return nil, fmt.Errorf("Gemini compatibility supports n=1 only")
	}
	for _, k := range []string{"logprobs", "top_logprobs", "audio", "modalities", "prediction", "frequency_penalty", "presence_penalty", "logit_bias", "seed"} {
		if v, ok := source[k]; ok && v != nil && v != false && v != float64(0) {
			return nil, fmt.Errorf("%s is unsupported for Gemini compatibility", k)
		}
	}
	req := map[string]any{}
	for _, k := range []string{"model", "stream", "instructions", "temperature", "top_p", "stop", "parallel_tool_calls", "previous_response_id", "store", "background", "include", "conversation", "max_tool_calls", "prompt", "context_management", "service_tier", "truncation"} {
		if v, ok := source[k]; ok {
			req[k] = v
		}
	}
	for _, k := range []string{"max_tokens", "max_completion_tokens"} {
		if v, ok := source[k]; ok {
			if _, err := geminiPositiveIntGeili(v, k); err != nil {
				return nil, err
			}
			req["max_output_tokens"] = v
		}
	}
	if r, ok := source["reasoning_effort"]; ok {
		req["reasoning"] = map[string]any{"effort": r}
	}
	if raw, ok := source["response_format"]; ok && raw != nil {
		f, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("response_format must be an object")
		}
		format := map[string]any{"type": f["type"]}
		if f["type"] == "json_schema" {
			nested, ok := f["json_schema"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("response_format.json_schema must be an object")
			}
			for k, v := range nested {
				format[k] = v
			}
		}
		req["text"] = map[string]any{"format": format}
	}
	tools := []any{}
	if raw, ok := source["tools"]; ok {
		arr, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("tools must be an array")
		}
		for _, v := range arr {
			tool, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("tools must contain objects")
			}
			if tool["type"] == "function" {
				f, ok := tool["function"].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("function tool requires function")
				}
				copy := map[string]any{"type": "function"}
				for k, v := range f {
					copy[k] = v
				}
				tools = append(tools, copy)
			} else {
				tools = append(tools, tool)
			}
		}
	}
	if raw, ok := source["functions"]; ok {
		arr, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("functions must be an array")
		}
		for _, v := range arr {
			f, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("functions must contain objects")
			}
			copy := map[string]any{"type": "function"}
			for k, v := range f {
				copy[k] = v
			}
			tools = append(tools, copy)
		}
	}
	if len(tools) > 0 {
		req["tools"] = tools
	}
	if raw, ok := source["tool_choice"]; ok {
		if choice, ok := raw.(map[string]any); ok {
			f, ok := choice["function"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("named Chat tool_choice requires function")
			}
			req["tool_choice"] = map[string]any{"type": "function", "name": f["name"]}
		} else {
			req["tool_choice"] = raw
		}
	} else if raw, ok := source["function_call"]; ok {
		if f, ok := raw.(map[string]any); ok {
			req["tool_choice"] = map[string]any{"type": "function", "name": f["name"]}
		} else {
			req["tool_choice"] = raw
		}
	}
	messages, ok := source["messages"].([]any)
	if !ok {
		return nil, fmt.Errorf("messages must be an array")
	}
	input := []any{}
	legacyIDs := map[string]string{}
	for _, raw := range messages {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("messages must contain objects")
		}
		role, _ := m["role"].(string)
		switch role {
		case "system", "developer", "user", "assistant":
			if role == "assistant" {
				reason, _ := m["reasoning_content"].(string)
				if reason == "" {
					reason, _ = m["reasoning"].(string)
				}
				if reason != "" {
					input = append(input, map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": reason}}})
				}
			}
			if content, exists := m["content"]; exists && content != nil {
				normalized, err := geminiChatContentGeili(content)
				if err != nil {
					return nil, err
				}
				input = append(input, map[string]any{"role": role, "content": normalized})
			}
			if raw, exists := m["tool_calls"]; exists {
				if role != "assistant" {
					return nil, fmt.Errorf("tool_calls require assistant role")
				}
				arr, ok := raw.([]any)
				if !ok {
					return nil, fmt.Errorf("tool_calls must be an array")
				}
				for _, v := range arr {
					tc, ok := v.(map[string]any)
					if !ok || tc["type"] != "function" {
						return nil, fmt.Errorf("only function tool_calls are supported")
					}
					f, ok := tc["function"].(map[string]any)
					if !ok {
						return nil, fmt.Errorf("tool_call requires function")
					}
					args := f["arguments"]
					if args == nil || args == "" {
						args = "{}"
					}
					input = append(input, map[string]any{"type": "function_call", "call_id": tc["id"], "name": f["name"], "arguments": args, "gemini_thought_signature": tc["gemini_thought_signature"]})
				}
			}
			if f, ok := m["function_call"].(map[string]any); ok {
				if role != "assistant" {
					return nil, fmt.Errorf("function_call requires assistant role")
				}
				name, _ := f["name"].(string)
				id := "call_legacy_" + fmt.Sprint(len(input))
				legacyIDs[name] = id
				args := f["arguments"]
				if args == nil || args == "" {
					args = "{}"
				}
				input = append(input, map[string]any{"type": "function_call", "call_id": id, "name": name, "arguments": args})
			}
			if refusal, _ := m["refusal"].(string); refusal != "" {
				return nil, fmt.Errorf("refusal history is unsupported by Gemini compatibility")
			}
		case "tool", "function":
			id, _ := m["tool_call_id"].(string)
			if role == "function" {
				name, _ := m["name"].(string)
				id = legacyIDs[name]
			}
			content, err := geminiChatContentGeili(m["content"])
			if err != nil {
				return nil, err
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": id, "output": content})
		default:
			return nil, fmt.Errorf("unsupported Chat message role %q", role)
		}
	}
	req["input"] = input
	return req, nil
}
func geminiChatContentGeili(raw any) (any, error) {
	if s, ok := raw.(string); ok {
		return s, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("message content must be a string or array")
	}
	out := []any{}
	for _, v := range arr {
		p, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("content parts must be objects")
		}
		switch p["type"] {
		case "text":
			out = append(out, map[string]any{"type": "input_text", "text": p["text"]})
		case "image_url":
			image, ok := p["image_url"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("image_url must be an object")
			}
			out = append(out, map[string]any{"type": "input_image", "image_url": image["url"], "detail": image["detail"]})
		default:
			return nil, fmt.Errorf("unsupported Chat content part %q", p["type"])
		}
	}
	return out, nil
}
