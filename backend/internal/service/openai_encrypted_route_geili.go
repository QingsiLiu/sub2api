package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Only opaque-source metadata is persisted. Ciphertext and credentials never leave the request.
type OpenAIEncryptedRouteGeili struct {
	Digest             string    `json:"digest"`
	GroupID            int64     `json:"group_id"`
	AccountID          int64     `json:"account_id"`
	Model              string    `json:"model"`
	BackendFingerprint string    `json:"backend_fingerprint"`
	Conflict           bool      `json:"conflict"`
	GeneratedAt        time.Time `json:"generated_at"`
	CompatibleGroupIDs []int64   `json:"-"`
}
type OpenAIEncryptedRouteCacheGeili interface {
	GetOpenAIEncryptedRoutesGeili(context.Context, int64, []string) (map[string]OpenAIEncryptedRouteGeili, error)
	SetOpenAIEncryptedRoutesGeili(context.Context, int64, []OpenAIEncryptedRouteGeili) error
}

var ErrOpenAIEncryptedRouteUnavailableGeili = errors.New("original encrypted conversation route unavailable")

type OpenAIEncryptedRouteErrorGeili struct {
	Status        int
	Code, Message string
}

func (e *OpenAIEncryptedRouteErrorGeili) Error() string { return e.Message }
func (e *OpenAIEncryptedRouteErrorGeili) Unwrap() error {
	return ErrOpenAIEncryptedRouteUnavailableGeili
}
func encryptedRouteErrorGeili(status int, code, message string) error {
	return &OpenAIEncryptedRouteErrorGeili{status, code, message}
}

type openAIEncryptedRouteContextKeyGeili struct{}
type openAIEncryptedRoutePolicyKeyGeili struct{}
type openAIEncryptedRouteStateGeili struct {
	mu                           sync.Mutex
	captureMu                    sync.Mutex
	captureSpent                 time.Duration
	service                      *OpenAIGatewayService
	userID                       int64
	enabled, protected, recovery bool
	owner                        *OpenAIEncryptedRouteGeili
	origin                       *OpenAIEncryptedRouteGeili
	claimed                      bool
	observed                     map[string]bool
	waitSpent                    time.Duration
}

var encryptedRouteLookupFailuresGeili atomic.Uint64
var encryptedRouteRecordFailuresGeili atomic.Uint64
var encryptedRouteUnknownGeili atomic.Uint64
var encryptedRouteQueueDropsGeili atomic.Uint64

func encryptedRouteStateGeili(ctx context.Context) *openAIEncryptedRouteStateGeili {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(openAIEncryptedRouteContextKeyGeili{}).(*openAIEncryptedRouteStateGeili)
	return state
}

// Policy injection also lets mocked acceptance freeze a validated policy without a settings DB.
func WithOpenAIEncryptedRoutePolicyGeili(ctx context.Context, policy OpenAIEncryptedRouteSettingsGeili) context.Context {
	return context.WithValue(ctx, openAIEncryptedRoutePolicyKeyGeili{}, policy)
}
func OpenAIEncryptedRouteProtectedGeili(ctx context.Context) bool {
	s := encryptedRouteStateGeili(ctx)
	return s != nil && s.protected
}
func OpenAIEncryptedRoutePinnedGeili(ctx context.Context) bool {
	s := encryptedRouteStateGeili(ctx)
	return s != nil && s.protected && s.owner != nil
}
func OpenAIEncryptedRouteRecoveryEnabledGeili(ctx context.Context) bool {
	s := encryptedRouteStateGeili(ctx)
	return s != nil && s.protected && s.recovery
}
func ClaimOpenAIEncryptedRecoveryGeili(ctx context.Context) bool {
	s := encryptedRouteStateGeili(ctx)
	if s == nil || !s.protected || !s.recovery || !BeginRequestRecovery(ctx) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimed {
		return false
	}
	s.claimed = true
	return true
}

