package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

var (
	ErrAUAPIImageConflict = infraerrors.Conflict("IMAGE_IDEMPOTENCY_CONFLICT", "Idempotency-Key was used for a different image request")
	ErrAUAPIImageLease    = errors.New("image task lease lost")
)

type AUAPIImageTaskRecord struct {
	TaskID                                                                     string
	UserID, APIKeyID, GroupID, AccountID                                       int64
	Model, UpstreamModel                                                       string
	Status, Phase                                                              string
	RequestJSON                                                                json.RawMessage
	IdempotencyKey, UpstreamIdempotencyKey, RequestHash, UpstreamTaskID        string
	BaseURL                                                                    string // freezes the endpoint identity; credentials are always loaded separately
	ImageSize                                                                  string
	Count, SuccessCount                                                        int
	UnitPrice, RateMultiplier, AccountRateMultiplier, HoldAmount, ActualAmount float64
	SubscriptionID                                                             *int64
	AdmissionKey                                                               string
	ResultJSON, ErrorJSON                                                      json.RawMessage
	URLs                                                                       []string
	OutputIndexes                                                              []int
	HTTPStatus, RetryCount                                                     int
	Billed                                                                     bool
	NextPollAt, CreatedAt, ExpiresAt                                           time.Time
	CompletedAt                                                                *time.Time
	LeaseToken                                                                 string `json:"-"`
}

type AUAPIImageTaskRepository interface {
	Create(context.Context, *AUAPIImageTaskRecord) (*AUAPIImageTaskRecord, bool, error)
	Get(context.Context, string) (*AUAPIImageTaskRecord, error)
	GetByIdempotency(context.Context, int64, int64, string) (*AUAPIImageTaskRecord, error)
	Claim(context.Context, string, time.Duration) (*AUAPIImageTaskRecord, error)
	Save(context.Context, *AUAPIImageTaskRecord) error
	Settle(context.Context, *AUAPIImageTaskRecord) error
	ReleaseLease(context.Context, *AUAPIImageTaskRecord) error
	Due(context.Context, int) ([]string, error)
}

// A distinct Wire type keeps the AUAPI Redis namespace isolated from Gemini batches.
type AUAPIImageQueue struct{ BatchImageQueue }

