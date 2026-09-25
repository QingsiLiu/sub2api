package repository

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestUsageRecoveryManifestHashRejectsTamperingAndCutoff(t *testing.T) {
	from := time.Date(2026, 9, 24, 16, 0, 0, 0, time.UTC)
	cutoff := from.Add(24 * time.Hour)
	c := UsageRecoveryCandidate{RequestID: "local:proof", APIKeyID: 7, Kind: "unattributed_billing_operation", Disposition: "unresolved", UnknownFields: []string{"actual_cost"}}
	m := &UsageRecoveryManifest{Version: 1, From: from, Cutoff: cutoff, ScannedAt: cutoff.Add(time.Hour), Candidates: []UsageRecoveryCandidate{c}}
	var err error
	m.Summary, err = recoverySummary(m.Candidates)
	require.NoError(t, err)
	hash, err := UsageRecoveryManifestDigest(m)
	require.NoError(t, err)
	require.NoError(t, validateRecoveryManifest(m, hash, cutoff))
	require.ErrorContains(t, validateRecoveryManifest(m, hash, cutoff.Add(time.Second)), "cutoff")
	m.Candidates[0].RequestID = "local:tampered"
	require.ErrorContains(t, validateRecoveryManifest(m, hash, cutoff), "SHA256")
}
func TestUsageRecoveryUnknownAmountDoesNotBecomeZero(t *testing.T) {
	known := "10.8422572800"
	summary, err := recoverySummary([]UsageRecoveryCandidate{{Disposition: "recoverable", AmountUSD: &known}, {Disposition: "unresolved", AmountUSD: nil}})
	require.NoError(t, err)
	require.Equal(t, "10.8422572800", summary.KnownAmountUSD)
	require.Equal(t, 1, summary.UnknownAmountCount)
}
func TestUsageRecoveryMappingDoesNotGuessAsyncOrRawIDs(t *testing.T) {
	for _, id := range []string{"local:123", "client:123", "generated:123", "web_search:123", "grok-video:123"} {
		require.True(t, normalRecoveryRequestID(id), id)
	}
	for _, id := range []string{"auapi_image_settle:123", "batch_image_capture:123", "random-upstream-id", "local:", ""} {
		require.False(t, normalRecoveryRequestID(id), id)
	}
}
