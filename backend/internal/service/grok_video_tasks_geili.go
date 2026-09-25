package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

// GrokVideoTaskSnapshot is a token-safe create-time contract; it deliberately
// stores neither full APIKey/Account nor request body, media URL or auth headers.
type GrokVideoTaskSnapshot struct {
	Version            int                     `json:"version"`
	TaskID             string                  `json:"task_id"`
	APIKeyID           int64                   `json:"api_key_id"`
	UserID             int64                   `json:"user_id"`
	AccountID          int64                   `json:"account_id"`
	FinancialRequestID string                  `json:"financial_request_id"`
	Pending            GrokVideoPendingBilling `json:"pending"`
	ChargePerUnit      string                  `json:"charge_per_unit"`
	StandardPerUnit    string                  `json:"standard_per_unit"`
	Unit               string                  `json:"unit"`
	Template           UsageBillingCommand     `json:"command_template"`
	Detail             UsageSettlementDetail   `json:"detail_template"`
	ChargeBalance      bool                    `json:"charge_balance"`
	ChargeSubscription bool                    `json:"charge_subscription"`
	ChargeKeyQuota     bool                    `json:"charge_key_quota"`
	ChargeKeyWindow    bool                    `json:"charge_key_window"`
	ChargeAccount      bool                    `json:"charge_account"`
	ChargePlatform     bool                    `json:"charge_platform"`
	AccountRate        float64                 `json:"account_rate"`
}
type GrokVideoTaskLease struct {
	Snapshot GrokVideoTaskSnapshot
	Token    string
}
type GrokVideoObservation struct {
	Status          string    `json:"status"`
	DurationSeconds int       `json:"duration_seconds"`
	OutputTokens    *int      `json:"output_tokens,omitempty"`
	CompletedAt     time.Time `json:"completed_at"`
}
type GrokVideoTaskRepository interface {
	StoreGrokVideoTask(context.Context, *GrokVideoTaskSnapshot) error
	LoadGrokVideoTask(context.Context, string, int64, int64) (*GrokVideoTaskSnapshot, error)
	LeaseGrokVideoTasks(context.Context, int) ([]GrokVideoTaskLease, error)
	ObserveGrokVideoTask(context.Context, *GrokVideoTaskSnapshot, string, GrokVideoObservation, *UsageBillingCommand, *UsageLog) error
	RetryGrokVideoTask(context.Context, *GrokVideoTaskSnapshot, string, string) error
}
type grokVideoCreateContextKey struct{}
type grokVideoCreateContract struct{ snapshot GrokVideoTaskSnapshot }

func (s *OpenAIGatewayService) HasDurableGrokVideoTasks() bool {
	if s == nil {
		return false
	}
	_, ok := s.usageBillingRepo.(GrokVideoTaskRepository)
	return ok
}
func (s *OpenAIGatewayService) LoadDurableGrokVideoTask(ctx context.Context, id string, user, key int64) (*GrokVideoTaskSnapshot, error) {
	r, ok := s.usageBillingRepo.(GrokVideoTaskRepository)
	if !ok {
		return nil, nil
	}
	task, err := r.LoadGrokVideoTask(ctx, id, user, key)
	if err != nil || task != nil {
		return task, err
	}
	root, e := openGrokVideoIngress(s.cfg)
	if e != nil {
		return nil, e
	}
	if root == nil {
		return nil, nil
	}
	defer root.Close()
	pending, e := readGrokVideoIngress(root, videoIngressName(&GrokVideoTaskSnapshot{FinancialRequestID: StableGrokVideoBillingRequestID(id), APIKeyID: key}))
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if pending.UserID != user || pending.APIKeyID != key || pending.TaskID != id {
		return nil, ErrUsageBillingRequestConflict
	}
	if e = r.StoreGrokVideoTask(ctx, pending); e != nil {
		return nil, e
	}
	return pending, nil
}

