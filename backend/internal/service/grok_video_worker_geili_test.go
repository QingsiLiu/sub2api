//go:build unit

package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
	"time"
)

func TestDurableVideoWorkerPollsBoundAccountAndPreservesReportedUsage(t *testing.T) {
	for _, tc := range []struct {
		name, body, unit, id string
		duration, tokens     int
	}{{"xai", `{"status":"done","video":{"url":"https://provider.invalid/file","duration":12}}`, "second", "task-1", 12, 0}, {"seedance", `{"status":"succeeded","usage":{"completion_tokens":12345}}`, "output_token", "seedance:task-2", 0, 12345}, {"seedance_zero", `{"status":"succeeded","usage":{"completion_tokens":0}}`, "output_token", "seedance:task-3", 0, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			account := grokMediaContentTestAccount()
			if tc.unit == "output_token" {
				account = seedanceTestAccount()
			}
			account.Status = StatusActive
			account.Schedulable = true
			upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(tc.body)}
			svc := &OpenAIGatewayService{accountRepo: &openAIRecordUsageAccountRepoStub{account: account}, httpUpstream: upstream}
			obs, err := svc.pollDurableGrokVideo(context.Background(), &GrokVideoTaskSnapshot{TaskID: tc.id, AccountID: account.ID, Unit: tc.unit})
			require.NoError(t, err)
			require.Equal(t, "done", obs.Status)
			require.Equal(t, tc.duration, obs.DurationSeconds)
			if tc.unit == "output_token" {
				require.NotNil(t, obs.OutputTokens)
				require.Equal(t, tc.tokens, *obs.OutputTokens)
			}
			require.WithinDuration(t, time.Now(), obs.CompletedAt, time.Second)
			require.NotEmpty(t, upstream.request.Header.Get("Authorization"))
			require.NotContains(t, upstream.request.URL.String(), "provider.invalid")
		})
	}
}
func TestDurableVideoWorkerRetainsUnknownUsageAndPausedProviders(t *testing.T) {
	account := seedanceTestAccount()
	account.Status = StatusActive
	account.Schedulable = true
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"status":"succeeded"}`)}
	svc := &OpenAIGatewayService{accountRepo: &openAIRecordUsageAccountRepoStub{account: account}, httpUpstream: upstream}
	_, err := svc.pollDurableGrokVideo(context.Background(), &GrokVideoTaskSnapshot{TaskID: "seedance:task", AccountID: account.ID, Unit: "output_token"})
	require.ErrorContains(t, err, "without reported")
	account.Schedulable = false
	upstream.request = nil
	_, err = svc.pollDurableGrokVideo(context.Background(), &GrokVideoTaskSnapshot{TaskID: "seedance:task", AccountID: account.ID, Unit: "output_token"})
	require.ErrorContains(t, err, "paused")
	require.Nil(t, upstream.request)
}

type videoTransportFailure struct{ HTTPUpstream }

func (videoTransportFailure) Do(*http.Request, string, int64, int) (*http.Response, error) {
	return nil, errors.New("connection lost after request body sent")
}
func TestDurableVideoAmbiguousCreateDoesNotBecomeFailover(t *testing.T) {
	account := grokMediaContentTestAccount()
	svc := &OpenAIGatewayService{httpUpstream: videoTransportFailure{}}
	c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/videos/generations", nil)
	_, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointVideosGenerations, "", []byte(`{"model":"grok-imagine-video","prompt":"fixture"}`), "application/json")
	require.ErrorContains(t, err, "automatic retry disabled")
	var retry *UpstreamFailoverError
	require.False(t, errors.As(err, &retry))
}

func TestDurableVideoAmbiguousHTTP5xxDoesNotBecomeFailover(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504} {
		response := grokMediaContentStatusResponse(`{"error":"proxy lost upstream response"}`)
		response.StatusCode = status
		svc := &OpenAIGatewayService{httpUpstream: &grokMediaContentUpstreamStub{response: response}}
		c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/videos/generations", nil)
		_, err := svc.ForwardGrokMedia(context.Background(), c, grokMediaContentTestAccount(), GrokMediaEndpointVideosGenerations, "", []byte(`{"model":"grok-imagine-video","prompt":"fixture"}`), "application/json")
		require.ErrorContains(t, err, "automatic retry disabled")
		var retry *UpstreamFailoverError
		require.False(t, errors.As(err, &retry))
	}
}
