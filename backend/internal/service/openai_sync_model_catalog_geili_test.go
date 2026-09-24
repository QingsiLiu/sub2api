package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultOpenAISyncModelIDs(t *testing.T) {
	require.Equal(t, []string{
		"codex-auto-review",
		"gpt-5.5",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-6-astra",
		"gpt-reserve",
		"gpt-5.6-luna",
		"gpt-6-sol",
		"gpt-6-luna",
	}, DefaultOpenAISyncModelIDs())
}

func TestNormalizeOpenAISyncModelIDs(t *testing.T) {
	got, err := NormalizeOpenAISyncModelIDs([]string{" gpt-6-astra ", "gpt-reserve"})
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-6-astra", "gpt-reserve"}, got)

	for _, input := range [][]string{{}, {""}, {"gpt-6-astra", "gpt-6-astra"}, {"not-a-model"}} {
		_, err := NormalizeOpenAISyncModelIDs(input)
		require.Error(t, err, "input=%v", input)
	}
}

func TestParseOpenAISyncModelIDsFallsBackToDefaults(t *testing.T) {
	require.Equal(t, DefaultOpenAISyncModelIDs(), ParseOpenAISyncModelIDs(""))
	require.Equal(t, DefaultOpenAISyncModelIDs(), ParseOpenAISyncModelIDs(`["unknown-model"]`))
}