// Scan only protocol items, not user strings or tool-schema descriptions.
func encryptedDigestsGeili(body []byte) []string {
	seen := map[string]bool{}
	out := []string{}
	var walk func(gjson.Result)
	walk = func(v gjson.Result) {
		if v.IsArray() {
			v.ForEach(func(_, p gjson.Result) bool { walk(p); return len(out) < 256 })
			return
		}
		if !v.IsObject() {
			return
		}
		v.ForEach(func(k, p gjson.Result) bool {
			if k.String() == "encrypted_content" && p.Type == gjson.String && p.String() != "" {
				d := openAIEncryptedContentDigest(p.String())
				if !seen[d] {
					seen[d] = true
					out = append(out, d)
				}
			}
			if p.IsObject() || p.IsArray() {
				walk(p)
			}
			return len(out) < 256
		})
	}
	input := openAIRequestPayloadView(body).Get("input")
	walk(input)
	return out
}
func HasOpenAIOpaqueToolStateGeili(body []byte) bool {
	input := openAIRequestPayloadView(body).Get("input")
	found := false
	check := func(item gjson.Result) {
		if item.Get("type").String() == "reasoning" {
			return
		}
		raw := []byte(`{"input":[` + item.Raw + `]}`)
		if len(encryptedDigestsGeili(raw)) > 0 {
			found = true
		}
	}
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool { check(item); return !found })
	} else if input.IsObject() {
		check(input)
	}
	return found
}
func (s *OpenAIGatewayService) PrepareOpenAIEncryptedRouteGeili(c *gin.Context, key *APIKey, body []byte) (*OpenAIEncryptedRouteGeili, error) {
	if c == nil || c.Request == nil {
		return nil, nil
	}
	if state := encryptedRouteStateGeili(c.Request.Context()); state != nil {
		return state.owner, nil
	}
	ctx, owner, err := s.PrepareOpenAIEncryptedTurnGeili(c.Request.Context(), key, body)
	c.Request = c.Request.WithContext(ctx)
	return owner, err
}
func (s *OpenAIGatewayService) PrepareOpenAIEncryptedTurnGeili(ctx context.Context, key *APIKey, body []byte) (context.Context, *OpenAIEncryptedRouteGeili, error) {
	if s == nil || key == nil || key.UserID <= 0 {
		return ctx, nil, nil
	}
	policy, ok := ctx.Value(openAIEncryptedRoutePolicyKeyGeili{}).(OpenAIEncryptedRouteSettingsGeili)
	if !ok {
		policy = DefaultOpenAIEncryptedRouteSettingsGeili()
		if s.settingService != nil {
			policy = s.settingService.GetOpenAIEncryptedRouteSettingsGeili(ctx)
		}
	}
	if policy.Mode == "off" {
		return ctx, nil, nil
	}
	eligible := false
	if policy.Mode == "protect" {
		for _, id := range policy.ProtectedUserIDs {
			eligible = eligible || id == key.UserID
		}
	}
	digests := encryptedDigestsGeili(body)
	state := &openAIEncryptedRouteStateGeili{service: s, userID: key.UserID, enabled: eligible, protected: eligible && len(digests) > 0, observed: map[string]bool{}}
	next := context.WithValue(ctx, openAIEncryptedRouteContextKeyGeili{}, state)
	cache, ok := s.cache.(OpenAIEncryptedRouteCacheGeili)
	if !ok || len(digests) == 0 {
		return next, nil, nil
	}
	lookupStarted := time.Now()
	prefHint := false
	if eligible {
		if hints, ok := cache.(interface {
			HasOpenAIEncryptedSourceHintGeili(context.Context, int64, []string) (bool, error)
		}); ok {
			hintCtx, hintCancel := context.WithTimeout(ctx, 10*time.Millisecond)
			present, hintErr := hints.HasOpenAIEncryptedSourceHintGeili(hintCtx, key.UserID, digests)
			hintCancel()
			prefHint = present && hintErr == nil
		}
	}
	lookupBudget := 50 * time.Millisecond
	if prefHint {
		// Verified presence avoids canceling a tracked session's first SQL read
		// after 50 ms. The hint supplies no authority; SQL must still verify it.
		lookupBudget = 250 * time.Millisecond
	}
	lookup, cancel := context.WithDeadline(ctx, lookupStarted.Add(lookupBudget))
	defer cancel()
	sources, err := cache.GetOpenAIEncryptedRoutesGeili(lookup, key.UserID, digests)
	if err != nil {
		encryptedRouteLookupFailuresGeili.Add(1)
		var hint interface{ KnownOpenAIEncryptedSourceHintGeili() bool }
		tracked := prefHint || (errors.As(err, &hint) && hint.KnownOpenAIEncryptedSourceHintGeili())
		slog.Warn("encrypted_route.lookup_failed", "user_id", key.UserID, "error_class", encryptedPersistenceFailureClassGeili(err), "lookup_phase", encryptedRouteLookupPhaseGeili(err), "tracked_source_hint", tracked)
		if !prefHint && eligible && tracked && errors.Is(lookup.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			// Retry metadata only, once. The hint cannot authorize an origin;
			// PostgreSQL must supply a fresh result within the total 250 ms window.
			cancel()
			var retryCancel context.CancelFunc
			lookup, retryCancel = context.WithDeadline(ctx, lookupStarted.Add(250*time.Millisecond))
			defer retryCancel()
			sources, err = cache.GetOpenAIEncryptedRoutesGeili(lookup, key.UserID, digests)
			resultClass := "none"
			if err != nil {
				resultClass = encryptedPersistenceFailureClassGeili(err)
			}
			slog.Info("encrypted_route.lookup_retry", "user_id", key.UserID, "error_class", resultClass, "lookup_phase", encryptedRouteLookupPhaseGeili(err), "elapsed_ms", time.Since(lookupStarted).Milliseconds())
			if err == nil && len(sources) == 0 {
				return next, nil, encryptedRouteErrorGeili(503, "encrypted_session_verification_unavailable", "暂时无法核验已追踪的会话加密状态，请稍后重试，勿切换线路")
			}
		}
		// A cache-presence hint establishes prior tracking, never route authority.
		// Keep its encrypted history intact until PostgreSQL can verify the source.
		if err != nil && eligible && tracked {
			return next, nil, encryptedRouteErrorGeili(503, "encrypted_session_verification_unavailable", "暂时无法核验已追踪的会话加密状态，请稍后重试，勿切换线路")
		}
		if err != nil {
			return next, nil, nil
		}
	}
	if prefHint && len(sources) == 0 {
		return next, nil, encryptedRouteErrorGeili(503, "encrypted_session_verification_unavailable", "暂时无法核验已追踪的会话加密状态，请稍后重试，勿切换线路")
	}
	slog.Info("encrypted_route.lookup", "user_id", key.UserID, "mode", policy.Mode, "model", gjson.GetBytes(body, "model").String(), "cipher_count", len(digests), "known_count", len(sources))
	if !eligible {
		return next, nil, nil
	}
	for _, d := range digests {
		source, known := sources[d]
		if !known {
			encryptedRouteUnknownGeili.Add(1)
			continue
		}
		if source.Conflict {
			return next, nil, encryptedRouteErrorGeili(409, "encrypted_session_route_conflict", "会话加密状态的来源冲突，请联系技术支持恢复原线路")
		}
		if state.owner != nil && (state.owner.AccountID != source.AccountID || state.owner.Model != source.Model || state.owner.BackendFingerprint != source.BackendFingerprint) {
			return next, nil, encryptedRouteErrorGeili(409, "encrypted_session_route_conflict", "会话混合了不兼容线路的加密状态，请联系技术支持恢复上下文")
		}
		owner := source
		state.owner = &owner
	}
	if state.owner != nil && s.accountRepo != nil {
		account, e := s.accountRepo.GetByID(lookup, state.owner.AccountID)
		if e == nil && account != nil {
			state.owner.CompatibleGroupIDs = append([]int64(nil), account.GroupIDs...)
			for _, membership := range account.AccountGroups {
				state.owner.CompatibleGroupIDs = append(state.owner.CompatibleGroupIDs, membership.GroupID)
			}
		}
	}
	// Unknown historical ciphertext is never eligible for destructive recovery.
	state.recovery = policy.ReasoningRecoveryEnabled && state.owner != nil && len(sources) == len(digests) && len(digests) < 256
	return next, state.owner, nil
}

