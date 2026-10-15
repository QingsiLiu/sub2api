package service

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// IsLongThinkingRequestModel controls only waiting budgets, never protocol
// validation or signed-thinking handling. Share it between the upstream idle
// watchdog and composite-route deadline so one cannot cut the other short.
func IsLongThinkingRequestModel(model string) bool {
	id := strings.ToLower(strings.TrimSpace(model))
	if slash := strings.LastIndexByte(id, '/'); slash >= 0 {
		id = strings.TrimSpace(id[slash+1:])
	}
	id = strings.ReplaceAll(id, "_", "-")
	for _, prefix := range []string{"us.", "eu.", "apac.", "jp.", "au.", "us-gov.", "global.", "anthropic."} {
		id = strings.TrimPrefix(id, prefix)
	}
	if mapped, ok := claude.ModelIDReverseOverrides[id]; ok {
		id = mapped
	}
	// Media/embedding requests retain their own deadlines. In particular, do
	// not let a reasoning-family prefix include Gemini/GPT image or audio IDs.
	parts := strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == '_' || r == ':' })
	for _, part := range parts {
		switch part {
		case "image", "images", "video", "audio", "realtime", "embedding", "embeddings", "rerank", "tts", "transcribe", "transcription", "imagine":
			return false
		}
	}
	if strings.Contains(id, "non-reasoning") || strings.Contains(id, "non-thinking") || strings.Contains(id, "nonthinking") {
		return false
	}
	// Explicit reasoning aliases also cover third-party families (e.g. Qwen
	// thinking and DeepSeek R1) without adding each dated release to a whitelist.
	for _, part := range parts {
		if part == "thinking" || part == "reasoning" || part == "reasoner" || part == "r1" {
			return true
		}
	}
	if len(claude.EffortLevelsForModel(id)) > 0 || longThinkingModelVersionGeili(id, "claude-opus-", 4, 0) ||
		longThinkingModelVersionGeili(id, "claude-sonnet-", 4, 0) || longThinkingModelVersionGeili(id, "claude-haiku-", 4, 5) ||
		longThinkingModelVersionGeili(id, "claude-", 4, 0) ||
		id == "claude-3-7-sonnet" || strings.HasPrefix(id, "claude-3-7-sonnet-") {
		return true
	}
	if longThinkingModelVersionGeili(openai.CanonicalizeOpenAIModelAliasSpelling(id), "gpt-", 5, 0) ||
		longThinkingModelVersionGeili(id, "o", 1, 0) || longThinkingModelVersionGeili(id, "gemini-", 2, 5) ||
		longThinkingModelVersionGeili(id, "grok-", 4, 0) ||
		id == "grok-3-mini" || strings.HasPrefix(id, "grok-3-mini-") ||
		longThinkingModelVersionGeili(id, "deepseek-v", 3, 1) || longThinkingModelVersionGeili(id, "kimi-k", 2, 5) ||
		longThinkingModelVersionGeili(id, "glm-", 4, 5) || longThinkingModelVersionGeili(id, "minimax-m", 2, 0) ||
		id == "codex" || strings.HasPrefix(id, "codex-") {
		return true
	}
	return !strings.Contains(id, "-coder") && !strings.Contains(id, "-instruct") && longThinkingModelVersionGeili(id, "qwen", 3, 0)
}

// Read a numeric family version, including decimal and Claude's dashed minor
// versions. Checking the generation avoids another per-release timeout patch.
func longThinkingModelVersionGeili(id, prefix string, minMajor, minMinor int) bool {
	rest, ok := strings.CutPrefix(id, prefix)
	if !ok {
		return false
	}
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 || (end < len(rest) && rest[end] != '.' && rest[end] != '-' && rest[end] != ':' && rest[end] != '@') {
		return false
	}
	major, err := strconv.Atoi(rest[:end])
	if err != nil || major < minMajor {
		return false
	}
	if major > minMajor || minMinor == 0 {
		return true
	}
	if end == len(rest) {
		return false
	}
	// Outside Claude's dashed versions, a dash introduces a release date or
	// variant, not a minor generation (e.g. deepseek-v3-0324 and glm-4-0520).
	if rest[end] != '.' && !(rest[end] == '-' && strings.HasPrefix(prefix, "claude-")) {
		return false
	}
	rest = rest[end+1:]
	end = 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	minor, err := strconv.Atoi(rest[:end])
	return err == nil && minor >= minMinor
}
