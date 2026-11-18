//go:build unit

package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

type providerDurableGeili struct {
	UsageBillingBatchRepository
	UsageSettlementRepository
	calls int
}

func (r *providerDurableGeili) Apply(context.Context, *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	r.calls++
	return &UsageBillingApplyResult{Applied: true}, nil
}
func TestGeiliBillingProviderRetainsDurabilityAndSingleApply(t *testing.T) {
	raw := &providerDurableGeili{}
	repo := ProvideUsageBillingRepositoryGeili(raw)
	require.Same(t, raw, repo)
	require.Implements(t, (*UsageSettlementRepository)(nil), repo)
	got, err := repo.Apply(context.Background(), &UsageBillingCommand{RequestID: "owned-only"})
	require.NoError(t, err)
	require.True(t, got.Applied)
	require.Equal(t, 1, raw.calls)
}
func TestGeiliBillingProviderRejectsUnjournaledBatchSurface(t *testing.T) {
	raw := &struct{ UsageBillingBatchRepository }{}
	require.Panics(t, func() { ProvideUsageBillingRepositoryGeili(raw) })
}
