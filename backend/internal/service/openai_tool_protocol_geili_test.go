package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeiliToolProtocolRequirement(t *testing.T) {
	for _, tc := range []struct {
		name, model, body string
		want              bool
	}{
		{"astra tools", "gpt-6-astra", `{"tools":[{"type":"function","name":"lookup"}],"reasoning":{"effort":"high"}}`, true},
		{"astra none still needs responses", "gpt-6-astra", `{"tools":[{}],"reasoning_effort":"none"}`, true},
		{"astra default reasoning", "openai/gpt-6-astra-2026-09-01", `{"tools":[{}]}`, true},
		{"gpt6 alias", "gpt-6", `{"functions":[{"name":"lookup"}]}`, true},
		{"runtime tools", "gpt-6-astra", `{"input":[{"type":"additional_tools","tools":[{"type":"custom","name":"exec"}]}]}`, true},
		{"sol reasoning", "gpt-6-sol", `{"tools":[{}],"reasoning_effort":"high"}`, true},
		{"luna default", "gpt-6-luna", `{"tools":[{}]}`, true},
		{"sol explicit none", "gpt-6-sol", `{"tools":[{}],"reasoning_effort":"none"}`, false},
		{"luna nested none", "gpt-6-luna", `{"tools":[{}],"reasoning":{"effort":"none"}}`, false},
		{"astra text only", "gpt-6-astra", `{"input":"hello","reasoning":{"effort":"high"}}`, false},
		{"empty runtime tools", "gpt-6-astra", `{"tools":[],"input":[{"type":"additional_tools","tools":[]}]}`, false},
		{"ordinary chat", "gpt-5.6-sol", `{"tools":[{}],"reasoning_effort":"high"}`, false},
		{"anthropic schema", "gpt-6-astra", `{"tools":[{"name":"lookup","input_schema":{"type":"object"}}],"output_config":{"effort":"high"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openAIToolProtocolRequirementFromBody([]byte(tc.body)).requiresResponses(tc.model))
		})
	}
}

var geiliToolProtocolSchedulerModes = []struct {
	name                string
	advanced, loadBatch bool
}{
	{"legacy", false, false},
	{"legacy-load-batch", false, true},
	{"advanced", true, false},
}

func TestGeiliToolProtocolSchedulingSkipsChatOnlyEvenWhenSticky(t *testing.T) {
	for _, mode := range geiliToolProtocolSchedulerModes {
		for _, model := range []string{"gpt-6-astra", "public-alias"} {
			for _, sticky := range []string{"", "previous-session"} {
				name := mode.name + "/" + model + "/" + sticky
				t.Run(name, func(t *testing.T) {
					resetOpenAIAdvancedSchedulerSettingCacheForTest()
					accounts := geiliToolProtocolAccounts(model)
					svc := newOpenAICompactionSchedulerTestService(accounts, mode.advanced)
					svc.cfg.Gateway.Scheduling.LoadBatchEnabled = mode.loadBatch
					cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"previous-session": 1}}
					svc.cache = cache
					ctx := WithOpenAIToolProtocolRequirement(context.Background(), []byte(`{"tools":[{"type":"function","name":"lookup"}],"reasoning":{"effort":"high"}}`))
					groupID := int64(3131)
					selected, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", sticky, model, nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, false)
					require.NoError(t, err)
					require.Equal(t, int64(2), selected.Account.ID)
					if selected.ReleaseFunc != nil {
						selected.ReleaseFunc()
					}
				})
			}
		}
	}
}

func TestGeiliToolProtocolSchedulingFailsClosedWithOnlyChatAccounts(t *testing.T) {
	for _, mode := range geiliToolProtocolSchedulerModes {
		resetOpenAIAdvancedSchedulerSettingCacheForTest()
		svc := newOpenAICompactionSchedulerTestService(geiliToolProtocolAccounts("gpt-6-astra")[:1], mode.advanced)
		svc.cfg.Gateway.Scheduling.LoadBatchEnabled = mode.loadBatch
		ctx := WithOpenAIToolProtocolRequirement(context.Background(), []byte(`{"tools":[{}]}`))
		selected, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, nil, "", "", "gpt-6-astra", nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, false)
		require.ErrorIs(t, err, ErrNoAvailableAccounts)
		require.Nil(t, selected)
	}
}

func TestGeiliToolProtocolDoesNotBlockUnrelatedRequests(t *testing.T) {
	account := geiliToolProtocolAccounts("public-alias")[0]
	t.Run("plain text", func(t *testing.T) {
		ctx := WithOpenAIToolProtocolRequirement(context.Background(), []byte(`{"input":"hello"}`))
		require.True(t, openAIAccountSupportsToolProtocol(ctx, &account, "public-alias"))
	})
	t.Run("force responses overrides stale probe", func(t *testing.T) {
		account.Extra["openai_responses_mode"] = "force_responses"
		ctx := WithOpenAIToolProtocolRequirement(context.Background(), []byte(`{"tools":[{}]}`))
		require.True(t, openAIAccountSupportsToolProtocol(ctx, &account, "public-alias"))
	})
	t.Run("other provider", func(t *testing.T) {
		account.Platform = PlatformDeepseek
		ctx := WithOpenAIToolProtocolRequirement(context.Background(), []byte(`{"tools":[{}]}`))
		require.True(t, openAIAccountSupportsToolProtocol(ctx, &account, "public-alias"))
	})
}

func geiliToolProtocolAccounts(model string) []Account {
	accounts := make([]Account, 2)
	for i := range accounts {
		accounts[i] = Account{
			ID: int64(i + 1), Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: i,
			GroupIDs:    []int64{3131},
			Credentials: map[string]any{"api_key": "fixture-key", "base_url": "https://upstream.example", "model_mapping": map[string]any{model: "gpt-6-astra"}},
			Extra:       map[string]any{"openai_responses_supported": i == 1},
		}
	}
	return accounts
}

func TestGeiliToolProtocolMessagesMappingAndPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, requested, channelModel, defaultModel, mappedModel, effort string
		policy                                                           bool
		want                                                             bool
	}{
		{"channel alias to astra", "public", "gpt-6-astra", "", "", "high", false, false},
		{"channel astra to ordinary", "gpt-6-astra", "gpt-5.6-sol", "", "", "high", false, true},
		{"account alias to astra", "public", "public", "", "gpt-6-astra", "high", false, false},
		{"dispatch fallback to astra", "public", "public", "gpt-6-astra", "", "high", false, false},
		{"account overrides dispatch", "public", "public", "gpt-6-astra", "gpt-5.6-sol", "high", false, true},
		{"sol none is allowed", "gpt-6-sol", "gpt-6-sol", "", "", "none", false, true},
		{"sol none policy enables reasoning", "gpt-6-sol", "gpt-6-sol", "", "", "none", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := geiliToolProtocolAccounts(tc.requested)[0]
			delete(account.Credentials, "model_mapping")
			if tc.mappedModel != "" {
				account.Credentials["model_mapping"] = map[string]any{tc.channelModel: tc.mappedModel}
			}
			ctx := context.Background()
			if tc.policy {
				ctx = WithOpenAIReasoningEffortPolicy(ctx, "", []ReasoningEffortMapping{{From: "none", To: "low"}}, "")
			}
			body := []byte(`{"tools":[{"name":"lookup"}],"output_config":{"effort":"` + tc.effort + `"}}`)
			ctx = WithOpenAIMessagesToolProtocolRequirement(ctx, body, tc.channelModel, tc.defaultModel)
			require.Equal(t, tc.want, openAIAccountSupportsToolProtocol(ctx, &account, tc.requested))
		})
	}
}

func TestGeiliToolProtocolChatIgnoresForeignNestedEffort(t *testing.T) {
	account := geiliToolProtocolAccounts("gpt-6-sol")[0]
	delete(account.Credentials, "model_mapping")
	body := []byte(`{"model":"gpt-6-sol","tools":[{"type":"function"}],"reasoning":{"effort":"none"},"reasoning_effort":"high"}`)
	ctx := WithOpenAIChatToolProtocolRequirement(context.Background(), body)
	require.False(t, openAIAccountSupportsToolProtocol(ctx, &account, "gpt-6-sol"))
	err := guardOpenAIChatToolProtocol(nil, &account, body)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.False(t, failover.ShouldReportAccountScheduleFailure(), "no upstream request means no scheduler health failure")
	_, _, eligible := classifyOpenAIAPIKeyHealthFailure(err)
	require.False(t, eligible, "protocol mismatch must not trip account health breaker")
}

func TestGeiliToolProtocolChatAcceptsResponsesShapedBody(t *testing.T) {
	account := geiliToolProtocolAccounts("gpt-6-sol")[0]
	delete(account.Credentials, "model_mapping")
	ctx := WithOpenAIChatToolProtocolRequirement(context.Background(), []byte(`{"model":"gpt-6-sol","input":"hello","tools":[{"type":"function","name":"lookup"}],"reasoning":{"effort":"none"}}`))
	require.True(t, openAIAccountSupportsToolProtocol(ctx, &account, "gpt-6-sol"))
}