type AUAPIImageTaskService struct {
	repo         AUAPIImageTaskRepository
	accounts     AccountRepository
	storage      ImageStorageResolver
	gateway      *OpenAIGatewayService
	logs         UsageLogRepository
	billing      UsageBillingRepository
	authCache    APIKeyAuthCacheInvalidator
	billingCache *BillingCacheService
	queue        AUAPIImageQueue
	cfg          *config.Config
	mu           sync.Mutex
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

func NewAUAPIImageTaskService(repo AUAPIImageTaskRepository, accounts AccountRepository, storage ImageStorageResolver, cfg *config.Config) *AUAPIImageTaskService {
	return &AUAPIImageTaskService{repo: repo, accounts: accounts, storage: storage, cfg: cfg}
}
func (s *AUAPIImageTaskService) Enabled() bool {
	return s != nil && s.repo != nil && s.cfg != nil && s.cfg.AUAPIImage.Enabled
}

// Route only when the group contains an explicitly marked account for this model.
// Operational failures fail closed rather than dropping back to a different provider.
func (s *AUAPIImageTaskService) Available(ctx context.Context, groupID int64, model string) (bool, error) {
	if s == nil || s.accounts == nil {
		return false, nil
	}
	list, err := s.accounts.ListByGroup(ctx, groupID)
	if err != nil {
		return false, ErrImageTaskUnavailable.WithCause(err)
	}
	for i := range list {
		if list[i].IsAUAPIImageAccount() && list[i].IsModelSupported(model) {
			return true, nil
		}
	}
	return false, nil
}
func (s *AUAPIImageTaskService) Submit(ctx context.Context, key *APIKey, sub *UserSubscription, body []byte, idem string) (*ImageTask, bool, error) {
	if !s.Enabled() {
		return nil, false, ErrImageTaskUnavailable
	}
	if key == nil || key.Group == nil || key.GroupID == nil {
		return nil, false, ErrGroupNotAllowed
	}
	if s.storage == nil {
		return nil, false, ErrImageTaskUnavailable
	}
	if _, ok := s.storage(); !ok {
		return nil, false, ErrImageTaskUnavailable
	}
	request, tier, err := buildAUAPIImagePayload(body)
	if err != nil {
		return nil, false, infraerrors.BadRequest("invalid_request_error", err.Error())
	}
	canonical, _ := json.Marshal(request)
	hash := HashUsageRequestPayload(canonical)
	idem = strings.TrimSpace(idem)
	if len(idem) > 191 {
		return nil, false, infraerrors.BadRequest("invalid_request_error", "Idempotency-Key exceeds 191 bytes")
	}
	if idem == "" {
		idem = uuid.NewString()
	}
	existing, err := s.repo.GetByIdempotency(ctx, key.UserID, key.ID, idem)
	if err == nil {
		if existing.RequestHash != hash {
			return nil, false, ErrAUAPIImageConflict
		}
		return auapiImageTaskPublic(existing), true, nil
	}
	if !errors.Is(err, ErrImageTaskNotFound) {
		return nil, false, ErrImageTaskUnavailable.WithCause(err)
	}
	if err = s.billingCache.CheckBillingEligibility(ctx, key.User, key, key.Group, sub, QuotaPlatform(ctx, key)); err != nil {
		return nil, false, err
	}
	routingModel := request.Model
	if mapped, ok := ResolvedUpstreamModelFromContext(ctx); ok {
		routingModel = mapped
	}
	mapping, _ := s.gateway.ResolveChannelMappingAndRestrict(ctx, key.GroupID, routingModel)
	if mapping.MappedModel != "" {
		routingModel = mapping.MappedModel
	}
	list, err := s.accounts.ListByGroup(ctx, *key.GroupID)
	if err != nil {
		return nil, false, ErrImageTaskUnavailable.WithCause(err)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Priority < list[j].Priority })
	var account *Account
	for i := range list {
		a := &list[i]
		if a.IsAUAPIImageAccount() && a.IsSchedulable() && a.IsModelSupported(routingModel) && a.GetCredential("api_key") != "" {
			account = a
			break
		}
	}
	if account == nil {
		return nil, false, infraerrors.ServiceUnavailable("NO_AUAPI_IMAGE_ACCOUNT", "No available AUAPI image account")
	}
	request.Model = account.GetMappedModel(routingModel)
	// AUAPI's public GPT image contract currently exposes gpt-image-2.
	if request.Model != "gpt-image-2" {
		return nil, false, infraerrors.BadRequest("invalid_request_error", "AUAPI image account must map to gpt-image-2")
	}
	payload, _ := json.Marshal(request)
	multiplier := s.gateway.ResolveUserGroupRateMultiplier(ctx, key.UserID, key.Group.ID, key.Group.BillingRateMultiplier(sub != nil))
	multiplier = resolveImageRateMultiplier(key, multiplier)
	resolved := s.gateway.resolveOpenAIChannelPricing(ctx, routingModel, key)
	if !apiKeyHasConfiguredImagePrice(key, tier) && (resolved == nil || (resolved.Mode != BillingModeImage && resolved.Mode != BillingModePerRequest)) {
		return nil, false, infraerrors.BadRequest("IMAGE_PRICE_REQUIRED", "Configure a per-image price for this model and size before enabling AUAPI")
	}
	cost := s.gateway.calculateOpenAIImageCost(ctx, routingModel, key, &OpenAIForwardResult{ImageCount: 1, ImageSize: tier}, multiplier)
	if cost == nil || cost.TotalCost < 0 || cost.ActualCost < 0 || math.IsNaN(cost.ActualCost) || math.IsInf(cost.ActualCost, 0) {
		return nil, false, ErrImageTaskUnavailable
	}
	now := time.Now().UTC()
	task := &AUAPIImageTaskRecord{TaskID: "auimgtask_" + strings.ReplaceAll(uuid.NewString(), "-", ""), UserID: key.UserID, APIKeyID: key.ID, GroupID: key.Group.ID, AccountID: account.ID, Model: routingModel, UpstreamModel: request.Model, Status: ImageTaskStatusProcessing, Phase: "estimate", RequestJSON: payload, IdempotencyKey: idem, RequestHash: hash, ImageSize: tier, Count: request.Parameters.N, UnitPrice: cost.TotalCost, RateMultiplier: multiplier, AccountRateMultiplier: account.BillingRateMultiplier(), HoldAmount: QuantizeUsageBillingAmount(cost.ActualCost * float64(request.Parameters.N)), CreatedAt: now, NextPollAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	task.UpstreamIdempotencyKey = "sub2api-" + task.TaskID
	task.BaseURL = strings.TrimSpace(account.GetCredential("base_url"))
	if task.BaseURL == "" {
		task.BaseURL = AUAPIImageDefaultBaseURL
	}
	if _, err = s.gateway.validateUpstreamBaseURL(task.BaseURL); err != nil {
		return nil, false, infraerrors.BadRequest("invalid_request_error", "Invalid AUAPI base URL")
	}
	if sub != nil {
		if sub.AdmissionKey == "" {
			return nil, false, ErrSubscriptionInvalid
		}
		task.SubscriptionID = &sub.ID
		task.AdmissionKey = sub.AdmissionKey
	}
	// geili hook: repository acceptance owns the unique idempotency claim and
	// balance reservation in one transaction. Never reserve before its INSERT.
	stored, created, err := s.repo.Create(ctx, task)
	if err != nil {
		return nil, false, err
	}
	if stored.RequestHash != hash {
		return nil, false, ErrAUAPIImageConflict
	}
	s.invalidate(ctx, stored)
	// PostgreSQL is the durable outbox. Queue failure cannot undo accepted work;
	// recovery periodically republishes due rows, including after Redis data loss.
	// The PostgreSQL due index is the durable queue; direct workers recover rows
	// after process restarts without relying on an in-memory enqueue.
	return auapiImageTaskPublic(stored), !created, nil
}
func (s *AUAPIImageTaskService) Get(ctx context.Context, owner ImageTaskOwner, id string) (*ImageTask, error) {
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.UserID != owner.UserID || t.APIKeyID != owner.APIKeyID || time.Now().After(t.ExpiresAt) {
		return nil, ErrImageTaskNotFound
	}
	return auapiImageTaskPublic(t), nil
}
func auapiQueueID(id string) string { return "imgbatch_" + id }
func (s *AUAPIImageTaskService) Process(ctx context.Context, queueID string) (BatchImageProcessResult, error) {
	id := strings.TrimPrefix(queueID, "imgbatch_")
	if !strings.HasPrefix(id, "auimgtask_") {
		return BatchImageProcessResult{Terminal: true}, nil
	}
	// The lease must cover one bounded provider step and durable settlement;
	// stale workers are fenced on every save even when configuration is small.
	lease := time.Duration(max(s.cfg.AUAPIImage.LeaseSeconds, s.cfg.AUAPIImage.RequestTimeoutSeconds+15)) * time.Second
	task, err := s.repo.Claim(ctx, id, lease)
	if err != nil {
		return BatchImageProcessResult{}, err
	}
	if task == nil {
		return BatchImageProcessResult{RequeueAfter: 3 * time.Second}, nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.repo.ReleaseLease(cleanup, task)
	}()
	if task.Phase == "done" {
		return BatchImageProcessResult{Terminal: true}, nil
	}
	work, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.AUAPIImage.RequestTimeoutSeconds)*time.Second)
	defer cancel()
	err = s.step(work, task)
	if err != nil {
		if errors.Is(err, ErrAUAPIImageLease) {
			return BatchImageProcessResult{}, err
		}
		task.RetryCount++
		delay := time.Duration(1<<min(task.RetryCount, 6)) * time.Second
		var httpErr *auapiHTTPError
		if errors.As(err, &httpErr) {
			if httpErr.RetryAfter > delay {
				delay = httpErr.RetryAfter
			}
		}
		// Settlement and its usage record must never be discarded after a charge.
		if task.Phase != "settle" && task.Phase != "fail" && (task.RetryCount >= 8 || (errors.As(err, &httpErr) && !httpErr.Retryable())) {
			task.Phase = "fail"
			task.ErrorJSON = imageTaskErrorJSON("api_error", err.Error())
			task.HTTPStatus = 502
		}
		task.NextPollAt = time.Now().Add(delay)
		if saveErr := s.repo.Save(ctx, task); saveErr != nil {
			return BatchImageProcessResult{}, saveErr
		}
		slog.Warn("auapi image task deferred", "task_id", task.TaskID, "phase", task.Phase, "retry", task.RetryCount)
	}
	return BatchImageProcessResult{Terminal: task.Phase == "done", RequeueAfter: func() time.Duration {
		d := time.Until(task.NextPollAt)
		if d < time.Second {
			return time.Second
		}
		return d
	}()}, nil
}
func (s *AUAPIImageTaskService) step(ctx context.Context, t *AUAPIImageTaskRecord) error {
	if t.Phase == "settle" || t.Phase == "fail" {
		return s.finish(ctx, t)
	}
	if time.Since(t.CreatedAt) > time.Duration(s.cfg.AUAPIImage.MaxPollSeconds)*time.Second {
		t.Phase = "fail"
		t.HTTPStatus = 504
		t.ErrorJSON = imageTaskErrorJSON("timeout_error", "AUAPI task deadline exceeded; no replacement task was submitted")
		return s.repo.Save(ctx, t)
	}
	account, err := s.accounts.GetByID(ctx, t.AccountID)
	if err != nil {
		return errors.New("AUAPI task account unavailable")
	}
	if !account.IsAUAPIImageAccount() || account.GetCredential("api_key") == "" {
		return errors.New("AUAPI task account credentials unavailable")
	}
	// Task ownership stays on the accepted account and endpoint even if routing changes.
	copy := *account
	copy.Credentials = make(map[string]any, len(account.Credentials))
	for k, v := range account.Credentials {
		copy.Credentials[k] = v
	}
	copy.Credentials["base_url"] = t.BaseURL
	client := &auapiImageClient{account: &copy, gateway: s.gateway}
	switch t.Phase {
	case "estimate":
		if err = client.Estimate(ctx, t.RequestJSON, t.UpstreamIdempotencyKey); err != nil {
			return err
		}
		t.Phase = "submit"
	case "submit":
		// Same server-generated idempotency key survives a timeout or crash before Save.
		id, e := client.Submit(ctx, t.RequestJSON, t.UpstreamIdempotencyKey)
		if e != nil {
			return e
		}
		t.UpstreamTaskID = id
		t.Phase = "poll"
	case "poll":
		status, e := client.Status(ctx, t.UpstreamTaskID)
		if e != nil {
			return e
		}
		switch status.Status {
		case "queued", "running":
			t.NextPollAt = time.Now().Add(time.Duration(s.cfg.AUAPIImage.PollIntervalSeconds) * time.Second)
		case "succeeded":
			if len(status.Outputs) == 0 || len(status.Outputs) > t.Count {
				return errors.New("invalid AUAPI output count")
			}
			seen := map[int]bool{}
			t.OutputIndexes = nil
			for _, out := range status.Outputs {
				if out.Index < 0 || out.Index >= t.Count || seen[out.Index] {
					return errors.New("invalid AUAPI output index")
				}
				seen[out.Index] = true
				t.OutputIndexes = append(t.OutputIndexes, out.Index)
			}
			t.Phase = "store"
		case "failed", "canceled", "timeout":
			t.Phase = "fail"
			t.HTTPStatus = 502
			t.ErrorJSON = imageTaskErrorJSON("api_error", "AUAPI task ended with status "+status.Status)
		default:
			return errors.New("unknown AUAPI task status")
		}
	case "store":
		if s.storage == nil {
			return errors.New("image storage unavailable")
		}
		uploader, enabled := s.storage()
		if !enabled || uploader == nil || uploader.storage == nil {
			return errors.New("image storage unavailable")
		}
		i := len(t.URLs)
		if i < len(t.OutputIndexes) {
			data, _, e := client.Content(ctx, t.UpstreamTaskID, t.OutputIndexes[i], uploader.maxDownloadBytes)
			if e != nil {
				return e
			}
			ct := detectedImageContentType(data)
			if ct == "" {
				return errors.New("AUAPI content is not an image")
			}
			link, e := uploader.storage.Save(ctx, uploader.buildKey(t.TaskID, i, ct), ct, data)
			if e != nil {
				return errors.New("failed to store AUAPI image")
			}
			t.URLs = append(t.URLs, link)
		}
		if len(t.URLs) == len(t.OutputIndexes) {
			items := make([]map[string]string, 0, len(t.URLs))
			for _, link := range t.URLs {
				items = append(items, map[string]string{"url": link})
			}
			t.ResultJSON, _ = json.Marshal(map[string]any{"created": time.Now().Unix(), "data": items})
			t.SuccessCount = len(t.URLs)
			t.ActualAmount = QuantizeUsageBillingAmount(t.UnitPrice * t.RateMultiplier * float64(t.SuccessCount))
			t.Phase = "settle"
			completed := time.Now().UTC().Truncate(time.Microsecond)
			t.CompletedAt = &completed
		}
	default:
		return fmt.Errorf("invalid AUAPI task phase")
	}
	t.RetryCount = 0
	return s.repo.Save(ctx, t)
}
func (s *AUAPIImageTaskService) finish(ctx context.Context, t *AUAPIImageTaskRecord) error {
	if t.Phase == "fail" {
		t.ActualAmount = 0
		t.SuccessCount = 0
		t.ResultJSON = nil
	}
	if t.CompletedAt == nil {
		completed := time.Now().UTC().Truncate(time.Microsecond)
		t.CompletedAt = &completed
		// Persist the completion timestamp before financial effects so replay
		// preserves the original financial/detail identity and time.
		if err := s.repo.Save(ctx, t); err != nil {
			return err
		}
	}
	detail := auapiImageUsageDetail(t)
	if t.Phase == "fail" {
		detail = nil // failed work is a terminal zero receipt, not a successful usage row
	}
	if s.billing == nil {
		return errors.New("AUAPI billing repository is not configured")
	}
	if t.SubscriptionID != nil {
		_, err := s.billing.Apply(ctx, &UsageBillingCommand{
			SubscriptionAdmissionKey: t.AdmissionKey, RequestID: "auapi_image_settle:" + t.TaskID,
			APIKeyID: t.APIKeyID, RequestFingerprint: t.RequestHash, RequestPayloadHash: t.RequestHash,
			UserID: t.UserID, AccountID: t.AccountID, SubscriptionID: t.SubscriptionID,
			AccountType: AccountTypeAPIKey, Model: t.Model, ImageCount: t.SuccessCount,
			MediaType: "image", SubscriptionCost: t.ActualAmount,
			UsageDetail: detail, CompletedAt: *t.CompletedAt, TerminalFailure: t.Phase == "fail",
		})
		if err != nil {
			return err
		}
	} else if t.HoldAmount > 0 {
		cmd := &BatchImageBalanceHoldCommand{
			HoldRequestID: "auapi_image_hold:" + t.TaskID, APIKeyID: t.APIKeyID,
			RequestFingerprint: t.RequestHash, RequestPayloadHash: t.RequestHash,
			UserID: t.UserID, BatchID: t.TaskID, HoldAmount: t.HoldAmount,
			CompletedAt: *t.CompletedAt,
		}
		if t.Phase == "settle" {
			cmd.RequestID = "auapi_image_capture:" + t.TaskID
			cmd.ActualAmount = t.ActualAmount
			cmd.UsageDetail = detail
			if _, err := s.billing.CaptureBatchImageBalance(ctx, cmd); err != nil {
				return err
			}
		} else {
			cmd.RequestID = "auapi_image_release:" + t.TaskID
			if _, err := s.billing.ReleaseBatchImageBalance(ctx, cmd); err != nil {
				return err
			}
		}
	} else if t.Phase == "settle" {
		// Explicit free balance calls still get financial/detail evidence.
		if _, err := s.billing.Apply(ctx, &UsageBillingCommand{
			RequestID: "auapi_image_settle:" + t.TaskID, APIKeyID: t.APIKeyID,
			RequestFingerprint: t.RequestHash, RequestPayloadHash: t.RequestHash,
			UserID: t.UserID, AccountID: t.AccountID, AccountType: AccountTypeAPIKey,
			Model: t.Model, ImageCount: t.SuccessCount, MediaType: "image",
			UsageDetail: detail, CompletedAt: *t.CompletedAt,
		}); err != nil {
			return err
		}
	}
	s.invalidate(ctx, t)
	if t.Phase == "settle" {
		// Production delivery is journal-owned. Legacy adapters retain their
		// immediate projection; they cannot turn delivery retries into a charge.
		if _, durable := s.billing.(UsageSettlementRepository); !durable && s.logs != nil {
			if _, err := s.logs.Create(ctx, detail); err != nil {
				return err
			}
		}
		t.Status = ImageTaskStatusCompleted
		t.HTTPStatus = http.StatusOK
	} else {
		t.Status = ImageTaskStatusFailed
	}
	t.ExpiresAt = t.CompletedAt.Add(24 * time.Hour)
	t.Phase = "done"
	return s.repo.Save(ctx, t)
}

