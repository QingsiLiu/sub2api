package main

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSubjectRefDerivationAndExplicitReference(t *testing.T) {
	cfg := &config.Config{TrainingData: config.TrainingDataConfig{SubjectHMACKey: "0123456789abcdef0123456789abcdef"}}
	derived, err := subjectRef(cfg, "user", "", 42)
	require.NoError(t, err)
	require.Len(t, derived, 64)
	require.NotEqual(t, "42", derived)

	explicitRef := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	explicit, err := subjectRef(cfg, "api_key", explicitRef, 0)
	require.NoError(t, err)
	require.Equal(t, explicitRef, explicit)
	require.Error(t, func() error {
		_, err := subjectRef(cfg, "user", explicitRef, 42)
		return err
	}())
	_, err = subjectRef(cfg, "user", "not-a-hash", 0)
	require.Error(t, err)
}

func TestSplitListIsStableAndUnique(t *testing.T) {
	require.Equal(t, []string{"buyer-a", "buyer-b"}, splitList(" buyer-b,buyer-a,buyer-b "))
}

func TestOptionalTimeUsesRFC3339(t *testing.T) {
	fallback := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	got, err := optionalTime("", fallback)
	require.NoError(t, err)
	require.Equal(t, fallback, got)
	got, err = optionalTime("2026-08-20T12:30:00+08:00", fallback)
	require.NoError(t, err)
	require.Equal(t, "2026-08-20T04:30:00Z", got.Format(time.RFC3339))
}
