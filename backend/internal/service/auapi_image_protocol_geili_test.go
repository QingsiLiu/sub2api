package service

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAUAPIExplicitOpenAIProtocolDoesNotCaptureImageEditsAsNativeTasks(t *testing.T) {
	for _, marker := range []string{"openai", " OpenAI "} {
		account := Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
			"base_url": "https://api.auapi.ai", "image_provider": marker,
			"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"},
		}}
		require.False(t, account.IsAUAPIImageAccount())
		svc := &AUAPIImageTaskService{accounts: &auapiSeedAccountRepo{account: account}}
		available, err := svc.Available(context.Background(), 34, "gpt-image-2")
		require.NoError(t, err)
		require.False(t, available, "compatible Images requests must remain on the regular sync/async forwarding path")
	}
}

func TestAUAPICompatibleProtocolForwardsReferenceBytesAndExactModel(t *testing.T) {
	for _, model := range []string{"gpt-image-2", "gpt-image-2.5-flare", "gpt-image-2.5-sunburst"} {
		t.Run(model, func(t *testing.T) {
			var input bytes.Buffer
			form := multipart.NewWriter(&input)
			for key, value := range map[string]string{"model": model, "prompt": "edit the cup", "size": "1024x1024", "quality": "low", "response_format": "b64_json"} {
				require.NoError(t, form.WriteField(key, value))
			}
			image, err := form.CreateFormFile("image", "reference.png")
			require.NoError(t, err)
			_, err = image.Write([]byte("original-reference-bytes"))
			require.NoError(t, err)
			require.NoError(t, form.Close())
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, openAIImagesEditsEndpoint, bytes.NewReader(input.Bytes()))
			c.Request.Header.Set("Content-Type", form.FormDataContentType())
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aW1hZ2U="}]}`))}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			parsed, err := svc.ParseOpenAIImagesRequest(c, input.Bytes())
			require.NoError(t, err)
			account := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
				"base_url": "https://api.auapi.ai", "image_provider": "openai", "api_key": "fixture-only",
				"model_mapping": map[string]any{model: model},
			}}
			result, err := svc.ForwardImages(c.Request.Context(), c, account, input.Bytes(), parsed, "")
			require.NoError(t, err)
			require.Equal(t, model, result.UpstreamModel)
			require.Equal(t, "https://api.auapi.ai/v1/images/edits", upstream.lastReq.URL.String())
			mediaType := upstream.lastReq.Header.Get("Content-Type")
			request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(upstream.lastBody))
			request.Header.Set("Content-Type", mediaType)
			require.NoError(t, request.ParseMultipartForm(1<<20))
			defer request.MultipartForm.RemoveAll()
			require.Equal(t, model, request.FormValue("model"))
			require.Equal(t, "low", request.FormValue("quality"))
			require.Equal(t, "b64_json", request.FormValue("response_format"))
			file, _, err := request.FormFile("image")
			require.NoError(t, err)
			defer file.Close()
			actual, err := io.ReadAll(file)
			require.NoError(t, err)
			require.Equal(t, "original-reference-bytes", string(actual))
		})
	}
}

func TestAUAPINativeProtocolAndLegacyHostnameDiscoveryRemainCompatible(t *testing.T) {
	for _, marker := range []string{"", "auapi", " AUAPI "} {
		account := Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
			"base_url": "https://api.auapi.ai", "image_provider": marker,
		}}
		require.True(t, account.IsAUAPIImageAccount())
	}
}
