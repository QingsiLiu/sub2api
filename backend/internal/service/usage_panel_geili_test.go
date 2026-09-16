//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeUsagePanel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: ""},
		{in: " gpt ", want: UsagePanelGPT},
		{in: UsagePanelGrok, want: UsagePanelGrok},
		{in: UsagePanelClaude, want: UsagePanelClaude},
		{in: UsagePanelNational, want: UsagePanelNational},
		{in: UsagePanelGemini, want: UsagePanelGemini},
		{in: "openai", wantErr: true},
		{in: "anthropic", wantErr: true},
		{in: "GPT", wantErr: true},
	}
	for _, tc := range cases {
		got, err := NormalizeUsagePanel(tc.in)
		if tc.wantErr {
			require.Error(t, err, tc.in)
			continue
		}
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.want, got)
	}
	require.False(t, IsUsagePanel(""))
	require.False(t, IsUsagePanel("openai"))
	require.True(t, IsUsagePanel(UsagePanelNational))
}

func TestCreateGroupRejectsUnknownUsagePanel(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{groupRepo: repo}
	_, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name: "x", Platform: PlatformOpenAI, RateMultiplier: 1, UsagePanel: "openai",
	})
	require.Error(t, err)
	require.Nil(t, repo.created)
}

func TestCreateAndUpdateGroupPersistUsagePanel(t *testing.T) {
	repo := &groupRepoStubForAdmin{getByID: &Group{ID: 1, Name: "x", Platform: PlatformOpenAI, RateMultiplier: 1, Status: StatusActive, SubscriptionType: SubscriptionTypeStandard}}
	svc := &adminServiceImpl{groupRepo: repo}
	created, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name: "gpt line", Platform: PlatformOpenAI, RateMultiplier: 1, UsagePanel: UsagePanelGPT,
	})
	require.NoError(t, err)
	require.Equal(t, UsagePanelGPT, created.UsagePanel)

	national := UsagePanelNational
	updated, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{UsagePanel: &national})
	require.NoError(t, err)
	require.Equal(t, UsagePanelNational, updated.UsagePanel)
}
