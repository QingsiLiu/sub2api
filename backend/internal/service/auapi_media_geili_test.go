package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAUAPIVideoRejectsImplicitOrDroppedCapabilities(t *testing.T) {
	valid := `{"model":"MiniMax-H3","prompt":"a boat","resolution":"768p","duration":5,"generate_audio":true}`
	r, tier, err := buildAUAPIVideoPayload([]byte(valid))
	require.NoError(t, err)
	require.Equal(t, "768p", tier)
	require.Equal(t, 5, r.Parameters.DurationSeconds)
	require.Equal(t, "video", r.Kind)
	require.Empty(t, r.Parameters.Ratio, "omitted aspect ratio must not promise a supplier-specific geometry")
	for _, body := range []string{
		strings.Replace(valid, `,"generate_audio":true`, "", 1),
		strings.Replace(valid, `"duration":5`, `"duration":5,"seconds":5`, 1),
		strings.Replace(valid, `"duration":5`, `"duration":5.5`, 1),
		strings.Replace(valid, `"duration":5`, `"duration":-1`, 1),
		strings.Replace(valid, `"duration":5`, `"duration":5,"image_url":"https://example.com/reference.png"`, 1),
	} {
		_, _, err := buildAUAPIVideoPayload([]byte(body))
		require.Error(t, err, body)
	}
}

func TestAUAPISpecPriceRequiresExactAudioAndResolution(t *testing.T) {
	a := &Account{Credentials: map[string]any{"auapi_media_prices": `{"MiniMax-H3":{"768p:no_video_input:audio_true":0.04},"gpt-image-2.5":{"1k:low":0.006725}}`}}
	r, tier, err := buildAUAPIVideoPayload([]byte(`{"model":"MiniMax-H3","prompt":"a boat","resolution":"768p","duration":5,"generate_audio":true}`))
	require.NoError(t, err)
	price, err := auapiConfiguredMediaPrice(a, r, tier)
	require.NoError(t, err)
	require.InDelta(t, .2, price, 1e-9)
	no := false
	r.Parameters.GenerateAudio = &no
	_, err = auapiConfiguredMediaPrice(a, r, tier)
	require.Error(t, err)
	r.Parameters.GenerateAudio = nil
	_, err = auapiConfiguredMediaPrice(a, r, tier)
	require.Error(t, err)
	_, err = auapiConfiguredMediaPrice(a, r, "2k")
	require.Error(t, err)
}

func TestAUAPIVideoBillingHasVideoDetailAndRetainsDiscountOnce(t *testing.T) {
	repo, billing := &auapiSettlementRepo{}, &auapiSettlementBilling{}
	s := &AUAPIImageTaskService{repo: repo, billing: billing}
	task := &AUAPIImageTaskRecord{TaskID: "auvidtask_test", Kind: "video", Model: "MiniMax-H3", UpstreamModel: "MiniMax-H3", Phase: "settle", Count: 1, SuccessCount: 1, UnitPrice: .2, RateMultiplier: .9, ActualAmount: .18, HoldAmount: .18, DurationSeconds: 5, ImageSize: "768p", CreatedAt: time.Now()}
	require.NoError(t, s.finish(context.Background(), task))
	require.Len(t, billing.captured, 1)
	d := billing.captured[0].UsageDetail
	require.Equal(t, 0, d.ImageCount)
	require.Nil(t, d.ImageSize)
	require.Equal(t, 1, d.VideoCount)
	require.Equal(t, 5, *d.VideoDurationSeconds)
	require.Equal(t, "video", *d.BillingMode)
	require.Equal(t, "/v1/videos/tasks", *d.UpstreamEndpoint)
	require.Equal(t, .18, d.ActualCost)
	require.Equal(t, "video.generation.task", auapiImageTaskPublic(task).Object)
}

