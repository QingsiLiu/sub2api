package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func gptEditFixture(t *testing.T, model, size string, images int) ([]byte, *OpenAIImagesRequest, [][]byte) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, field := range [][2]string{{"model", model}, {"prompt", "make the cup red"}, {"size", size}, {"quality", "high"}, {"n", "1"}, {"response_format", "b64_json"}} {
		require.NoError(t, w.WriteField(field[0], field[1]))
	}
	refs := [][]byte{}
	for i := 0; i < images; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 2+i, 3))
		img.Set(0, 0, color.RGBA{R: uint8(i + 1), A: 255})
		var b bytes.Buffer
		require.NoError(t, png.Encode(&b, img))
		refs = append(refs, b.Bytes())
		field := "image"
		if i > 0 {
			field = fmt.Sprintf("image[%d]", i)
		}
		f, err := w.CreateFormFile(field, "reference.png")
		require.NoError(t, err)
		_, err = f.Write(b.Bytes())
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, openAIImagesEditsEndpoint, bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	p, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body.Bytes())
	require.NoError(t, err)
	return body.Bytes(), p, refs
}

func gptJSONResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

type gptDurableRepo struct {
	auapiSeedAcceptanceRepo
	saved   []string
	saveErr error
}

func (r *gptDurableRepo) GetByIdempotency(_ context.Context, _ int64, _ int64, idem string) (*AUAPIImageTaskRecord, error) {
	for _, task := range r.created {
		if task.IdempotencyKey == idem {
			return task, nil
		}
	}
	return nil, ErrImageTaskNotFound
}
func (r *gptDurableRepo) Save(_ context.Context, task *AUAPIImageTaskRecord) error {
	r.saved = append(r.saved, task.Phase)
	return r.saveErr
}
func (r *auapiSeedAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return &r.account, nil
}

func gptDurableFixture(model string) (*AUAPIImageTaskService, *APIKey, *gptDurableRepo, *httpUpstreamRecorder) {
	s, key, _, up := auapiSeedAcceptanceFixture(model, "")
	s.cfg.AUAPIImage.MaxPollSeconds = 600
	s.cfg.AUAPIImage.RequestTimeoutSeconds = 30
	s.cfg.AUAPIImage.PollIntervalSeconds = 1
	a := &s.accounts.(*auapiSeedAccountRepo).account
	a.Credentials["image_provider"] = "auapi_gpt_async"
	a.Credentials["model_mapping"] = map[string]any{model: model}
	prices := []float64{.03, .08, .1}
	key.Group.ImagePrice1K = &prices[0]
	key.Group.ImagePrice2K = &prices[1]
	key.Group.ImagePrice4K = &prices[2]
	repo := &gptDurableRepo{}
	s.repo = repo
	return s, key, repo, up
}

func TestAUAPIGPTDurableEditUploadsThenPersistsReferenceURLs(t *testing.T) {
	for _, model := range []string{"gpt-image-2", "gpt-image-2.5-flare", "gpt-image-2.5-sunburst"} {
		for _, size := range []string{"1024x1024", "2048x2048", "2880x2880"} {
			t.Run(model+size, func(t *testing.T) {
				s, key, repo, up := gptDurableFixture(model)
				body, p, refs := gptEditFixture(t, model, size, 2)
				up.responses = []*http.Response{}
				for i, ref := range refs {
					up.responses = append(up.responses, gptJSONResponse(200, fmt.Sprintf(`{"url":"https://references.example.com/%d.png","mime_type":"image/png","size":%d,"width":%d,"height":3}`, i, len(ref), i+2)))
				}
				task, replayed, err := s.SubmitImageRequest(context.Background(), key, nil, body, p, "edit")
				require.NoError(t, err)
				require.False(t, replayed)
				require.NotNil(t, task)
				require.Len(t, repo.created, 1)
				stored := repo.created[0]
				var request auapiImageRequest
				require.NoError(t, json.Unmarshal(stored.RequestJSON, &request))
				require.Equal(t, "edit", request.Action)
				require.Equal(t, model, request.Model)
				require.Equal(t, size, request.Parameters.Size)
				require.Equal(t, "high", request.Parameters.Quality)
				require.Equal(t, []string{"https://references.example.com/0.png", "https://references.example.com/1.png"}, request.Parameters.ImageURLs)
				require.Len(t, up.requests, 2)
				for i, r := range up.requests {
					require.Equal(t, "/v1/media/references", r.URL.Path)
					require.Equal(t, "image/png", r.Header.Get("Content-Type"))
					require.Equal(t, refs[i], up.bodies[i])
				}
				require.NotContains(t, string(stored.RequestJSON), "b64_json")
				require.Equal(t, "estimate", stored.Phase)
				_, replayed, err = s.SubmitImageRequest(context.Background(), key, nil, body, p, "edit")
				require.NoError(t, err)
				require.True(t, replayed)
				require.Len(t, up.requests, 2)
				changed := *p
				changed.Prompt = "different edit"
				_, _, err = s.SubmitImageRequest(context.Background(), key, nil, body, &changed, "edit")
				require.ErrorIs(t, err, ErrAUAPIImageConflict)
				require.Len(t, up.requests, 2)
				up.resp = gptJSONResponse(200, `{"id":"accepted_gpt_task","status":"queued"}`)
				stored.Phase = "submit"
				require.NoError(t, s.step(context.Background(), stored))
				require.Equal(t, []string{"reconcile", "poll"}, repo.saved)
				require.Equal(t, "accepted_gpt_task", stored.UpstreamTaskID)
				require.Equal(t, "/v1/async/tasks", up.lastReq.URL.Path)
				require.Equal(t, stored.UpstreamIdempotencyKey, up.lastReq.Header.Get("Idempotency-Key"))
				var sent auapiImageRequest
				require.NoError(t, json.Unmarshal(up.lastBody, &sent))
				require.Equal(t, request, sent)
			})
		}
	}
}

