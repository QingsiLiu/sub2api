package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type publicPricingSettingsRepoStub struct {
	SettingRepository
	values map[string]string
	err    error
}

func (s *publicPricingSettingsRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func TestGetPublicPricingRuntimeDefaults(t *testing.T) {
	service := NewSettingService(&publicPricingSettingsRepoStub{values: map[string]string{}}, nil)

	runtime, err := service.GetPublicPricingRuntime(context.Background())
	require.NoError(t, err)
	require.True(t, runtime.Enabled)
	require.Equal(t, []int64{4, 27, 80, 47, 46, 41, 67, 82}, runtime.GroupIDs)
	require.NotContains(t, runtime.GroupIDs, int64(34))
	require.NotContains(t, runtime.GroupIDs, int64(63))
	require.NotContains(t, runtime.ModelIDs, "gpt-image-2")
	require.Equal(t, "6.8", runtime.USDCNYReference.String())
	require.Equal(t, defaultPublicPricingFXSource, runtime.FXSource)
	require.Equal(t, "2026-08-14", runtime.FXEffectiveDate)
	require.True(t, runtime.HasOfficialCNYComparison())
	_, native := runtime.NativeCNYGroupIDs[82]
	require.True(t, native)
}

func TestGetPublicPricingRuntimeExplicitBlankFXDisablesSavings(t *testing.T) {
	service := NewSettingService(&publicPricingSettingsRepoStub{values: map[string]string{
		SettingKeyPublicPricingUSDCNYReference: "",
		SettingKeyPublicPricingFXSource:        "",
		SettingKeyPublicPricingFXEffectiveDate: "",
	}}, nil)

	runtime, err := service.GetPublicPricingRuntime(context.Background())
	require.NoError(t, err)
	require.Nil(t, runtime.USDCNYReference)
	require.False(t, runtime.HasOfficialCNYComparison())
}

func TestGetPublicPricingRuntimeParsesCustomListsAndDeduplicates(t *testing.T) {
	service := NewSettingService(&publicPricingSettingsRepoStub{values: map[string]string{
		SettingKeyPublicPricingGroupIDs:          "4, 27, 4",
		SettingKeyPublicPricingModelIDs:          `["GPT-5.4", "gpt-5.4", "claude-opus-5"]`,
		SettingKeyPublicPricingNativeCNYGroupIDs: `[82,82]`,
	}}, nil)

	runtime, err := service.GetPublicPricingRuntime(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int64{4, 27}, runtime.GroupIDs)
	require.Equal(t, []string{"gpt-5.4", "claude-opus-5"}, runtime.ModelIDs)
	require.Len(t, runtime.NativeCNYGroupIDs, 1)
}

func TestGetPublicPricingRuntimeAlwaysExcludesImageLaunchEntries(t *testing.T) {
	service := NewSettingService(&publicPricingSettingsRepoStub{values: map[string]string{
		SettingKeyPublicPricingGroupIDs: `[4,34,63]`,
		SettingKeyPublicPricingModelIDs: `["gpt-5.4","gpt-image-2"]`,
	}}, nil)

	runtime, err := service.GetPublicPricingRuntime(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int64{4}, runtime.GroupIDs)
	require.Equal(t, []string{"gpt-5.4"}, runtime.ModelIDs)
}

func TestGetPublicPricingRuntimeRejectsInvalidValues(t *testing.T) {
	tests := []map[string]string{
		{SettingKeyPublicPricingEnabled: "maybe"},
		{SettingKeyPublicPricingGroupIDs: "4,nope"},
		{SettingKeyPublicPricingQuotaUSDPerCNY: "0"},
		{SettingKeyPublicPricingUSDCNYReference: "-1"},
		{SettingKeyPublicPricingFXEffectiveDate: "2026-02-30"},
	}

	for _, values := range tests {
		service := NewSettingService(&publicPricingSettingsRepoStub{values: values}, nil)
		_, err := service.GetPublicPricingRuntime(context.Background())
		require.Error(t, err)
	}
}

func TestGetPublicPricingRuntimePropagatesRepositoryError(t *testing.T) {
	want := errors.New("database unavailable")
	service := NewSettingService(&publicPricingSettingsRepoStub{err: want}, nil)

	_, err := service.GetPublicPricingRuntime(context.Background())
	require.ErrorIs(t, err, want)
}
