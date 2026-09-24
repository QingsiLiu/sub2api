package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// DefaultOpenAISyncModelIDs is the curated set used by the admin "sync latest
// supported models" action. It is intentionally smaller than the complete
// OpenAI model catalog exposed by the gateway.
func DefaultOpenAISyncModelIDs() []string {
	return []string{
		"codex-auto-review",
		"gpt-5.5",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-6-astra",
		"gpt-reserve",
		"gpt-5.6-luna",
		"gpt-6-sol",
		"gpt-6-luna",
	}
}

// OpenAISyncModelCandidates returns every registered OpenAI model plus the
// reserved sync-only ID. The reserved ID is intentionally not part of the
// runtime /v1/models response until it has a real upstream definition.
func OpenAISyncModelCandidates() []string {
	ids := append([]string(nil), openai.DefaultModelIDs()...)
	for _, id := range DefaultOpenAISyncModelIDs() {
		if id == "gpt-reserve" {
			ids = append(ids, id)
			break
		}
	}
	return ids
}

// NormalizeOpenAISyncModelIDs validates a submitted catalog. UI editors normalize duplicates before submitting.
func NormalizeOpenAISyncModelIDs(raw []string) ([]string, error) {
	known := make(map[string]struct{}, len(OpenAISyncModelCandidates()))
	for _, modelID := range OpenAISyncModelCandidates() {
		known[modelID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		modelID := strings.TrimSpace(value)
		if modelID == "" {
			return nil, fmt.Errorf("openai_sync_model_ids contains an empty model")
		}
		if _, ok := known[modelID]; !ok {
			return nil, fmt.Errorf("openai_sync_model_ids contains unknown model %q", modelID)
		}
		if _, ok := seen[modelID]; ok {
			return nil, fmt.Errorf("openai_sync_model_ids contains duplicate model %q", modelID)
		}
		seen[modelID] = struct{}{}
		out = append(out, modelID)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("openai_sync_model_ids must contain at least one model")
	}
	return out, nil
}

func ParseOpenAISyncModelIDs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return DefaultOpenAISyncModelIDs()
	}
	var values []string
	err := json.Unmarshal([]byte(raw), &values)
	if err == nil {
		var normalized []string
		normalized, err = NormalizeOpenAISyncModelIDs(values)
		if err == nil {
			return normalized
		}
	}
	slog.Warn("invalid stored OpenAI sync catalog", "error", err)
	return DefaultOpenAISyncModelIDs()
}

func defaultOpenAISyncModelIDsJSON() string {
	data, _ := json.Marshal(DefaultOpenAISyncModelIDs())
	return string(data)
}