func TestAUAPIClientUsesNativeVideoEndpoint(t *testing.T) {
	u := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"task_123","status":"queued"}`))}}
	c := &auapiImageClient{account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.auapi.ai", "api_key": "fixture"}}, gateway: &OpenAIGatewayService{httpUpstream: u}}
	id, err := c.Submit(context.Background(), []byte(`{"kind":"video","model":"MiniMax-H3"}`), "stable-key")
	require.NoError(t, err)
	require.Equal(t, "task_123", id)
	require.Equal(t, "/v1/videos/tasks", u.lastReq.URL.Path)
	require.Equal(t, "stable-key", u.lastReq.Header.Get("Idempotency-Key"))
}

func TestAUAPIReconciliationRetainsHoldWithoutUpstreamOrSettlement(t *testing.T) {
	repo, billing := &auapiSettlementRepo{}, &auapiSettlementBilling{}
	s := &AUAPIImageTaskService{repo: repo, billing: billing, cfg: &config.Config{}}
	task := &AUAPIImageTaskRecord{TaskID: "auvidtask_unknown", Kind: "video", Phase: "reconcile", HoldAmount: .2, CreatedAt: time.Now().Add(-48 * time.Hour), RequestJSON: json.RawMessage(`{}`)}
	require.NoError(t, s.step(context.Background(), task))
	require.Equal(t, "reconcile", task.Phase)
	require.Equal(t, .2, task.HoldAmount)
	require.Empty(t, billing.applied)
	require.Empty(t, billing.released)
	require.True(t, task.NextPollAt.After(time.Now()))
}

type auapiMediaRecoveryRepo struct {
	AUAPIImageTaskRepository
	record  *AUAPIImageTaskRecord
	claimed string
}

func (r *auapiMediaRecoveryRepo) Get(context.Context, string) (*AUAPIImageTaskRecord, error) {
	return r.record, nil
}
func (r *auapiMediaRecoveryRepo) Claim(_ context.Context, id string, _ time.Duration) (*AUAPIImageTaskRecord, error) {
	r.claimed = id
	return r.record, nil
}
func (r *auapiMediaRecoveryRepo) Save(context.Context, *AUAPIImageTaskRecord) error { return nil }
func (r *auapiMediaRecoveryRepo) ReleaseLease(context.Context, *AUAPIImageTaskRecord) error {
	return nil
}

func TestAUAPIVideoDurableWorkerClaimsVideoAfterRestart(t *testing.T) {
	repo := &auapiMediaRecoveryRepo{record: &AUAPIImageTaskRecord{TaskID: "auvidtask_pending", Kind: "video", Phase: "reconcile", HoldAmount: .2}}
	s := &AUAPIImageTaskService{repo: repo, cfg: &config.Config{}}
	result, err := s.Process(context.Background(), auapiQueueID(repo.record.TaskID))
	require.NoError(t, err)
	require.Equal(t, repo.record.TaskID, repo.claimed)
	require.False(t, result.Terminal)
	require.Equal(t, .2, repo.record.HoldAmount)
}
func TestAUAPIPendingTaskRemainsOwnedAndQueryableAfterRetentionWindow(t *testing.T) {
	repo := &auapiMediaRecoveryRepo{record: &AUAPIImageTaskRecord{TaskID: "auvidtask_pending", UserID: 1, APIKeyID: 2, Phase: "poll", ExpiresAt: time.Now().Add(-time.Hour)}}
	s := &AUAPIImageTaskService{repo: repo}
	_, err := s.Get(context.Background(), ImageTaskOwner{UserID: 1, APIKeyID: 2}, repo.record.TaskID)
	require.NoError(t, err)
	_, err = s.Get(context.Background(), ImageTaskOwner{UserID: 1, APIKeyID: 3}, repo.record.TaskID)
	require.ErrorIs(t, err, ErrImageTaskNotFound)
	repo.record.Phase = "done"
	_, err = s.Get(context.Background(), ImageTaskOwner{UserID: 1, APIKeyID: 2}, repo.record.TaskID)
	require.ErrorIs(t, err, ErrImageTaskNotFound)
}

func TestAUAPIMediaIndependentSeedVersionAndKindIsolation(t *testing.T) {
	const seed = "dreamina-seedance-2-5-260628"
	require.True(t, supportedAUAPIMediaModel("video", seed))
	for _, kind := range []string{"image", "audio", "text", ""} {
		require.False(t, supportedAUAPIMediaModel(kind, seed), kind)
	}
	for _, model := range []string{"wan3.0-video", "seedance-2.5", "MiniMax-H3", "kling-3.0"} {
		require.True(t, supportedAUAPIMediaModel("video", model), model)
		require.False(t, supportedAUAPIMediaModel("image", model), model)
	}
	for _, model := range []string{"gpt-image-2", "gpt-image-2.5", "gemini-3-pro-image-preview"} {
		require.True(t, supportedAUAPIMediaModel("image", model), model)
		require.False(t, supportedAUAPIMediaModel("video", model), model)
	}
	for _, model := range []string{"dreamina-seedance-2-5-260627", "dreamina-seedance-2-5-260628-preview", "DREAMINA-SEEDANCE-2-5-260628", "kling-video-o1", "kling-3.0-turbo", "unknown-video"} {
		require.False(t, supportedAUAPIMediaModel("video", model), model)
	}
}

type auapiSeedAcceptanceRepo struct {
	AUAPIImageTaskRepository
	created []*AUAPIImageTaskRecord
}

func (r *auapiSeedAcceptanceRepo) GetByIdempotency(context.Context, int64, int64, string) (*AUAPIImageTaskRecord, error) {
	return nil, ErrImageTaskNotFound
}
func (r *auapiSeedAcceptanceRepo) Create(_ context.Context, task *AUAPIImageTaskRecord) (*AUAPIImageTaskRecord, bool, error) {
	r.created = append(r.created, task)
	return task, true, nil
}

type auapiSeedAccountRepo struct {
	AccountRepository
	account Account
}

func (r *auapiSeedAccountRepo) ListByGroup(context.Context, int64) ([]Account, error) {
	return []Account{r.account}, nil
}

func auapiSeedAcceptanceFixture(upstreamModel, prices string) (*AUAPIImageTaskService, *APIKey, *auapiSeedAcceptanceRepo, *httpUpstreamRecorder) {
	const seed = "dreamina-seedance-2-5-260628"
	repo := &auapiSeedAcceptanceRepo{}
	accounts := &auapiSeedAccountRepo{account: Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{
		"image_provider": "auapi", "api_key": "fixture-only", "base_url": "https://api.auapi.ai",
		"model_mapping": map[string]any{seed: upstreamModel}, "auapi_media_prices": prices,
	}}}
	cfg := &config.Config{RunMode: config.RunModeSimple, AUAPIImage: config.AUAPIImageConfig{Enabled: true}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"seed_task","status":"queued"}`))}}
	group := &Group{ID: 3, RateMultiplier: 1, AllowImageGeneration: true}
	key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, GroupID: &group.ID, Group: group}
	svc := &AUAPIImageTaskService{repo: repo, accounts: accounts, cfg: cfg, storage: func() (*ImageResultUploader, bool) { return nil, true },
		gateway: &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}, billingCache: &BillingCacheService{cfg: cfg}}
	return svc, key, repo, upstream
}

