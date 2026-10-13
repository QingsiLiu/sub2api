package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

type encryptedRouteCacheFixtureGeili struct {
	stubGatewayCache
	routes        map[int64]map[string]OpenAIEncryptedRouteGeili
	readErr       error
	reads, writes int
}

func (c *encryptedRouteCacheFixtureGeili) GetOpenAIEncryptedRoutesGeili(_ context.Context, user int64, digests []string) (map[string]OpenAIEncryptedRouteGeili, error) {
	c.reads++
	if c.readErr != nil {
		return nil, c.readErr
	}
	result := map[string]OpenAIEncryptedRouteGeili{}
	for _, d := range digests {
		if r, ok := c.routes[user][d]; ok {
			result[d] = r
		}
	}
	return result, nil
}
func (c *encryptedRouteCacheFixtureGeili) SetOpenAIEncryptedRoutesGeili(_ context.Context, user int64, records []OpenAIEncryptedRouteGeili) error {
	c.writes++
	if c.routes == nil {
		c.routes = map[int64]map[string]OpenAIEncryptedRouteGeili{}
	}
	if c.routes[user] == nil {
		c.routes[user] = map[string]OpenAIEncryptedRouteGeili{}
	}
	for _, r := range records {
		c.routes[user][r.Digest] = r
	}
	return nil
}

const encryptedToolHistoryFixtureGeili = `{"model":"gpt-6-astra","input":[{"type":"function_call","call_id":"call_test","name":"read_file","arguments":"{\"nonce\":9007199254740993}"},{"type":"function_call_output","call_id":"call_test","output":[{"type":"input_text","text":"retained visible result"},{"type":"encrypted_content","encrypted_content":"enc_opaque_fixture"}]},{"type":"message","role":"user","content":"continue"}]}`

func encryptedPolicyCtxGeili(user int64) context.Context {
	return WithOpenAIEncryptedRoutePolicyGeili(context.Background(), OpenAIEncryptedRouteSettingsGeili{Mode: "protect", ProtectedUserIDs: []int64{user}, ReasoningRecoveryEnabled: true})
}
func TestOpenAIEncryptedRouteGeiliRecordedItemsSurviveKeyRotationAndFailedTerminal(t *testing.T) {
	svc, account := encryptedRecoveryFixtureGeili(&httpUpstreamRecorder{})
	cache := &encryptedRouteCacheFixtureGeili{}
	svc.cache = cache
	group := int64(131)
	key := &APIKey{ID: 10, UserID: 3885}
	ctx, owner, err := svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3885), key, []byte(`{"input":"hello"}`))
	require.NoError(t, err)
	require.Nil(t, owner)
	ctx = WithOpenAIEncryptedOutputOriginGeili(ctx, &group, account, "gpt-6-astra")
	ObserveOpenAIEncryptedOutputGeili(ctx, []byte(`{"type":"response.output_item.done","item":{"type":"agent_message","content":[{"type":"encrypted_content","encrypted_content":"enc_opaque_fixture"}]}}`))
	ObserveOpenAIEncryptedOutputGeili(ctx, []byte(`{"type":"response.failed","response":{"output":[]}}`))
	require.Equal(t, 1, cache.writes)
	nextKey := &APIKey{ID: 11, UserID: 3885}
	ctx, owner, err = svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3885), nextKey, []byte(encryptedToolHistoryFixtureGeili))
	require.NoError(t, err)
	require.NotNil(t, owner)
	require.Equal(t, account.ID, owner.AccountID)
	require.True(t, OpenAIEncryptedRoutePinnedGeili(ctx))
	require.NoError(t, ValidateOpenAIEncryptedAccountGeili(ctx, account, "gpt-6-astra"))
	wrong := *account
	wrong.ID++
	require.Error(t, ValidateOpenAIEncryptedAccountGeili(ctx, &wrong, "gpt-6-astra"))
	require.True(t, ClaimOpenAIEncryptedRecoveryGeili(ctx))
	require.False(t, ClaimOpenAIEncryptedRecoveryGeili(ctx))
	_, owner, err = svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3999), &APIKey{ID: 12, UserID: 3999}, []byte(encryptedToolHistoryFixtureGeili))
	require.NoError(t, err)
	require.Nil(t, owner)
}
func TestOpenAIEncryptedRouteGeiliDefaultOffAndUnknownAreCompatible(t *testing.T) {
	svc, _ := encryptedRecoveryFixtureGeili(&httpUpstreamRecorder{})
	cache := &encryptedRouteCacheFixtureGeili{readErr: errors.New("offline")}
	svc.cache = cache
	key := &APIKey{ID: 1, UserID: 3885}
	ctx, owner, err := svc.PrepareOpenAIEncryptedTurnGeili(context.Background(), key, []byte(encryptedToolHistoryFixtureGeili))
	require.NoError(t, err)
	require.Nil(t, owner)
	require.False(t, OpenAIEncryptedRouteProtectedGeili(ctx))
	require.Zero(t, cache.reads)
	ctx, owner, err = svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3885), key, []byte(encryptedToolHistoryFixtureGeili))
	require.NoError(t, err)
	require.Nil(t, owner)
	require.True(t, OpenAIEncryptedRouteProtectedGeili(ctx))
	require.False(t, OpenAIEncryptedRoutePinnedGeili(ctx))
	require.False(t, OpenAIEncryptedRouteRecoveryEnabledGeili(ctx))
	require.False(t, ClaimOpenAIEncryptedRecoveryGeili(ctx))
}

