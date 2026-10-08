package service

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"time"
)

// Keep a bounded watchdog for long silent thinking, independent of downstream
// heartbeats. Explicitly disabling the ordinary watchdog still disables it.
func longThinkingStreamInterval(cfg *config.Config, model string, ordinary time.Duration) time.Duration {
	if cfg == nil || ordinary <= 0 || (!isClaude55SignedThinkingModel(model) && !isOpenAIGPT6AstraModel(model)) {
		return ordinary
	}
	extended := time.Duration(cfg.Gateway.LongThinkingStreamDataIntervalTimeout) * time.Second
	if extended > ordinary {
		return extended
	}
	return ordinary
}

// Check idle deadlines without waiting another entire thinking interval.
func streamIdleCheckPeriod(interval time.Duration) time.Duration {
	step := interval / 10
	if step > 10*time.Second {
		return 10 * time.Second
	}
	if step <= 0 {
		return interval
	}
	return step
}

const openAIResponsesBareErrorGrace = 2 * time.Second
