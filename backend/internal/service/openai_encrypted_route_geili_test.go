package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
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
