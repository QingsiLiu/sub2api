package config

import (
	"fmt"
	"time"
)

func (g GatewayConfig) RequestRecoveryBudget() time.Duration {
	if g.RequestRecoveryTimeoutSeconds > 0 {
		return time.Duration(g.RequestRecoveryTimeoutSeconds) * time.Second
	}
	return 600 * time.Second
}

func (g GatewayConfig) KeyGroupRequestBudget(longThinking bool) time.Duration {
	ordinary := g.KeyGroupRequestTimeoutSeconds
	if ordinary <= 0 {
		ordinary = 600
	}
	// Config literals with a non-default value are also explicit. Runtime Load
	// records whether even the default value was deliberately configured.
	if !longThinking || g.KeyGroupRequestTimeoutExplicit || (ordinary != 600) {
		return time.Duration(ordinary) * time.Second
	}
	extended := g.KeyGroupLongThinkingRequestTimeoutSeconds
	if extended <= 0 {
		extended = 1800
	}
	return time.Duration(extended) * time.Second
}

func validateRequestRecoveryConfig(g GatewayConfig) error {
	for _, field := range []struct {
		name  string
		value int
	}{
		{"gateway.request_recovery_timeout_seconds", g.RequestRecoveryTimeoutSeconds},
		{"gateway.key_group_long_thinking_request_timeout_seconds", g.KeyGroupLongThinkingRequestTimeoutSeconds},
	} {
		if field.value < 0 || field.value > 86400 {
			return fmt.Errorf("%s must be between 0 and 86400 seconds", field.name)
		}
	}
	return nil
}