func TestEncryptedReasoningRouteReleaseGeiliPreservesReadableHistoryAndClaimsOnce(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","reasoning":{"effort":"max"},"input":[{"type":"reasoning","id":"rs_old","encrypted_content":"old","summary":[{"type":"summary_text","text":"keep this progress"}]},{"role":"user","content":"continue","nonce":9007199254740993}]}`)
	state := &openAIEncryptedRouteStateGeili{protected: true, recovery: true, owner: &OpenAIEncryptedRouteGeili{AccountID: 1}}
	ctx := context.WithValue(context.Background(), openAIEncryptedRouteContextKeyGeili{}, state)
	next, ok := ReleaseOpenAIEncryptedReasoningRouteGeili(ctx, body)
	require.True(t, ok)
	require.Contains(t, string(next), "keep this progress")
	require.Contains(t, string(next), "9007199254740993")
	require.Contains(t, string(next), `"effort":"max"`)
	require.Contains(t, string(body), `"encrypted_content":"old"`)
	require.False(t, OpenAIEncryptedRoutePinnedGeili(ctx))
	_, ok = ReleaseOpenAIEncryptedReasoningRouteGeili(ctx, body)
	require.False(t, ok)
	state.owner = &OpenAIEncryptedRouteGeili{AccountID: 1}
	state.claimed = false
	_, ok = ReleaseOpenAIEncryptedReasoningRouteGeili(ctx, []byte(encryptedToolHistoryFixtureGeili))
	require.False(t, ok)
	require.True(t, OpenAIEncryptedRoutePinnedGeili(ctx))
}
func TestOpenAIEncryptedRouteGeiliConflictingOriginsCannotRoute(t *testing.T) {
	svc, account := encryptedRecoveryFixtureGeili(&httpUpstreamRecorder{})
	digests := encryptedDigestsGeili([]byte(`{"input":[{"type":"reasoning","encrypted_content":"a"},{"type":"reasoning","encrypted_content":"b"}]}`))
	cache := &encryptedRouteCacheFixtureGeili{routes: map[int64]map[string]OpenAIEncryptedRouteGeili{3885: {digests[0]: {Digest: digests[0], AccountID: account.ID, GroupID: 131}, digests[1]: {Digest: digests[1], AccountID: account.ID + 1, GroupID: 128}}}}
	svc.cache = cache
	_, _, err := svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3885), &APIKey{UserID: 3885}, []byte(`{"input":[{"type":"reasoning","encrypted_content":"a"},{"type":"reasoning","encrypted_content":"b"}]}`))
	var typed *OpenAIEncryptedRouteErrorGeili
	require.ErrorAs(t, err, &typed)
	require.Equal(t, 409, typed.Status)
}
func TestOpenAIEncryptedRouteGeiliFilteringPreservesAuthorizedPricingGroup(t *testing.T) {
	az, official, other := int64(128), int64(131), int64(140)
	candidates := []CompositeRouteDecision{{TargetPlatform: PlatformOpenAI, TargetGroupID: &az}, {TargetPlatform: PlatformOpenAI, TargetGroupID: &official}}
	owner := &OpenAIEncryptedRouteGeili{GroupID: official, AccountID: 1, CompatibleGroupIDs: []int64{official, az}}
	selected := FilterOpenAIEncryptedRoutesGeili(candidates, owner)
	require.Len(t, selected, 1)
	require.Equal(t, official, *selected[0].TargetGroupID)
	selected = FilterOpenAIEncryptedRoutesGeili(candidates[:1], owner)
	require.Equal(t, az, *selected[0].TargetGroupID)
	require.Empty(t, FilterOpenAIEncryptedRoutesGeili([]CompositeRouteDecision{{TargetPlatform: PlatformOpenAI, TargetGroupID: &other}}, owner))
	owner.CompatibleGroupIDs = []int64{az}
	selected = FilterOpenAIEncryptedRoutesGeili(candidates, owner)
	require.Equal(t, az, *selected[0].TargetGroupID, "revoked historical membership must not mask an authorized current group")
}
func TestOpenAIEncryptedRouteGeiliNonstreamCipherAndIncompleteItem(t *testing.T) {
	svc, account := encryptedRecoveryFixtureGeili(&httpUpstreamRecorder{})
	cache := &encryptedRouteCacheFixtureGeili{}
	svc.cache = cache
	group := int64(131)
	ctx, _, err := svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3885), &APIKey{UserID: 3885}, []byte(`{"input":"hello"}`))
	require.NoError(t, err)
	ctx = WithOpenAIEncryptedOutputOriginGeili(ctx, &group, account, "gpt-6-astra")
	ObserveOpenAIEncryptedOutputGeili(ctx, []byte(`{"type":"response.output_item.added","item":{"type":"reasoning","encrypted_content":"unfinished"}}`))
	require.Zero(t, cache.writes)
	ObserveOpenAIEncryptedOutputGeili(ctx, []byte(`{"object":"response","status":"completed","output":[{"type":"reasoning","encrypted_content":"complete"}]}`))
	require.Equal(t, 1, cache.writes)
	r := cache.routes[3885][openAIEncryptedContentDigest("complete")]
	require.WithinDuration(t, time.Now(), r.GeneratedAt, time.Second)
}

