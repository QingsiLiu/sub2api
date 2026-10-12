package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSubmissionFormCredentialsGeiliKeepAuthenticationTypes(t *testing.T) {
	for _, a := range []*Account{
		{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "synthetic-access", "refresh_token": "synthetic-refresh"}},
		{Platform: PlatformAnthropic, Type: AccountTypeSetupToken, Credentials: map[string]any{"access_token": "synthetic-setup"}},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-key", "model_mapping": map[string]any{"a": "b"}}},
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"agent_private_key": "synthetic-agent-key"}},
		{Platform: PlatformAnthropic, Type: "bedrock", Credentials: map[string]any{"aws_access_key_id": "synthetic-id", "aws_secret_access_key": "synthetic-secret", "aws_region": "us-east-1"}},
		{Platform: PlatformAnthropic, Type: "service_account", Credentials: map[string]any{"service_account_json": map[string]any{"token_uri": "https://oauth2.googleapis.com/token"}}},
	} {
		require.NoError(t, validateSubmissionFormCredentialsGeili(context.Background(), a), a.Type)
	}
	for _, a := range []*Account{
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{}},
		{Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Credentials: map[string]any{"access_token": "synthetic"}},
		{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic", "base_url": "http://127.0.0.1"}},
		{Platform: PlatformAnthropic, Type: "bedrock", Credentials: map[string]any{"api_key": "synthetic", "aws_region": "localhost/path"}},
		{Platform: PlatformAnthropic, Type: "service_account", Credentials: map[string]any{"service_account_json": map[string]any{"token_uri": "http://127.0.0.1"}}},
	} {
		require.ErrorIs(t, validateSubmissionFormCredentialsGeili(context.Background(), a), ErrSubmissionInvalid)
	}
}
func TestSubmissionCreateGeiliDoesNotAffectAdministratorContext(t *testing.T) {
	require.Nil(t, submissionAccountCreate(context.Background()))
	invoked := false
	ctx := WithSubmissionAccountCreate(context.Background(), func(context.Context, *Account, []int64) (*Account, error) {
		invoked = true
		return &Account{Status: "inactive", Schedulable: false}, nil
	})
	a, err := submissionAccountCreate(ctx)(ctx, &Account{}, nil)
	require.NoError(t, err)
	require.True(t, invoked)
	require.Equal(t, "inactive", a.Status)
	require.False(t, a.Schedulable)
}

func TestSubmissionCreateGeiliReusesOfficialValidationAndSkipsOrdinaryWrite(t *testing.T) {
	svc := &adminServiceImpl{}
	ctx := WithSubmissionAccountCreate(context.Background(), func(_ context.Context, a *Account, groups []int64) (*Account, error) {
		require.Empty(t, groups)
		require.Equal(t, AccountTypeOAuth, a.Type)
		require.Equal(t, "synthetic-access", a.Credentials["access_token"])
		a.ID = 99
		a.Status = "inactive"
		a.Schedulable = false
		return a, nil
	})
	a, err := svc.CreateAccount(ctx, &CreateAccountInput{Name: "Holder", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "synthetic-access"}, Concurrency: 5, SkipDefaultGroupBind: true})
	require.NoError(t, err)
	require.Equal(t, int64(99), a.ID)
	require.False(t, a.Schedulable)
	// Invalid normal form settings must fail before the invitation callback.
	negative := -1.0
	_, err = svc.CreateAccount(ctx, &CreateAccountInput{Name: "Holder", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "synthetic-access"}, RateMultiplier: &negative, SkipDefaultGroupBind: true})
	require.Error(t, err)
}