func TestAUAPIGPTDurableRejectsBadUploadsBeforeTaskOrHold(t *testing.T) {
	for _, response := range []*http.Response{gptJSONResponse(503, `{}`), gptJSONResponse(200, `{"url":"http://unsafe.example/image.png","mime_type":"image/png","size":1,"width":2,"height":3}`), gptJSONResponse(200, `{"url":"https://references.example/image.png","mime_type":"image/png","size":1,"width":2,"height":3}`)} {
		s, key, repo, up := gptDurableFixture("gpt-image-2")
		body, p, _ := gptEditFixture(t, "gpt-image-2", "1024x1024", 1)
		up.resp = response
		_, _, err := s.SubmitImageRequest(context.Background(), key, nil, body, p, "reject")
		require.Error(t, err)
		require.Empty(t, repo.created)
		require.Len(t, up.requests, 1)
		require.Equal(t, "/v1/media/references", up.lastReq.URL.Path)
	}
	s, key, repo, up := gptDurableFixture("gpt-image-2")
	body, p, _ := gptEditFixture(t, "gpt-image-2", "1024x1024", 1)
	p.Uploads[0].Data = []byte("not an image")
	_, _, err := s.SubmitImageRequest(context.Background(), key, nil, body, p, "invalid")
	require.Error(t, err)
	require.Empty(t, repo.created)
	require.Empty(t, up.requests)
}

func TestAUAPIGPTDurableUncertainSubmissionAndRestartNeverRepeatPOST(t *testing.T) {
	for _, status := range []int{0, 502, 524} {
		s, key, repo, up := gptDurableFixture("gpt-image-2.5-flare")
		p := &OpenAIImagesRequest{Model: "gpt-image-2.5-flare", Prompt: "cup", N: 1, Size: "2880x2880", Quality: "max", Endpoint: openAIImagesGenerationsEndpoint}
		_, _, err := s.SubmitImageRequest(context.Background(), key, nil, []byte(`{"model":"gpt-image-2.5-flare","prompt":"cup","n":1,"size":"2880x2880","quality":"max"}`), p, "uncertain")
		require.NoError(t, err)
		task := repo.created[0]
		task.Phase = "submit"
		if status == 0 {
			up.err = errors.New("connection lost")
		} else {
			up.resp = gptJSONResponse(status, `{}`)
		}
		require.Error(t, s.step(context.Background(), task))
		require.Equal(t, "reconcile", task.Phase)
		require.Len(t, up.requests, 1)
		require.NoError(t, s.step(context.Background(), task))
		require.Len(t, up.requests, 1, "restart must not dispatch another POST")
		require.Equal(t, "/v1/async/tasks", up.lastReq.URL.Path)
	}
	s, key, repo, up := gptDurableFixture("gpt-image-2")
	p := &OpenAIImagesRequest{Model: "gpt-image-2", Prompt: "cup", N: 1, Size: "1024x1024", Endpoint: openAIImagesGenerationsEndpoint}
	_, _, err := s.SubmitImageRequest(context.Background(), key, nil, []byte(`{"model":"gpt-image-2","prompt":"cup"}`), p, "persist-first")
	require.NoError(t, err)
	repo.created[0].Phase = "submit"
	repo.saveErr = errors.New("database unavailable")
	require.Error(t, s.step(context.Background(), repo.created[0]))
	require.Empty(t, up.requests, "a failed durable barrier must prevent POST")
}