type trackedEncryptedLookupFailureGeili struct {
	tracked bool
	phase   string
}

func (e trackedEncryptedLookupFailureGeili) Error() string {
	return "private database error must not be exposed"
}
func (e trackedEncryptedLookupFailureGeili) Unwrap() error { return context.DeadlineExceeded }
func (e trackedEncryptedLookupFailureGeili) KnownOpenAIEncryptedSourceHintGeili() bool {
	return e.tracked
}
func (e trackedEncryptedLookupFailureGeili) EncryptedRouteLookupPhaseGeili() string { return e.phase }
func TestEncryptedTrackedSourceVerificationFailureGeiliStopsOnlyEligibleProtect(t *testing.T) {
	for _, tc := range []struct {
		name, mode                 string
		eligible, tracked, blocked bool
	}{
		{"protected tracked", "protect", true, true, true},
		{"protected unknown", "protect", true, false, false},
		{"other user", "protect", false, true, false},
		{"observe", "observe", true, true, false},
		{"off", "off", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := encryptedRecoveryFixtureGeili(&httpUpstreamRecorder{})
			cache := &encryptedRouteCacheFixtureGeili{readErr: fmt.Errorf("wrapped: %w", trackedEncryptedLookupFailureGeili{tracked: tc.tracked, phase: "query"})}
			svc.cache = cache
			user := int64(3885)
			if !tc.eligible {
				user++
			}
			ctx := WithOpenAIEncryptedRoutePolicyGeili(context.Background(), OpenAIEncryptedRouteSettingsGeili{Mode: tc.mode, ProtectedUserIDs: []int64{3885}, ReasoningRecoveryEnabled: true})
			body := []byte(encryptedToolHistoryFixtureGeili)
			original := append([]byte(nil), body...)
			ctx, owner, err := svc.PrepareOpenAIEncryptedTurnGeili(ctx, &APIKey{UserID: user}, body)
			if tc.blocked {
				var failure *OpenAIEncryptedRouteErrorGeili
				require.ErrorAs(t, err, &failure)
				require.Equal(t, 503, failure.Status)
				require.Equal(t, "encrypted_session_verification_unavailable", failure.Code)
				require.NotContains(t, failure.Message, "private")
			} else {
				require.NoError(t, err)
			}
			require.Nil(t, owner)
			require.False(t, OpenAIEncryptedRoutePinnedGeili(ctx))
			require.False(t, OpenAIEncryptedRouteRecoveryEnabledGeili(ctx))
			require.False(t, ClaimOpenAIEncryptedRecoveryGeili(ctx))
			recovered, ok := ReleaseOpenAIEncryptedReasoningRouteGeili(ctx, body)
			require.False(t, ok)
			require.Equal(t, original, recovered)
			require.Equal(t, original, body)
			require.Zero(t, cache.writes)
		})
	}
}
func TestEncryptedLookupPhaseGeiliWhitelist(t *testing.T) {
	for _, phase := range []string{"query", "rows", "close"} {
		require.Equal(t, phase, encryptedRouteLookupPhaseGeili(fmt.Errorf("wrapped: %w", trackedEncryptedLookupFailureGeili{phase: phase})))
	}
	require.Equal(t, "unknown", encryptedRouteLookupPhaseGeili(trackedEncryptedLookupFailureGeili{phase: "privateDSN"}))
	require.Equal(t, "unknown", encryptedRouteLookupPhaseGeili(errors.New("private")))
}