func TestAUAPIMediaSeedAcceptanceKeepsExactVersionAndPayload(t *testing.T) {
	const seed = "dreamina-seedance-2-5-260628"
	svc, key, repo, upstream := auapiSeedAcceptanceFixture(seed, `{"dreamina-seedance-2-5-260628":{"720p:no_video_input:audio_false":0.3683}}`)
	body := []byte(`{"model":"dreamina-seedance-2-5-260628","prompt":"boat","resolution":"720p","duration":4,"generate_audio":false,"aspect_ratio":"16:9"}`)
	task, replayed, err := svc.SubmitVideo(context.Background(), key, nil, body, "seed-exact")
	require.NoError(t, err)
	require.False(t, replayed)
	require.True(t, strings.HasPrefix(task.ID, "auvidtask_"))
	require.Len(t, repo.created, 1)
	stored := repo.created[0]
	require.Equal(t, seed, stored.Model)
	require.Equal(t, seed, stored.UpstreamModel)
	require.InDelta(t, 1.4732, stored.UnitPrice, 1e-9)
	require.Empty(t, upstream.requests, "acceptance persists before asynchronous provider submission")
	accounts, ok := svc.accounts.(*auapiSeedAccountRepo)
	require.True(t, ok)
	client := &auapiImageClient{account: &accounts.account, gateway: svc.gateway}
	_, err = client.Submit(context.Background(), stored.RequestJSON, stored.UpstreamIdempotencyKey)
	require.NoError(t, err)
	var submitted auapiImageRequest
	require.NoError(t, json.Unmarshal(upstream.lastBody, &submitted))
	require.Equal(t, seed, submitted.Model)
	require.Equal(t, "video", submitted.Kind)
	require.Equal(t, "720p", submitted.Parameters.Resolution)
	require.Equal(t, 4, submitted.Parameters.DurationSeconds)
	require.Equal(t, "16:9", submitted.Parameters.Ratio)
	require.NotNil(t, submitted.Parameters.GenerateAudio)
	require.False(t, *submitted.Parameters.GenerateAudio)
	require.Equal(t, "/v1/videos/tasks", upstream.lastReq.URL.Path)
}

func TestAUAPIMediaSeedRejectsUnsupportedMappingsOrUnpricedSpecsBeforeHold(t *testing.T) {
	const seed = "dreamina-seedance-2-5-260628"
	for _, tc := range []struct{ name, mapping, prices, resolution, audio string }{
		{"different version", "dreamina-seedance-2-5-260627", `{}`, "720p", "false"},
		{"unverified O1", "kling-video-o1", `{}`, "720p", "false"},
		{"unverified Turbo", "kling-3.0-turbo", `{}`, "720p", "false"},
		{"old version tariff", seed, `{"seedance-2.5":{"720p:no_video_input:audio_false":0.2842}}`, "720p", "false"},
		{"unpriced audio", seed, `{"dreamina-seedance-2-5-260628":{"720p:no_video_input:audio_false":0.3683}}`, "720p", "true"},
		{"unpriced resolution", seed, `{"dreamina-seedance-2-5-260628":{"720p:no_video_input:audio_false":0.3683}}`, "1080p", "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, key, repo, upstream := auapiSeedAcceptanceFixture(tc.mapping, tc.prices)
			body := []byte(`{"model":"` + seed + `","prompt":"boat","resolution":"` + tc.resolution + `","duration":4,"generate_audio":` + tc.audio + `,"aspect_ratio":"16:9"}`)
			_, _, err := svc.SubmitVideo(context.Background(), key, nil, body, "seed-reject")
			require.Error(t, err)
			require.Empty(t, repo.created, "repository INSERT and its balance hold must not run")
			require.Empty(t, upstream.requests, "rejected requests must never submit upstream")
		})
	}
}