func TestAUAPIGPTDurableUsesGroupPriceAndAppliesMultiplierOnce(t *testing.T) {
	for _, multiplier := range []float64{0, .5, 2} {
		s, key, repo, up := gptDurableFixture("gpt-image-2.5-sunburst")
		key.Group.RateMultiplier = multiplier
		p := &OpenAIImagesRequest{Model: "gpt-image-2.5-sunburst", Prompt: "cup", N: 1, Size: "2048x2048", Endpoint: openAIImagesGenerationsEndpoint}
		_, _, err := s.SubmitImageRequest(context.Background(), key, nil, []byte(`{"model":"gpt-image-2.5-sunburst","prompt":"cup","size":"2048x2048"}`), p, "price")
		require.NoError(t, err)
		task := repo.created[0]
		require.InDelta(t, .08, task.UnitPrice, 1e-9)
		require.InDelta(t, .08*multiplier, task.HoldAmount, 1e-9)
		up.resp = gptJSONResponse(200, `{"amount_usd":"0.04"}`)
		require.NoError(t, s.step(context.Background(), task))
		require.Equal(t, "submit", task.Phase, "upstream retail quote must not replace customer group price")
	}
}

func TestAUAPIGPTDurableAllSitePresetsAndExplicitOptions(t *testing.T) {
	for size, tier := range auapiGPTImageSizes {
		p := &OpenAIImagesRequest{Model: "gpt-image-2.5-flare", Prompt: "cup", N: 1, Size: size, Endpoint: openAIImagesGenerationsEndpoint}
		prepared, err := prepareAUAPIGPTImages([]byte(`{"model":"gpt-image-2.5-flare","prompt":"cup"}`), p)
		require.NoError(t, err)
		require.Equal(t, tier, prepared.tier)
		require.Equal(t, strings.ToLower(tier), prepared.request.Parameters.Resolution)
	}
	body, p, _ := gptEditFixture(t, "gpt-image-2.5-flare", "1024x1024", 1)
	zero := 0
	p.OutputFormat = "jpeg"
	p.OutputCompression = &zero
	prepared, err := prepareAUAPIGPTImages(body, p)
	require.NoError(t, err)
	payload, err := json.Marshal(prepared.request)
	require.NoError(t, err)
	require.Contains(t, string(payload), `"output_compression":0`)
	for _, change := range []func(*OpenAIImagesRequest){
		func(p *OpenAIImagesRequest) { p.HasMask = true }, func(p *OpenAIImagesRequest) { p.Stream = true },
		func(p *OpenAIImagesRequest) { p.Size = "auto" }, func(p *OpenAIImagesRequest) { p.Size = "3840x3840" },
		func(p *OpenAIImagesRequest) { p.N = 11 }, func(p *OpenAIImagesRequest) { p.PartialImages = &zero },
		func(p *OpenAIImagesRequest) { p.InputImageURLs = []string{"data:image/png;base64,aW1hZ2U="} },
	} {
		bad := *p
		change(&bad)
		_, err := prepareAUAPIGPTImages(body, &bad)
		require.Error(t, err)
	}
	_, err = prepareAUAPIGPTImages([]byte(`{"model":"gpt-image-2","prompt":"cup","unrecognized":1}`), &OpenAIImagesRequest{Model: "gpt-image-2", Prompt: "cup", N: 1, Size: "1024x1024"})
	require.Error(t, err)
}

func TestAUAPIGPTDurableSnapshotKeepsProtocolAfterCredentialRefresh(t *testing.T) {
	s, key, repo, up := gptDurableFixture("gpt-image-2.5-flare")
	p := &OpenAIImagesRequest{Model: "gpt-image-2.5-flare", Prompt: "cup", N: 1, Size: "1024x1024", Endpoint: openAIImagesGenerationsEndpoint}
	_, _, err := s.SubmitImageRequest(context.Background(), key, nil, []byte(`{"model":"gpt-image-2.5-flare","prompt":"cup"}`), p, "snapshot")
	require.NoError(t, err)
	raw, err := json.Marshal(repo.created[0])
	require.NoError(t, err)
	var task AUAPIImageTaskRecord
	require.NoError(t, json.Unmarshal(raw, &task))
	require.True(t, task.GPTAsync)
	s.accounts.(*auapiSeedAccountRepo).account.Credentials["image_provider"] = "auapi"
	task.Phase = "submit"
	up.resp = gptJSONResponse(200, `{"id":"snapshot_task","status":"queued"}`)
	require.NoError(t, s.step(context.Background(), &task))
	require.Equal(t, "/v1/async/tasks", up.lastReq.URL.Path)
	require.Equal(t, "reconcile", repo.saved[0])
}