func TestEncryptedTrackedVerificationWSFirstFrameGeiliRejectsBeforeDial(t *testing.T) {
	upstream := newStagedPassthroughConn()
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	dialer := &encryptedWSSequenceDialerGeili{conns: []*stagedPassthroughConn{upstream}}
	svc.openaiWSPassthroughDialer = dialer
	svc.cache = &encryptedRouteCacheFixtureGeili{readErr: trackedEncryptedLookupFailureGeili{tracked: true, phase: "query"}}
	group := int64(131)
	var serverContext *gin.Context
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, encryptedPolicyCtxGeili(3885), svc, passthroughLifecycleAccount(), func(c *gin.Context) *OpenAIWSIngressHooks {
		serverContext = c
		c.Set("api_key", &APIKey{ID: 4987, UserID: 3885, GroupID: &group})
		return nil
	})
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6-astra","input":[{"type":"reasoning","encrypted_content":"tracked-synthetic","summary":[]}]}`)))
	_, body, err := client.Read(ctx)
	require.NoError(t, err)
	require.Contains(t, string(body), "encrypted_session_verification_unavailable")
	require.NotContains(t, string(body), "private")
	require.NotContains(t, string(body), "tracked-synthetic")
	require.Zero(t, dialer.calls.Load())
	select {
	case <-serverErr:
		streamErr, ok := GetOpsStreamError(serverContext)
		require.True(t, ok)
		require.Equal(t, 503, streamErr.IntendedStatus)
		require.Equal(t, "encrypted_session_verification_unavailable", streamErr.Code)
		require.True(t, streamErr.CountTowardsSLA)
	case <-ctx.Done():
		t.Fatal("verification failure did not finish WS turn")
	}
	select {
	case <-upstream.writes:
		t.Fatal("unverified source dispatched upstream")
	default:
	}
}

type retryEncryptedLookupCacheGeili struct {
	stubGatewayCache
	calls                          int
	tracked, persistent, immediate bool
	sources                        map[string]OpenAIEncryptedRouteGeili
	budgets                        []time.Duration
}