// auapiImageUsageDetail is deliberately independent of RequestJSON: prompts,
// supplier keys and authorization headers never enter financial evidence.
func auapiImageUsageDetail(t *AUAPIImageTaskRecord) *UsageLog {
	mode, inbound, upstream := "image", "/v1/images/generations/async", "/v1/images/tasks"
	typ := BillingTypeBalance
	if t.SubscriptionID != nil {
		typ = BillingTypeSubscription
	}
	return &UsageLog{
		UserID: t.UserID, APIKeyID: t.APIKeyID, AccountID: t.AccountID,
		GroupID: &t.GroupID, SubscriptionID: t.SubscriptionID,
		RequestID: "auapi_image:" + t.TaskID, Model: t.Model, RequestedModel: t.Model,
		UpstreamModel: &t.UpstreamModel, ImageCount: t.SuccessCount, ImageSize: &t.ImageSize,
		ImageOutputCost: t.UnitPrice * float64(t.SuccessCount), TotalCost: t.UnitPrice * float64(t.SuccessCount),
		ActualCost: t.ActualAmount, RateMultiplier: t.RateMultiplier,
		AccountRateMultiplier: &t.AccountRateMultiplier, BillingMode: &mode,
		BillingType: typ, RequestType: RequestTypeSync,
		InboundEndpoint: &inbound, UpstreamEndpoint: &upstream, CreatedAt: t.CreatedAt,
	}
}

