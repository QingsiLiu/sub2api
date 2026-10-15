package service

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAUAPIGPT25KeepsIndependentModelTariffsAndSubmission(t *testing.T) {
	for _, model := range []string{"gpt-image-2.5-flare", "gpt-image-2.5-sunburst"} {
		t.Run(model, func(t *testing.T) {
			svc, key, repo, upstream := auapiSeedAcceptanceFixture(model, `{"`+model+`":{"1k:low":0.123,"1k:max":0.456}}`)
			a := &svc.accounts.(*auapiSeedAccountRepo).account
			a.Credentials["model_mapping"] = map[string]any{model: model}
			for _, quality := range []string{"", "max"} {
				body := []byte(`{"model":"` + model + `","prompt":"cup","resolution":"1k","quality":"` + quality + `"}`)
				_, _, err := svc.Submit(context.Background(), key, nil, body, "model-"+quality)
				require.NoError(t, err)
				stored := repo.created[len(repo.created)-1]
				require.Equal(t, model, stored.UpstreamModel)
				wantQuality, wantPrice := "low", .123
				if quality != "" {
					wantQuality, wantPrice = quality, .456
				}
				require.InDelta(t, wantPrice, stored.UnitPrice, 1e-9)
				client := &auapiImageClient{account: a, gateway: svc.gateway}
				upstream.resp.Body = io.NopCloser(strings.NewReader(`{"id":"image_task","status":"queued"}`))
				_, err = client.Submit(context.Background(), stored.RequestJSON, stored.UpstreamIdempotencyKey)
				require.NoError(t, err)
				var sent auapiImageRequest
				require.NoError(t, json.Unmarshal(upstream.lastBody, &sent))
				require.Equal(t, model, sent.Model)
				require.Equal(t, wantQuality, sent.Parameters.Quality)
				require.Equal(t, "/v1/images/tasks", upstream.lastReq.URL.Path)
			}
		})
	}
}

func TestAUAPIGPT25RejectsLegacyTariffAndUnpricedQualityBeforeAcceptance(t *testing.T) {
	const model = "gpt-image-2.5-flare"
	for _, prices := range []string{`{"gpt-image-2.5":{"1k:low":0.123}}`, `{"gpt-image-2.5-sunburst":{"1k:low":0.123}}`, `{"gpt-image-2.5-flare":{"1k:high":0.123}}`} {
		svc, key, repo, upstream := auapiSeedAcceptanceFixture(model, prices)
		svc.accounts.(*auapiSeedAccountRepo).account.Credentials["model_mapping"] = map[string]any{model: model}
		_, _, err := svc.Submit(context.Background(), key, nil, []byte(`{"model":"`+model+`","prompt":"cup","resolution":"1k"}`), "unpriced")
		require.Error(t, err)
		require.Empty(t, repo.created)
		require.Empty(t, upstream.requests)
	}
}

func TestAUAPIGPTQualityRequiresExact25Model(t *testing.T) {
	for _, model := range []string{"gpt-image-2", "gemini-3-pro-image-preview", "gpt-image-2.5-flare-preview"} {
		for _, quality := range []string{"xhigh", "max"} {
			_, _, err := buildAUAPIImagePayload([]byte(`{"model":"` + model + `","prompt":"cup","quality":"` + quality + `"}`))
			require.Error(t, err)
		}
	}
	for _, model := range []string{"gpt-image-2.5-flare-preview", "gpt-image-2.5-sunburst-preview", "GPT-IMAGE-2.5-FLARE"} {
		require.False(t, supportedAUAPIMediaModel("image", model))
	}
}