// PrepareGrokVideoTaskContext runs before sending the upstream create. Sample
// the existing calculator to freeze its linear/fixed tariff, including zero.
func (s *OpenAIGatewayService) PrepareGrokVideoTaskContext(ctx context.Context, key *APIKey, sub *UserSubscription, account *Account, info GrokMediaRequestInfo, original, platform string, started time.Time, providerEndpoint GrokMediaEndpoint) (context.Context, error) {
	if !s.HasDurableGrokVideoTasks() {
		return ctx, nil
	}
	if key == nil || key.User == nil || key.UserID != key.User.ID || account == nil || strings.TrimSpace(info.Model) == "" {
		return ctx, errors.New("video billing identity missing")
	}
	root, err := openGrokVideoIngress(s.cfg)
	if err != nil {
		return ctx, err
	}
	if root != nil {
		root.Close()
	}
	base := 1.0
	if s.cfg != nil {
		base = s.cfg.Default.RateMultiplier
	}
	if key.GroupID != nil && key.Group != nil {
		base = s.ResolveUserGroupRateMultiplier(ctx, key.UserID, *key.GroupID, key.Group.BillingRateMultiplier(sub != nil))
	}
	seedance := providerEndpoint.IsSeedance()
	mult := resolveVideoRateMultiplier(key, base)
	if seedance {
		mult, _ = computePeakAwareMultipliers(key, base, started)
	}
	resolution := NormalizeVideoBillingResolutionOrDefault(info.Resolution)
	model := info.Model
	if resolved := s.resolveOpenAIChannelPricing(ctx, model, key); resolved != nil {
		if !seedance && resolved.Mode == BillingModeToken {
			return ctx, errors.New("durable video task requires an explicit video or per-request tariff")
		}
		if seedance && len(resolved.Intervals) > 0 {
			return ctx, errors.New("durable Seedance requires a flat output-token tariff; configured tiers need a versioned snapshot")
		}
	}
	costs := make([]*CostBreakdown, 3)
	for i := range costs {
		if seedance {
			costs[i], err = s.calculateOpenAIRecordUsageTokenCost(ctx, key, model, mult, started, UsageTokens{OutputTokens: (i + 1) * 1000}, "", "", openAILongContextBillingGate(account))
			if err != nil {
				return ctx, fmt.Errorf("seedance frozen token tariff: %w", err)
			}
		} else {
			costs[i] = s.calculateOpenAIVideoCost(ctx, model, key, &OpenAIForwardResult{VideoCount: 1, VideoResolution: resolution, VideoDurationSeconds: i + 1}, mult)
		}
		if costs[i] == nil || math.IsNaN(costs[i].ActualCost) || math.IsInf(costs[i].ActualCost, 0) || math.IsNaN(costs[i].TotalCost) || math.IsInf(costs[i].TotalCost, 0) || costs[i].ActualCost < 0 || costs[i].TotalCost < 0 {
			return ctx, errors.New("invalid video tariff")
		}
	}
	same := func(a, b float64) bool {
		return decimal.NewFromFloat(a).Round(10).Equal(decimal.NewFromFloat(b).Round(10))
	}
	unit := "second"
	if same(costs[0].ActualCost, costs[1].ActualCost) && same(costs[1].ActualCost, costs[2].ActualCost) && same(costs[0].TotalCost, costs[1].TotalCost) && same(costs[1].TotalCost, costs[2].TotalCost) {
		unit = "request"
	} else if !same(costs[1].ActualCost, 2*costs[0].ActualCost) || !same(costs[2].ActualCost, 3*costs[0].ActualCost) || !same(costs[1].TotalCost, 2*costs[0].TotalCost) || !same(costs[2].TotalCost, 3*costs[0].TotalCost) {
		return ctx, errors.New("unsupported nonlinear video tariff; task not submitted")
	}
	if seedance {
		if unit != "second" && (costs[0].TotalCost != 0 || costs[0].ActualCost != 0) {
			return ctx, errors.New("seedance token tariff must be linear per token")
		}
		unit = "output_token"
		for _, cost := range costs {
			cost.ActualCost /= 1000
			cost.TotalCost /= 1000
		}
	}
	simple := s.cfg != nil && s.cfg.RunMode == config.RunModeSimple
	isSub := sub != nil && key.UsesSubscriptionBilling()
	billingType := BillingTypeBalance
	if isSub {
		billingType = BillingTypeSubscription
	}
	log := &UsageLog{UserID: key.UserID, APIKeyID: key.ID, AccountID: account.ID, GroupID: key.GroupID, SubscriptionID: optionalSubscriptionID(sub), Model: model, RequestedModel: original, UpstreamModel: optionalTrimmedStringPtr(account.GetMappedModel(model)), BillingType: billingType, RequestType: RequestTypeSync, RateMultiplier: mult, BillingMode: optionalTrimmedStringPtr(string(BillingModeVideo)), VideoCount: 1, VideoResolution: &resolution, CreatedAt: started}
	if seedance {
		log.VideoCount = 0
		log.VideoResolution = nil
		log.BillingMode = optionalTrimmedStringPtr(string(BillingModeToken))
	}
	log.CaptureRouteBilling(key)
	detail, err := NewUsageSettlementDetail(log)
	if err != nil {
		return ctx, err
	}
	p := postUsageBillingParams{Cost: costs[0], User: key.User, APIKey: key, Account: account, Subscription: sub, IsSubscriptionBill: isSub, AccountRateMultiplier: account.BillingRateMultiplier(), Platform: platform, SimpleModeKeyRateLimitOnly: simpleModeKeyRateLimitBillingEnabled(s.cfg, key)}
	cmd := buildUsageBillingCommand("pending", log, &p)
	if cmd == nil {
		return ctx, errors.New("video billing command unavailable")
	}
	cmd.RequestFingerprint = ""
	cmd.RequestID = ""
	cmd.UsageDetail = nil
	cmd.CompletedAt = time.Time{}
	snap := GrokVideoTaskSnapshot{Version: 1, APIKeyID: key.ID, UserID: key.UserID, AccountID: account.ID, Pending: GrokVideoPendingBilling{Model: model, BillingModel: model, UpstreamModel: account.GetMappedModel(model), OriginalModel: original, VideoResolution: resolution, VideoDurationSeconds: NormalizeVideoBillingDurationSecondsOrDefault(info.DurationSeconds), CreatedAt: started.UTC().Format(time.RFC3339Nano)}, ChargePerUnit: decimal.NewFromFloat(costs[0].ActualCost).String(), StandardPerUnit: decimal.NewFromFloat(costs[0].TotalCost).String(), Unit: unit, Template: *cmd, Detail: *detail, ChargeBalance: !simple && !isSub, ChargeSubscription: !simple && isSub, ChargeKeyQuota: !simple && key.Quota > 0, ChargeKeyWindow: (!simple || p.SimpleModeKeyRateLimitOnly) && key.HasRateLimits(), ChargeAccount: !simple && p.shouldUpdateAccountQuota(), ChargePlatform: !simple && !isSub && platform != "", AccountRate: account.BillingRateMultiplier()}
	// Policy: Live is elsewhere; simple mode must never debit any monetary account.
	snap.Template.BalanceCost = 0
	snap.Template.SubscriptionCost = 0
	snap.Template.APIKeyQuotaCost = 0
	snap.Template.APIKeyRateLimitCost = 0
	snap.Template.AccountQuotaCost = 0
	snap.Template.PlatformQuotaCost = 0
	raw, err := json.Marshal(snap)
	if err != nil {
		return ctx, err
	}
	var frozen GrokVideoTaskSnapshot
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return ctx, err
	}
	if frozen.Unit == "output_token" {
		frozen.Pending.VideoResolution = ""
		frozen.Pending.VideoDurationSeconds = 0
	}
	return context.WithValue(ctx, grokVideoCreateContextKey{}, grokVideoCreateContract{frozen}), nil
}
func (s *OpenAIGatewayService) persistGrokVideoAccepted(ctx context.Context, taskID string) error {
	contract, ok := ctx.Value(grokVideoCreateContextKey{}).(grokVideoCreateContract)
	if !ok {
		return nil
	}
	r, ok := s.usageBillingRepo.(GrokVideoTaskRepository)
	if !ok {
		return errors.New("durable video repository unavailable")
	}
	snap := contract.snapshot
	snap.TaskID = strings.TrimSpace(taskID)
	snap.FinancialRequestID = StableGrokVideoBillingRequestID(taskID)
	return persistGrokVideoIngress(ctx, s.cfg, r, &snap)
}
func (s *OpenAIGatewayService) ObserveDurableGrokVideoResult(ctx context.Context, task string, user, key int64, result *OpenAIForwardResult) (bool, error) {
	r, ok := s.usageBillingRepo.(GrokVideoTaskRepository)
	if !ok {
		return false, nil
	}
	snap, err := s.LoadDurableGrokVideoTask(ctx, task, user, key)
	if err != nil {
		return true, err
	}
	if snap == nil {
		return false, nil
	}
	if result == nil {
		return true, nil
	}
	if snap.Unit == "output_token" {
		if result.UpstreamTerminalEvent != "succeeded_usage_verified" || result.Usage.OutputTokens < 0 {
			return true, errors.New("invalid completion tokens")
		}
	} else if result.VideoCount <= 0 {
		return true, nil
	}
	observation := GrokVideoObservation{Status: "done", DurationSeconds: result.VideoDurationSeconds, CompletedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if snap.Unit == "output_token" {
		v := result.Usage.OutputTokens
		observation.OutputTokens = &v
	}
	cmd, log, err := BuildGrokVideoSettlement(snap, observation)
	if err != nil {
		return true, err
	}
	return true, r.ObserveGrokVideoTask(ctx, snap, "", observation, cmd, log)
}
func BuildGrokVideoSettlement(snap *GrokVideoTaskSnapshot, o GrokVideoObservation) (*UsageBillingCommand, *UsageLog, error) {
	if snap == nil || snap.Template.UserID != snap.UserID || snap.Template.APIKeyID != snap.APIKeyID || snap.Template.AccountID != snap.AccountID || snap.Detail.UserID != snap.UserID || snap.Detail.APIKeyID != snap.APIKeyID || snap.Detail.AccountID != snap.AccountID || snap.Version != 1 || snap.TaskID == "" || snap.FinancialRequestID != StableGrokVideoBillingRequestID(snap.TaskID) {
		return nil, nil, errors.New("invalid durable video identity")
	}
	if o.Status != "done" {
		return nil, nil, errors.New("video not completed")
	}
	seconds := o.DurationSeconds
	if seconds <= 0 {
		seconds = snap.Pending.VideoDurationSeconds
	}
	if snap.Unit != "output_token" && seconds <= 0 {
		return nil, nil, errors.New("video duration missing/out of range")
	}
	if snap.Unit != "output_token" {
		seconds = NormalizeVideoBillingDurationSecondsOrDefault(seconds)
	}
	units := int64(seconds)
	if snap.Unit == "request" {
		units = 1
	} else if snap.Unit == "output_token" {
		if o.OutputTokens == nil || *o.OutputTokens < 0 || *o.OutputTokens > 1000000000 {
			return nil, nil, errors.New("reported completion tokens required")
		}
		units = int64(*o.OutputTokens)
	} else if snap.Unit != "second" {
		return nil, nil, errors.New("unknown video tariff units")
	}
	charge, err := decimal.NewFromString(snap.ChargePerUnit)
	if err != nil || charge.IsNegative() {
		return nil, nil, errors.New("invalid video charge snapshot")
	}
	standard, err := decimal.NewFromString(snap.StandardPerUnit)
	if err != nil || standard.IsNegative() {
		return nil, nil, errors.New("invalid video standard snapshot")
	}
	actual, _ := charge.Mul(decimal.NewFromInt(units)).Round(8).Float64()
	total, _ := standard.Mul(decimal.NewFromInt(units)).Float64()
	cmd := snap.Template
	cmd.BalanceCost = 0
	cmd.SubscriptionCost = 0
	cmd.APIKeyQuotaCost = 0
	cmd.APIKeyRateLimitCost = 0
	cmd.AccountQuotaCost = 0
	cmd.PlatformQuotaCost = 0
	cmd.RequestID = snap.FinancialRequestID
	cmd.RequestFingerprint = ""
	cmd.CompletedAt = o.CompletedAt
	cmd.RequestPayloadHash = HashUsageRequestPayload([]byte(snap.TaskID))
	if cmd.CompletedAt.IsZero() {
		cmd.CompletedAt = time.Now().UTC().Truncate(time.Microsecond)
	}
	if snap.ChargeBalance {
		cmd.BalanceCost = actual
	}
	if snap.ChargeSubscription {
		cmd.SubscriptionCost = actual
	}
	if snap.ChargeKeyQuota {
		cmd.APIKeyQuotaCost = actual
	}
	if snap.ChargeKeyWindow {
		cmd.APIKeyRateLimitCost = actual
	}
	if snap.ChargeAccount {
		cmd.AccountQuotaCost = total * snap.AccountRate
	}
	if snap.ChargePlatform {
		cmd.PlatformQuotaCost = actual
	}
	// UsageLog() intentionally borrows pointer/map fields. Deep-copy the safe
	// scalar whitelist before changing financial values; the accepted snapshot
	// is immutable and must still match its SQL JSON after building a bill.
	frozenDetail, err := NewUsageSettlementDetail(snap.Detail.UsageLog())
	if err != nil {
		return nil, nil, err
	}
	log := frozenDetail.UsageLog()
	log.RequestID = snap.FinancialRequestID
	log.ActualCost = cmd.BalanceCost + cmd.SubscriptionCost
	log.TotalCost = total
	log.VideoDurationSeconds = &seconds
	log.CreatedAt = cmd.CompletedAt
	duration := int(GrokVideoE2EDuration(snap.Pending.CreatedAt, cmd.CompletedAt).Milliseconds())
	log.DurationMs = &duration
	log.VideoCount = 1
	if snap.Unit == "output_token" {
		log.VideoCount = 0
		log.VideoResolution = nil
		log.VideoDurationSeconds = nil
		log.OutputTokens = int(units)
		log.OutputCost = total
		cmd.OutputTokens = int(units)
	}
	if log.RouteBillingSnapshot != nil {
		log.RouteBillingSnapshot.ActualCost = log.ActualCost
		log.RouteBillingSnapshot.RawCost = log.TotalCost
	}
	cmd.UsageDetail = log
	cmd.Normalize()
	if err = ValidateUsageSettlementCommand(&cmd); err != nil {
		return nil, nil, err
	}
	return &cmd, log, nil
}

// ProcessDurableGrokVideoTasks polls only previously accepted task IDs. It never
// creates upstream jobs or resumes disabled/suspended provider accounts.
func (s *OpenAIGatewayService) ProcessDurableGrokVideoTasks(ctx context.Context, limit int) error {
	r, ok := s.usageBillingRepo.(GrokVideoTaskRepository)
	if !ok {
		return nil
	}
	replayErr := replayGrokVideoIngress(ctx, s.cfg, r)
	tasks, err := r.LeaseGrokVideoTasks(ctx, limit)
	if err != nil {
		return err
	}
	first := replayErr
	for _, task := range tasks {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		snap := task.Snapshot
		taskCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		obs, e := s.pollDurableGrokVideo(taskCtx, &snap)
		if e == nil && (obs.Status == "done") {
			var cmd *UsageBillingCommand
			var detail *UsageLog
			cmd, detail, e = BuildGrokVideoSettlement(&snap, obs)
			if e == nil {
				e = r.ObserveGrokVideoTask(taskCtx, &snap, task.Token, obs, cmd, detail)
			}
		} else if e == nil && (obs.Status == "failed" || obs.Status == "expired" || obs.Status == "cancelled") {
			e = r.ObserveGrokVideoTask(taskCtx, &snap, task.Token, obs, nil, nil)
		} else if e == nil {
			e = r.RetryGrokVideoTask(taskCtx, &snap, task.Token, "pending")
		}
		cancel()
		if e != nil {
			if first == nil {
				first = e
			}
			retry, c := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			_ = r.RetryGrokVideoTask(retry, &snap, task.Token, "retryable_poll_or_prepare_error")
			c()
		}
	}
	return first
}
func (s *OpenAIGatewayService) pollDurableGrokVideo(ctx context.Context, snap *GrokVideoTaskSnapshot) (GrokVideoObservation, error) {
	var out GrokVideoObservation
	account, err := s.accountRepo.GetByID(ctx, snap.AccountID)
	if err != nil {
		return out, err
	}
	if account == nil || account.Status != StatusActive || !account.Schedulable {
		return out, errors.New("video provider paused or unavailable")
	}
	token, _, err := s.getRequestCredential(ctx, nil, account)
	if err != nil {
		return out, err
	}
	target, err := buildGrokMediaURL(account, s.cfg, GrokMediaEndpointVideoStatus, snap.TaskID)
	if snap.Unit == "output_token" {
		var base string
		base, err = s.validateUpstreamBaseURL(account.GetCredential("base_url"))
		if err == nil {
			target, err = buildSeedanceURL(base, SeedanceEndpointStatus, strings.TrimPrefix(snap.TaskID, "seedance:"))
		}
	}
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(ctx), http.MethodGet, target, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if account.IsGrokOAuth() && isGrokCLIProxyTarget(target) {
		applyGrokCLIHeaders(req.Header)
	}
	account.ApplyHeaderOverrides(req.Header)
	proxy := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("video status HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return out, err
	}
	if len(raw) >= 1<<20 {
		return out, errors.New("video status exceeds safe limit")
	}
	if !gjson.ValidBytes(raw) {
		return out, errors.New("invalid video status")
	}
	out.Status = strings.ToLower(gjson.GetBytes(raw, "status").String())
	out.CompletedAt = time.Now().UTC().Truncate(time.Microsecond)
	if snap.Unit == "output_token" {
		if out.Status == "succeeded" {
			value := gjson.GetBytes(raw, "usage.completion_tokens")
			if !value.Exists() || value.Type != gjson.Number || value.Int() < 0 || value.Float() != float64(value.Int()) || value.Int() > 1000000000 {
				return out, errors.New("seedance succeeded without reported completion tokens")
			}
			n := int(value.Int())
			out.OutputTokens = &n
			out.Status = "done"
		}
		if out.Status == "canceled" {
			out.Status = "cancelled"
		}
		return out, nil
	}
	if out.Status == "done" {
		if !IsGrokVideoStatusBillable(raw) {
			return out, errors.New("completed video has no output evidence")
		}
		out.DurationSeconds, err = observedGrokVideoDuration(raw)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

type GrokVideoTaskHealth struct {
	Pending              int64   `json:"pending"`
	Prepared             int64   `json:"prepared"`
	Failed               int64   `json:"failed"`
	Retrying             int64   `json:"retrying"`
	OldestPendingSeconds float64 `json:"oldest_pending_seconds"`
	IngressPending       int64   `json:"ingress_pending"`
	IngressOldestSeconds float64 `json:"ingress_oldest_seconds"`
	IngressScanTruncated bool    `json:"ingress_scan_truncated"`
	Error                string  `json:"error,omitempty"`
}

func (s *OpenAIGatewayService) GrokVideoTaskHealth(ctx context.Context) GrokVideoTaskHealth {
	var h GrokVideoTaskHealth
	if r, ok := s.usageBillingRepo.(interface {
		GrokVideoTaskHealth(context.Context) (GrokVideoTaskHealth, error)
	}); ok {
		value, err := r.GrokVideoTaskHealth(ctx)
		h = value
		if err != nil {
			h.Error = err.Error()
		}
	}
	count, oldest, truncated, err := grokVideoIngressHealth(ctx, s.cfg)
	h.IngressPending = count
	h.IngressOldestSeconds = oldest
	h.IngressScanTruncated = truncated
	if err != nil {
		h.Error = err.Error()
	}
	return h
}

// The same parser is used by HTTP observations and background polling so a
// malformed or oversized upstream duration cannot choose a different bill.
func observedGrokVideoDuration(raw []byte) (int, error) {
	v := gjson.GetBytes(raw, "video.duration")
	if !v.Exists() {
		return 0, nil
	}
	if v.Type != gjson.Number || v.Float() != float64(v.Int()) || v.Int() <= 0 || v.Int() > 1000000000 {
		return 0, errors.New("invalid observed video duration")
	}
	return NormalizeVideoBillingDurationSecondsOrDefault(int(v.Int())), nil
}