func (c *retryEncryptedLookupCacheGeili) GetOpenAIEncryptedRoutesGeili(ctx context.Context, _ int64, _ []string) (map[string]OpenAIEncryptedRouteGeili, error) {
	c.calls++
	deadline, _ := ctx.Deadline()
	c.budgets = append(c.budgets, time.Until(deadline))
	if c.calls == 1 || c.persistent {
		if !c.immediate {
			<-ctx.Done()
		}
		return nil, trackedEncryptedLookupFailureGeili{tracked: c.tracked, phase: "rows"}
	}
	return c.sources, nil
}
func (*retryEncryptedLookupCacheGeili) SetOpenAIEncryptedRoutesGeili(context.Context, int64, []OpenAIEncryptedRouteGeili) error {
	return nil
}
func TestEncryptedKnownMetadataTimeoutRetryGeiliUsesFreshAuthority(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		conflict, missing, persistent bool
	}{{"fresh source", false, false, false}, {"fresh conflict", true, false, false}, {"fresh missing", false, true, false}, {"persistent timeout", false, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			svc, account := encryptedRecoveryFixtureGeili(&httpUpstreamRecorder{})
			body := []byte(encryptedToolHistoryFixtureGeili)
			original := append([]byte(nil), body...)
			digest := encryptedDigestsGeili(body)[0]
			source := OpenAIEncryptedRouteGeili{Digest: digest, AccountID: account.ID, GroupID: 131, Model: "gpt-6-astra", BackendFingerprint: encryptedBackendFingerprintGeili(account), Conflict: tc.conflict}
			cache := &retryEncryptedLookupCacheGeili{tracked: true, persistent: tc.persistent, sources: map[string]OpenAIEncryptedRouteGeili{digest: source}}
			if tc.missing {
				cache.sources = map[string]OpenAIEncryptedRouteGeili{}
			}
			svc.cache = cache
			start := time.Now()
			ctx, owner, err := svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3885), &APIKey{UserID: 3885}, body)
			require.Equal(t, 2, cache.calls)
			require.Less(t, time.Since(start), 350*time.Millisecond)
			require.InDelta(t, 50*time.Millisecond, cache.budgets[0], float64(10*time.Millisecond))
			require.LessOrEqual(t, cache.budgets[1], 210*time.Millisecond)
			if tc.persistent || tc.conflict || tc.missing {
				var failure *OpenAIEncryptedRouteErrorGeili
				require.ErrorAs(t, err, &failure)
				if tc.persistent || tc.missing {
					require.Equal(t, 503, failure.Status)
					require.Nil(t, owner)
					require.False(t, OpenAIEncryptedRouteRecoveryEnabledGeili(ctx))
					require.False(t, ClaimOpenAIEncryptedRecoveryGeili(ctx))
				} else {
					require.Equal(t, 409, failure.Status)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, owner)
				require.Equal(t, account.ID, owner.AccountID)
				require.True(t, OpenAIEncryptedRoutePinnedGeili(ctx))
			}
			require.Equal(t, original, body)
		})
	}
}
func TestEncryptedMetadataTimeoutRetryGeiliEligibilityAndParentBudget(t *testing.T) {
	for _, tc := range []struct {
		name, mode                    string
		tracked, otherUser, immediate bool
		parent                        time.Duration
		cancel                        bool
		calls                         int
	}{
		{name: "unknown", mode: "protect", calls: 1},
		{name: "observe", mode: "observe", tracked: true, calls: 1},
		{name: "off", mode: "off", tracked: true, calls: 0},
		{name: "noneligible", mode: "protect", tracked: true, otherUser: true, calls: 1},
		{name: "nonexpired failure", mode: "protect", tracked: true, immediate: true, calls: 1},
		{name: "parent deadline", mode: "protect", tracked: true, parent: 20 * time.Millisecond, calls: 1},
		{name: "parent canceled", mode: "protect", tracked: true, cancel: true, calls: 1},
		{name: "short parent bounds retry", mode: "protect", tracked: true, parent: 100 * time.Millisecond, calls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := encryptedRecoveryFixtureGeili(&httpUpstreamRecorder{})
			cache := &retryEncryptedLookupCacheGeili{tracked: tc.tracked, persistent: true, immediate: tc.immediate}
			svc.cache = cache
			base := context.Background()
			var cancel context.CancelFunc
			if tc.parent > 0 {
				base, cancel = context.WithTimeout(base, tc.parent)
				defer cancel()
			}
			if tc.cancel {
				base, cancel = context.WithCancel(base)
				cancel()
			}
			base = WithOpenAIEncryptedRoutePolicyGeili(base, OpenAIEncryptedRouteSettingsGeili{Mode: tc.mode, ProtectedUserIDs: []int64{3885}, ReasoningRecoveryEnabled: true})
			user := int64(3885)
			if tc.otherUser {
				user++
			}
			start := time.Now()
			ctx, owner, err := svc.PrepareOpenAIEncryptedTurnGeili(base, &APIKey{UserID: user}, []byte(encryptedToolHistoryFixtureGeili))
			require.Equal(t, tc.calls, cache.calls)
			require.Nil(t, owner)
			require.False(t, OpenAIEncryptedRouteRecoveryEnabledGeili(ctx))
			if tc.mode == "protect" && tc.tracked && !tc.otherUser {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Less(t, time.Since(start), 200*time.Millisecond)
		})
	}
}