func (s *AUAPIImageTaskService) invalidate(ctx context.Context, t *AUAPIImageTaskRecord) {
	if s.authCache != nil {
		s.authCache.InvalidateAuthCacheByUserID(ctx, t.UserID)
	}
	if s.billingCache != nil {
		s.billingCache.InvalidateUserBalance(ctx, t.UserID)
	}
}
func auapiImageTaskPublic(t *AUAPIImageTaskRecord) *ImageTask {
	var completed *int64
	if t.CompletedAt != nil {
		v := t.CompletedAt.Unix()
		completed = &v
	}
	return &ImageTask{ID: t.TaskID, TaskID: t.TaskID, Object: "image.generation.task", Status: t.Status, HTTPStatus: t.HTTPStatus, Result: t.ResultJSON, Error: t.ErrorJSON, CreatedAt: t.CreatedAt.Unix(), CompletedAt: completed, ExpiresAt: t.ExpiresAt.Unix(), ImageURL: firstImageTaskURL(t.ResultJSON)}
}

func (s *AUAPIImageTaskService) Start() {
	if !s.Enabled() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	launch := func(f func(context.Context)) { s.wg.Add(1); go func() { defer s.wg.Done(); f(ctx) }() }
	workers := s.cfg.AUAPIImage.WorkerCount
	if workers <= 0 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		launch(s.directWorker)
	}
}
func (s *AUAPIImageTaskService) directWorker(ctx context.Context) {
	for ctx.Err() == nil {
		ids, err := s.repo.Due(ctx, 1)
		if err == nil {
			for _, id := range ids {
				_, _ = s.Process(ctx, auapiQueueID(id))
			}
		} else {
			slog.Warn("auapi image recovery failed", "error", err)
		}
		sleepOrDone(ctx, time.Duration(maxAUAPIInt(1, s.cfg.AUAPIImage.PollIntervalSeconds))*time.Second)
	}
}
func maxAUAPIInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func (s *AUAPIImageTaskService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
}