func encryptedRouteLookupPhaseGeili(err error) string {
	var failure interface{ EncryptedRouteLookupPhaseGeili() string }
	if errors.As(err, &failure) {
		switch phase := failure.EncryptedRouteLookupPhaseGeili(); phase {
		case "query", "rows", "close":
			return phase
		}
	}
	return "unknown"
}
func FilterOpenAIEncryptedRoutesGeili(candidates []CompositeRouteDecision, owner *OpenAIEncryptedRouteGeili) []CompositeRouteDecision {
	if owner == nil {
		return candidates
	}
	for _, c := range candidates {
		if c.TargetPlatform == PlatformOpenAI && c.TargetGroupID != nil && *c.TargetGroupID == owner.GroupID {
			if len(owner.CompatibleGroupIDs) == 0 {
				return []CompositeRouteDecision{c}
			}
			for _, current := range owner.CompatibleGroupIDs {
				if current == owner.GroupID {
					return []CompositeRouteDecision{c}
				}
			}
		}
	}
	for _, c := range candidates {
		if c.TargetPlatform != PlatformOpenAI || c.TargetGroupID == nil {
			continue
		}
		for _, g := range owner.CompatibleGroupIDs {
			if g == *c.TargetGroupID {
				return []CompositeRouteDecision{c}
			}
		}
	}
	return nil
}
func encryptedBackendFingerprintGeili(a *Account) string {
	if a == nil {
		return ""
	}
	// Account identity is conservative; hidden supplier pool members cannot be inferred.
	parts := []string{fmt.Sprint(a.ID), a.Platform, a.Type, a.GetOpenAIBaseURL(), a.GetCredential("organization_id"), a.GetCredential("chatgpt_account_id"), a.GetCredential("api_key")}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

func ValidateOpenAIEncryptedAccountGeili(ctx context.Context, a *Account, model string) error {
	state := encryptedRouteStateGeili(ctx)
	if state == nil || !state.protected || state.owner == nil {
		return nil
	}
	owner := state.owner
	if a == nil || owner.AccountID != a.ID || owner.BackendFingerprint != encryptedBackendFingerprintGeili(a) || owner.Model != canonicalOpenAIAccountSchedulingModel(a, model) {
		return encryptedRouteErrorGeili(409, "encrypted_session_route_conflict", "当前线路与会话加密状态不兼容，请联系技术支持恢复原线路")
	}
	return nil
}

func openAIEncryptedTargetGroupGeili(ctx context.Context, groupID *int64) *int64 {
	if target, ok := CompositeTargetGroupIDFromContext(ctx); ok {
		return &target
	}
	return groupID
}

func WithOpenAIEncryptedOutputOriginGeili(ctx context.Context, groupID *int64, a *Account, model string) context.Context {
	groupID = openAIEncryptedTargetGroupGeili(ctx, groupID)
	state := encryptedRouteStateGeili(ctx)
	if state == nil || groupID == nil || a == nil || a.Platform != PlatformOpenAI {
		return ctx
	}
	state.mu.Lock()
	state.origin = &OpenAIEncryptedRouteGeili{GroupID: *groupID, AccountID: a.ID, Model: canonicalOpenAIAccountSchedulingModel(a, model), BackendFingerprint: encryptedBackendFingerprintGeili(a)}
	state.mu.Unlock()
	return ctx
}

// Only a validated reasoning-only body may release its opaque source route.
// The request-wide claim prevents a second recovery in any transport/group.
func ReleaseOpenAIEncryptedReasoningRouteGeili(ctx context.Context, body []byte) ([]byte, bool) {
	if !OpenAIEncryptedRoutePinnedGeili(ctx) || !OpenAIEncryptedRouteRecoveryEnabledGeili(ctx) {
		return body, false
	}
	next, changed, _ := PrepareOpenAIEncryptedReasoningRecoveryGeili(body)
	if !changed || !ClaimOpenAIEncryptedRecoveryGeili(ctx) {
		return body, false
	}
	state := encryptedRouteStateGeili(ctx)
	state.mu.Lock()
	state.owner = nil
	state.origin = nil
	state.mu.Unlock()
	slog.Info("encrypted_route.reasoning_route_recovery", "user_id", state.userID)
	return next, true
}

type encryptedRouteWriteGeili struct {
	cache   OpenAIEncryptedRouteCacheGeili
	userID  int64
	records []OpenAIEncryptedRouteGeili
	budget  time.Duration
}

var encryptedRouteWritesGeili struct {
	once  sync.Once
	queue chan encryptedRouteWriteGeili
}

// Capture is a durable write, unlike the latency-sensitive 50ms route lookup.
// Protect requests share this cap across all delivered output items; generation
// time between items does not consume it. Observe batches retain a bounded queue.
const encryptedRouteCaptureWriteBudgetGeili = 500 * time.Millisecond

func persistEncryptedRoutesGeili(w encryptedRouteWriteGeili) {
	budget := w.budget
	if budget <= 0 {
		budget = encryptedRouteCaptureWriteBudgetGeili
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	if err := w.cache.SetOpenAIEncryptedRoutesGeili(ctx, w.userID, w.records); err != nil {
		encryptedRouteRecordFailuresGeili.Add(1)
		slog.Warn("encrypted_route.record_failed", "user_id", w.userID, "error_class", encryptedPersistenceFailureClassGeili(err), "storage_phase", encryptedPersistenceStoragePhaseGeili(err))
	}
}

// Serialization keeps concurrent delivery callbacks within one request cap.
func persistProtectedEncryptedRoutesGeili(state *openAIEncryptedRouteStateGeili, w encryptedRouteWriteGeili) {
	state.captureMu.Lock()
	defer state.captureMu.Unlock()
	remaining := encryptedRouteCaptureWriteBudgetGeili - state.captureSpent
	if remaining <= 0 {
		encryptedRouteRecordFailuresGeili.Add(1)
		slog.Warn("encrypted_route.record_failed", "user_id", w.userID, "error_class", "deadline", "storage_phase", "write_budget")
		return
	}
	w.budget = remaining
	start := time.Now()
	persistEncryptedRoutesGeili(w)
	state.captureSpent += time.Since(start)
}

func ObserveOpenAIEncryptedOutputGeili(ctx context.Context, payload []byte) {
	state := encryptedRouteStateGeili(ctx)
	if state == nil || state.service == nil {
		return
	}
	cache, ok := state.service.cache.(OpenAIEncryptedRouteCacheGeili)
	if !ok {
		return
	}
	event := gjson.GetBytes(payload, "type").String()
	var items gjson.Result
	if event == "response.output_item.done" {
		items = gjson.GetBytes(payload, "item")
	} else if event == "response.completed" {
		items = gjson.GetBytes(payload, "response.output")
	} else if (event == "response" || gjson.GetBytes(payload, "object").String() == "response") && gjson.GetBytes(payload, "status").String() == "completed" {
		items = gjson.GetBytes(payload, "output")
	} else if gjson.GetBytes(payload, "object").String() == "response.compaction" {
		items = gjson.GetBytes(payload, "output")
	} else {
		return
	}
	// Only completed output items can become replay authorities; input echoed in errors never does.
	raw := []byte(`{"input":` + items.Raw + `}`)
	digests := encryptedDigestsGeili(raw)
	if len(digests) == 0 {
		return
	}
	state.mu.Lock()
	if state.origin == nil {
		state.mu.Unlock()
		return
	}
	records := []OpenAIEncryptedRouteGeili{}
	for _, d := range digests {
		if state.observed[d] {
			continue
		}
		state.observed[d] = true
		r := *state.origin
		r.Digest = d
		r.GeneratedAt = time.Now()
		records = append(records, r)
	}
	state.mu.Unlock()
	if len(records) == 0 {
		return
	}
	w := encryptedRouteWriteGeili{cache: cache, userID: state.userID, records: records}
	if state.enabled {
		persistProtectedEncryptedRoutesGeili(state, w)
		return
	}
	encryptedRouteWritesGeili.once.Do(func() {
		encryptedRouteWritesGeili.queue = make(chan encryptedRouteWriteGeili, 1024)
		go func() {
			for entry := range encryptedRouteWritesGeili.queue {
				persistEncryptedRoutesGeili(entry)
			}
		}()
	})
	select {
	case encryptedRouteWritesGeili.queue <- w:
	default:
		count := encryptedRouteQueueDropsGeili.Add(1)
		if count&(count-1) == 0 {
			slog.Warn("encrypted_route.observation_queue_full", "dropped", count)
		}
	}
}

const encryptedRouteMaxWaitGeili = 15 * time.Second

func encryptedRouteWaitRemainingGeili(ctx context.Context, state *openAIEncryptedRouteStateGeili) time.Duration {
	state.mu.Lock()
	remaining := encryptedRouteMaxWaitGeili - state.waitSpent
	state.mu.Unlock()
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < remaining {
		remaining = time.Until(deadline)
	}
	if remaining < 0 {
		return 0
	}
	return remaining
}
func encryptedRouteSpendWaitGeili(state *openAIEncryptedRouteStateGeili, elapsed time.Duration) {
	state.mu.Lock()
	state.waitSpent += elapsed
	state.mu.Unlock()
}

// Only explicit finite cooldowns are waitable; absent availability never causes
// heuristic polling or bypasses the normal scheduler's gates.
func (s *OpenAIGatewayService) encryptedRouteCooldownUntilGeili(ctx context.Context, a *Account, model string) time.Time {
	now := time.Now()
	until := time.Time{}
	extend := func(candidate time.Time) {
		if candidate.After(now) && candidate.After(until) {
			until = candidate
		}
	}
	for _, t := range []*time.Time{a.RateLimitResetAt, a.OverloadUntil, a.TempUnschedulableUntil} {
		if t != nil {
			extend(*t)
		}
	}
	if d := a.GetModelRateLimitRemainingTimeWithContext(ctx, model); d > 0 {
		extend(time.Now().Add(d))
	}
	// Populate the authoritative transport cache's local deadline using its
	// existing bounded lookup, then inspect exact model-runtime cooldown state.
	s.openAITransportBlockedGeili(ctx, a, false)
	extend(s.transportHealthStateGeili().blockedUntil(a.ID, time.Now()))
	transient := s.getOpenAIAccountModelTransientState()
	if key, ok := openAIAccountModelTransientKey(a.ID, openAIAccountModelTransientModel(canonicalOpenAIAccountSchedulingModel(a, model))); ok {
		transient.mu.Lock()
		entry, found := transient.entries[key]
		transient.mu.Unlock()
		if found && now.Sub(entry.lastFailure) <= openAIModelTransientStreakTTL {
			extend(entry.blockUntil)
		}
	}
	return until
}
func encryptedRouteAccountUnavailableGeili(a *Account) bool {
	if a == nil || !a.IsActive() || !a.Schedulable {
		return true
	}
	if a.AutoPauseOnExpired && a.ExpiresAt != nil && !time.Now().Before(*a.ExpiresAt) {
		return true
	}
	if a.IsAPIKeyOrBedrock() && a.IsQuotaExceeded() {
		return true
	}
	if a.TempUnschedulableUntil != nil && time.Now().Before(*a.TempUnschedulableUntil) {
		reason := strings.ToLower(a.TempUnschedulableReason)
		for _, token := range []string{"401", "403", "unauthorized", "forbidden", "permission", "authentication", "invalid_api_key", "insufficient", "额度不足", "余额不足", "鉴权", "权限"} {
			if strings.Contains(reason, token) {
				return true
			}
		}
	}
	return false
}
func (s *OpenAIGatewayService) selectOpenAIEncryptedRouteGeili(ctx context.Context, groupID *int64, sessionHash, model, platform string, excluded map[int64]struct{}, transport OpenAIUpstreamTransport, capability OpenAIEndpointCapability, images OpenAIImagesCapability, compact, compaction bool) (*AccountSelectionResult, bool, error) {
	state := encryptedRouteStateGeili(ctx)
	if state == nil || !state.protected || state.owner == nil || platform != PlatformOpenAI {
		return nil, false, nil
	}
	groupID = openAIEncryptedTargetGroupGeili(ctx, groupID)
	if groupID == nil || s.accountRepo == nil || s.checkChannelPricingRestriction(ctx, groupID, model) {
		return nil, true, ErrOpenAIEncryptedRouteUnavailableGeili
	}
	if _, excludedOrigin := excluded[state.owner.AccountID]; excludedOrigin {
		return nil, true, ErrOpenAIEncryptedRouteUnavailableGeili
	}
	if sessionHash == "" {
		sessionHash = fmt.Sprint(state.userID)
	}
	scheduler := &defaultOpenAIAccountScheduler{service: s, stats: newOpenAIAccountRuntimeStats()}
	req := OpenAIAccountScheduleRequest{GroupID: groupID, Platform: platform, SessionHash: sessionHash, StickyAccountID: state.owner.AccountID, PreserveStickyBinding: true, DisableStickyEscape: true, RequestedModel: model, ExcludedIDs: excluded, RequiredTransport: transport, RequiredCapability: capability, RequiredImageCapability: images, RequireCompact: compact, RequireCompactionCapability: compaction, RequirePrivacySet: s.openAIGroupRequiresPrivacySet(ctx, groupID)}
	for {
		account, err := s.accountRepo.GetByID(ctx, state.owner.AccountID)
		if err != nil || encryptedRouteAccountUnavailableGeili(account) {
			return nil, true, ErrOpenAIEncryptedRouteUnavailableGeili
		}
		allowed := openAIStickyAccountMatchesGroup(account, groupID)
		if !allowed {
			return nil, true, encryptedRouteErrorGeili(409, "encrypted_session_route_conflict", "原会话线路不在此 Key 的可用分组中")
		}
		// Canonical scheduling already mirrors ordinary passthrough's raw model
		// and native mapping once. Protected compact also keeps this wire rule.
		if err := ValidateOpenAIEncryptedAccountGeili(ctx, account, model); err != nil {
			return nil, true, err
		}
		if compatible, reason := scheduler.isAccountRequestCompatibleReason(ctx, account, req); !compatible && reason != "runtime_blocked" {
			return nil, true, ErrOpenAIEncryptedRouteUnavailableGeili
		}
		if !scheduler.isAccountTransportCompatible(account, transport) {
			return nil, true, ErrOpenAIEncryptedRouteUnavailableGeili
		}
		if until := s.encryptedRouteCooldownUntilGeili(ctx, account, model); until.After(time.Now()) {
			delay := time.Until(until)
			if delay > encryptedRouteWaitRemainingGeili(ctx, state) {
				return nil, true, ErrOpenAIEncryptedRouteUnavailableGeili
			}
			start := time.Now()
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
				encryptedRouteSpendWaitGeili(state, time.Since(start))
				continue
			case <-ctx.Done():
				timer.Stop()
				encryptedRouteSpendWaitGeili(state, time.Since(start))
				return nil, true, ctx.Err()
			}
		}
		selection, _, err := scheduler.selectBySessionHash(ctx, req)
		if err != nil || selection == nil || selection.Account == nil {
			return nil, true, ErrOpenAIEncryptedRouteUnavailableGeili
		}
		if selection.WaitPlan != nil {
			remaining := encryptedRouteWaitRemainingGeili(ctx, state)
			if remaining <= 0 {
				return nil, true, ErrOpenAIEncryptedRouteUnavailableGeili
			}
			if selection.WaitPlan.Timeout <= 0 || selection.WaitPlan.Timeout > remaining {
				selection.WaitPlan.Timeout = remaining
			}
			// Reserve the full returned allowance so retries cannot multiply queue wait;
			// generation keeps the original request context without this admission cap.
			encryptedRouteSpendWaitGeili(state, selection.WaitPlan.Timeout)
		}
		return selection, true, nil
	}
}

func (s *OpenAIGatewayService) ValidateOpenAIEncryptedAccountGeili(ctx context.Context, account *Account, model string) error {
	return ValidateOpenAIEncryptedAccountGeili(ctx, account, model)
}

// WithOpenAIEncryptedActualOutputOriginGeili receives an already-mapped model
// immediately before transport send. Never apply account aliases twice.
func WithOpenAIEncryptedActualOutputOriginGeili(ctx context.Context, groupID *int64, account *Account, upstreamModel string) context.Context {
	groupID = openAIEncryptedTargetGroupGeili(ctx, groupID)
	state := encryptedRouteStateGeili(ctx)
	if state == nil || groupID == nil || account == nil || account.Platform != PlatformOpenAI {
		return ctx
	}
	state.mu.Lock()
	state.origin = &OpenAIEncryptedRouteGeili{GroupID: *groupID, AccountID: account.ID, Model: strings.TrimSpace(upstreamModel), BackendFingerprint: encryptedBackendFingerprintGeili(account)}
	state.mu.Unlock()
	return ctx
}
