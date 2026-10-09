package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRequestRecoveryGeiliConfigDefaultsAndExplicitBudget(t *testing.T) {
	for _, tc := range []struct {
		name     string
		env      string
		file     string
		explicit bool
		want     time.Duration
	}{
		{name: "default", want: 1800 * time.Second},
		{name: "explicit-default-env", env: "600", explicit: true, want: 600 * time.Second},
		{name: "explicit-short-env", env: "90", explicit: true, want: 90 * time.Second},
		{name: "explicit-default-file", file: "gateway:\n  key_group_request_timeout_seconds: 600\n", explicit: true, want: 600 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			if tc.env != "" {
				t.Setenv("GATEWAY_KEY_GROUP_REQUEST_TIMEOUT_SECONDS", tc.env)
			}
			if tc.file != "" {
				path := filepath.Join(t.TempDir(), "config.yaml")
				require.NoError(t, os.WriteFile(path, []byte(tc.file), 0600))
				t.Setenv("CONFIG_FILE", path)
			}
			cfg, err := Load()
			require.NoError(t, err)
			require.Equal(t, tc.explicit, cfg.Gateway.KeyGroupRequestTimeoutExplicit)
			require.Equal(t, tc.want, cfg.Gateway.KeyGroupRequestBudget(true))
			require.Equal(t, 600*time.Second, cfg.Gateway.RequestRecoveryBudget())
		})
	}
}

func TestRequestRecoveryGeiliConfigBoundsAndOverrides(t *testing.T) {
	for _, value := range []int{0, 1, 600, 1800, 86400} {
		require.NoError(t, validateRequestRecoveryConfig(GatewayConfig{RequestRecoveryTimeoutSeconds: value, KeyGroupLongThinkingRequestTimeoutSeconds: value}))
	}
	for _, value := range []int{-1, 86401} {
		require.Error(t, validateRequestRecoveryConfig(GatewayConfig{RequestRecoveryTimeoutSeconds: value}))
		require.Error(t, validateRequestRecoveryConfig(GatewayConfig{KeyGroupLongThinkingRequestTimeoutSeconds: value}))
	}
	cfg := GatewayConfig{RequestRecoveryTimeoutSeconds: 90, KeyGroupLongThinkingRequestTimeoutSeconds: 1200}
	require.Equal(t, 90*time.Second, cfg.RequestRecoveryBudget())
	require.Equal(t, 1200*time.Second, cfg.KeyGroupRequestBudget(true))
	require.Equal(t, 600*time.Second, cfg.KeyGroupRequestBudget(false))
	cfg.KeyGroupRequestTimeoutSeconds = 45
	require.Equal(t, 45*time.Second, cfg.KeyGroupRequestBudget(true))
}

func TestRequestRecoveryGeiliConfigEnvOverrides(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("GATEWAY_REQUEST_RECOVERY_TIMEOUT_SECONDS", "90")
	t.Setenv("GATEWAY_KEY_GROUP_LONG_THINKING_REQUEST_TIMEOUT_SECONDS", "1200")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 90*time.Second, cfg.Gateway.RequestRecoveryBudget())
	require.Equal(t, 1200*time.Second, cfg.Gateway.KeyGroupRequestBudget(true))
}