type prehintEncryptedCacheGeili struct {
	stubGatewayCache
	present                 bool
	hintErr                 error
	hintDelay, queryDelay   time.Duration
	hintCalls, getCalls     int
	hintBudget, queryBudget time.Duration
	sources                 map[string]OpenAIEncryptedRouteGeili
}

func (c *prehintEncryptedCacheGeili) HasOpenAIEncryptedSourceHintGeili(ctx context.Context, _ int64, _ []string) (bool, error) {
	c.hintCalls++
	deadline, _ := ctx.Deadline()
	c.hintBudget = time.Until(deadline)
	timer := time.NewTimer(c.hintDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return c.present, c.hintErr
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
func (c *prehintEncryptedCacheGeili) GetOpenAIEncryptedRoutesGeili(ctx context.Context, _ int64, _ []string) (map[string]OpenAIEncryptedRouteGeili, error) {
	c.getCalls++
	deadline, _ := ctx.Deadline()
	c.queryBudget = time.Until(deadline)
	timer := time.NewTimer(c.queryDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return c.sources, nil
	case <-ctx.Done():
		return nil, trackedEncryptedLookupFailureGeili{tracked: c.present, phase: "rows"}
	}
}
func (*prehintEncryptedCacheGeili) SetOpenAIEncryptedRoutesGeili(context.Context, int64, []OpenAIEncryptedRouteGeili) error {
	return nil
}
func TestEncryptedPrehintGeiliKnownReadUsesSingleFullWindow(t *testing.T) {
	for _, tc := range []struct {
		name              string
		delay             time.Duration
		missing, conflict bool
		parent            time.Duration
	}{
		{name: "120ms fresh source", delay: 120 * time.Millisecond},
		{name: "persistent timeout", delay: time.Second},
		{name: "empty authority", delay: 120 * time.Millisecond, missing: true},
		{name: "fresh conflict", delay: 120 * time.Millisecond, conflict: true},
		{name: "parent clamp", delay: time.Second, parent: 100 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, account := encryptedRecoveryFixtureGeili(&httpUpstreamRecorder{})
			body := []byte(encryptedToolHistoryFixtureGeili)
			original := append([]byte(nil), body...)
			digest := encryptedDigestsGeili(body)[0]
			cache := &prehintEncryptedCacheGeili{present: true, queryDelay: tc.delay, sources: map[string]OpenAIEncryptedRouteGeili{digest: {Digest: digest, GroupID: 131, AccountID: account.ID, Model: "gpt-6-astra", BackendFingerprint: encryptedBackendFingerprintGeili(account), Conflict: tc.conflict}}}
			if tc.missing {
				cache.sources = map[string]OpenAIEncryptedRouteGeili{}
			}
			svc.cache = cache
			base := encryptedPolicyCtxGeili(3885)
			if tc.parent > 0 {
				var cancel context.CancelFunc
				base, cancel = context.WithTimeout(base, tc.parent)
				defer cancel()
			}
			start := time.Now()
			ctx, owner, err := svc.PrepareOpenAIEncryptedTurnGeili(base, &APIKey{UserID: 3885}, body)
			require.Equal(t, 1, cache.hintCalls)
			require.Equal(t, 1, cache.getCalls)
			require.LessOrEqual(t, cache.hintBudget, 10*time.Millisecond)
			require.LessOrEqual(t, cache.queryBudget, 250*time.Millisecond)
			require.Less(t, time.Since(start), 350*time.Millisecond)
			if tc.parent > 0 {
				require.LessOrEqual(t, cache.queryBudget, tc.parent)
				require.Less(t, time.Since(start), 200*time.Millisecond)
			}
			if tc.delay >= time.Second || tc.missing || tc.conflict {
				var failure *OpenAIEncryptedRouteErrorGeili
				require.ErrorAs(t, err, &failure)
				if tc.conflict {
					require.Equal(t, 409, failure.Status)
				} else {
					require.Equal(t, 503, failure.Status)
					require.Nil(t, owner)
					require.False(t, OpenAIEncryptedRouteRecoveryEnabledGeili(ctx))
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, owner)
				require.Equal(t, account.ID, owner.AccountID)
				require.True(t, OpenAIEncryptedRoutePinnedGeili(ctx))
			}
			require.Equal(t, original, body)
		})
	}
}
func TestEncryptedPrehintGeiliUnknownAndOtherModesKeepFirst50(t *testing.T) {
	for _, tc := range []struct {
		name, mode             string
		otherUser, hintFailure bool
		hintCalls, getCalls    int
	}{
		{name: "unknown", mode: "protect", hintCalls: 1, getCalls: 1},
		{name: "hint read failure", mode: "protect", hintFailure: true, hintCalls: 1, getCalls: 1},
		{name: "observe", mode: "observe", getCalls: 1},
		{name: "noneligible", mode: "protect", otherUser: true, getCalls: 1},
		{name: "off", mode: "off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := encryptedRecoveryFixtureGeili(&httpUpstreamRecorder{})
			cache := &prehintEncryptedCacheGeili{queryDelay: 120 * time.Millisecond}
			if tc.hintFailure {
				cache.hintDelay = time.Second
			}
			svc.cache = cache
			base := WithOpenAIEncryptedRoutePolicyGeili(context.Background(), OpenAIEncryptedRouteSettingsGeili{Mode: tc.mode, ProtectedUserIDs: []int64{3885}})
			user := int64(3885)
			if tc.otherUser {
				user++
			}
			start := time.Now()
			_, owner, err := svc.PrepareOpenAIEncryptedTurnGeili(base, &APIKey{UserID: user}, []byte(encryptedToolHistoryFixtureGeili))
			require.NoError(t, err)
			require.Nil(t, owner)
			require.Equal(t, tc.hintCalls, cache.hintCalls)
			require.Equal(t, tc.getCalls, cache.getCalls)
			require.Less(t, time.Since(start), 100*time.Millisecond)
			if tc.getCalls > 0 {
				require.LessOrEqual(t, cache.queryBudget, 50*time.Millisecond)
			}
		})
	}
}

func TestEncryptedPrehintGeiliFailureRetainsKnownMetadataFallback(t *testing.T) {
	svc, account := encryptedRecoveryFixtureGeili(&httpUpstreamRecorder{})
	body := []byte(encryptedToolHistoryFixtureGeili)
	digest := encryptedDigestsGeili(body)[0]
	cache := &prehintEncryptedCacheGeili{present: true, hintErr: errors.New("controlled hint unavailable"), queryDelay: 120 * time.Millisecond, sources: map[string]OpenAIEncryptedRouteGeili{digest: {Digest: digest, GroupID: 131, AccountID: account.ID, Model: "gpt-6-astra", BackendFingerprint: encryptedBackendFingerprintGeili(account)}}}
	svc.cache = cache
	ctx, owner, err := svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3885), &APIKey{UserID: 3885}, body)
	require.NoError(t, err)
	require.Equal(t, 1, cache.hintCalls)
	require.Equal(t, 2, cache.getCalls, "fallback applies only after first Get confirms prior tracking and actually times out")
	require.NotNil(t, owner)
	require.Equal(t, account.ID, owner.AccountID)
	require.True(t, OpenAIEncryptedRoutePinnedGeili(ctx))
}
