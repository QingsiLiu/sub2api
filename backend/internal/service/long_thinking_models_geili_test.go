package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestLongThinkingModelFamiliesGeili(t *testing.T) {
	for _, model := range []string{
		"claude-opus-5", "claude-sonnet-5", "claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8",
		"claude-sonnet-4-5-20250929", "claude-haiku-4-5", "claude-haiku-5-5", "claude-3-7-sonnet-20250219",
		"claude-fable-5-1", "claude-mythos-preview", "claude-opus-5-5", "claude-opus-7",
		"anthropic/claude-opus-5", "global.anthropic.claude-opus-5-v1:0", "claude-sonnet-4-6@20260217",
		"gpt-5", "gpt-5.2", "gpt-5.5", "gpt-6", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna",
		" GPT-6.1-SOL ", "openai/gpt-5.3-codex", "gpt-7-next", "gpt-5.4-mini", "codex-max",
		"o1", "openai/o3", "o4-mini", "models/gemini-2.5-pro", "google/gemini-3.1-pro-preview",
		"gemini-2.5-flash", "gemini-3.5-flash", "gemini-4-pro", "deepseek-reasoner", "deepseek/deepseek-r1-0528",
		"deepseek-v3.2", "deepseek-v4-pro", "grok-3-mini", "grok-4.6", "x-ai/grok-4.20-0309-reasoning",
		"kimi-k2-thinking", "moonshotai/kimi-k2.5", "qwen/qwen3-235b-thinking", "qwen3-32b", "glm-5-reasoning",
		"glm-4.5", "zai-org/glm-5.3", "MiniMax-M2.1", "claude-sonnet-4@20250514",
	} {
		t.Run(model, func(t *testing.T) {
			require.True(t, IsLongThinkingRequestModel(model))
			cfg := &config.Config{Gateway: config.GatewayConfig{LongThinkingStreamDataIntervalTimeout: 600}}
			require.Equal(t, 600*time.Second, longThinkingStreamInterval(cfg, model, 180*time.Second))
		})
	}
	for _, model := range []string{
		"", "unknown-alias", "gpt-4o", "gpt-4.1", "gpt-image-2", "gpt-5-audio", "gpt-6-realtime",
		"gemini-2.0-flash", "gemini-2.5-flash-image", "gemini-3.1-flash-image-preview", "text-embedding-3-large",
		"grok-imagine-video", "grok-4.20-0309-non-reasoning", "custom-non-thinking", "deepseek-chat",
		"claude-3-haiku", "claude-3-5-sonnet", "o1garbage", "gpt-5garbage", "gemini-2.0-thinking-image",
		"qwen3-coder", "qwen3-instruct", "qwen2.5-72b", "glm-4", "kimi-k2", "deepseek-v3-0324", "glm-4-0520",
		"grok-4.20-non_reasoning", "unknown_non_thinking",
	} {
		t.Run(model, func(t *testing.T) { require.False(t, IsLongThinkingRequestModel(model)) })
	}
}

func TestLongThinkingTimeoutOverridesGeili(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{LongThinkingStreamDataIntervalTimeout: 600}}
	for _, model := range []string{"claude-opus-5", "gpt-6-sol", "gemini-3.1-pro", "deepseek-reasoner", "grok-4.6"} {
		require.Zero(t, longThinkingStreamInterval(cfg, model, 0), "explicitly disabled idle watchdog stays disabled")
		require.Equal(t, 900*time.Second, longThinkingStreamInterval(cfg, model, 900*time.Second), "never shorten a larger configured budget")
		require.Equal(t, 180*time.Second, longThinkingStreamInterval(nil, model, 180*time.Second))
	}
	cfg.Gateway.LongThinkingStreamDataIntervalTimeout = 0
	require.Equal(t, 180*time.Second, longThinkingStreamInterval(cfg, "claude-opus-5", 180*time.Second))
	cfg.Gateway.LongThinkingStreamDataIntervalTimeout = 60
	require.Equal(t, 180*time.Second, longThinkingStreamInterval(cfg, "claude-opus-5", 180*time.Second))
}
