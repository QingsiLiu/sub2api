package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeModelPlazaConfigDefaultsToAll(t *testing.T) {
	cfg, err := NormalizeModelPlazaConfig(GroupModelPlazaConfig{})
	require.NoError(t, err)
	require.Equal(t, GroupModelPlazaModeAll, cfg.Mode)
	require.Empty(t, cfg.Models)
}

func TestNormalizeModelPlazaConfigSelectedDeduplicates(t *testing.T) {
	cfg, err := NormalizeModelPlazaConfig(GroupModelPlazaConfig{
		Mode:   GroupModelPlazaModeSelected,
		Models: []string{" gpt-5 ", "gpt-5", "GPT-5", "claude"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5", "claude"}, cfg.Models)
}

func TestNormalizeModelPlazaConfigSelectedRequiresModels(t *testing.T) {
	_, err := NormalizeModelPlazaConfig(GroupModelPlazaConfig{Mode: GroupModelPlazaModeSelected})
	require.Error(t, err)
}

func TestFilterModelPlazaModelsPreservesDiscoveryOrder(t *testing.T) {
	cfg := GroupModelPlazaConfig{Mode: GroupModelPlazaModeSelected, Models: []string{"b", "a"}}
	require.Equal(t, []string{"a", "b"}, FilterModelPlazaModels(cfg, []string{"a", "c", "b"}))
}
