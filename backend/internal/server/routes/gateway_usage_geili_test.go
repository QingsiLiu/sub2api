package routes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Unimplemented repository methods intentionally panic: a usage read must never
// enter admission, settlement, refunds, route selection or upstream dispatch.
type usageRouteKeysGeili struct {
	service.APIKeyRepository
	keys    map[string]*service.APIKey
	touched []int64
}

func (r *usageRouteKeysGeili) GetByKeyForAuth(_ context.Context, credential string) (*service.APIKey, error) {
	key := r.keys[credential]
	if key == nil {
		return nil, service.ErrAPIKeyNotFound
	}
	copy := *key
	return &copy, nil
}
func (r *usageRouteKeysGeili) UpdateLastUsed(_ context.Context, id int64, _ time.Time) error {
	r.touched = append(r.touched, id)
	return nil
}
func (r *usageRouteKeysGeili) GetRateLimitData(context.Context, int64) (*service.APIKeyRateLimitData, error) {
	now := time.Now()
	return &service.APIKeyRateLimitData{Usage5h: 2, Window5hStart: &now}, nil
}

type usageRouteUsersGeili struct {
	service.UserRepository
	user *service.User
	err  error
}

func (r *usageRouteUsersGeili) GetByID(_ context.Context, id int64) (*service.User, error) {
	if r.err != nil {
		return nil, r.err
	}
	if id != r.user.ID {
		return nil, service.ErrUserNotFound
	}
	copy := *r.user
	return &copy, nil
}
func (*usageRouteUsersGeili) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

type usageRouteSubsGeili struct {
	service.UserSubscriptionRepository
	subs    map[int64]*service.UserSubscription
	lookups []int64
}

func (r *usageRouteSubsGeili) GetByID(_ context.Context, id int64) (*service.UserSubscription, error) {
	r.lookups = append(r.lookups, id)
	sub := r.subs[id]
	if sub == nil {
		return nil, service.ErrSubscriptionNotFound
	}
	copy := *sub
	return &copy, nil
}
func (r *usageRouteSubsGeili) GetActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*service.UserSubscription, error) {
	for id, sub := range r.subs {
		if sub.UserID == userID && sub.GroupID == groupID {
			return r.GetByID(ctx, id)
		}
	}
	return nil, service.ErrSubscriptionNotFound
}

type usageRouteStatsGeili struct {
	service.UsageLogRepository
	t                    *testing.T
	err                  error
	keys                 []int64
	modelStart, modelEnd time.Time
}

func (r *usageRouteStatsGeili) GetAPIKeyDashboardStats(_ context.Context, keyID int64) (*usagestats.UserDashboardStats, error) {
	r.keys = append(r.keys, keyID)
	return &usagestats.UserDashboardStats{TotalRequests: keyID}, r.err
}
func (r *usageRouteStatsGeili) GetUsageTrendWithFilters(_ context.Context, start, end time.Time, granularity string, userID, keyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8) ([]usagestats.TrendDataPoint, error) {
	require.Equal(r.t, "day", granularity)
	require.Equal(r.t, int64(7), userID)
	require.Zero(r.t, accountID)
	require.Zero(r.t, groupID)
	require.Empty(r.t, model)
	require.Nil(r.t, requestType)
	require.Nil(r.t, stream)
	require.Nil(r.t, billingType)
	require.Equal(r.t, 7*24*time.Hour, end.Sub(start))
	r.keys = append(r.keys, keyID)
	return []usagestats.TrendDataPoint{{Date: "2026-11-03", Requests: keyID}}, r.err
}
func (r *usageRouteStatsGeili) GetModelStatsWithFilters(_ context.Context, start, end time.Time, userID, keyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8) ([]usagestats.ModelStat, error) {
	require.Zero(r.t, userID)
	require.Zero(r.t, accountID)
	require.Zero(r.t, groupID)
	require.Nil(r.t, requestType)
	require.Nil(r.t, stream)
	require.Nil(r.t, billingType)
	r.keys = append(r.keys, keyID)
	r.modelStart, r.modelEnd = start, end
	return []usagestats.ModelStat{{Model: fmt.Sprintf("key-%d", keyID), Requests: keyID}}, r.err
}

type usageRouteFixtureGeili struct {
	router *gin.Engine
	keys   *usageRouteKeysGeili
	users  *usageRouteUsersGeili
	subs   *usageRouteSubsGeili
	stats  *usageRouteStatsGeili
}

func newUsageRouteFixtureGeili(t *testing.T) *usageRouteFixtureGeili {
	t.Helper()
	gin.SetMode(gin.TestMode)
	f := &usageRouteFixtureGeili{
		keys:  &usageRouteKeysGeili{keys: map[string]*service.APIKey{}},
		users: &usageRouteUsersGeili{user: &service.User{ID: 7, Status: service.StatusActive, Balance: 17}},
		subs:  &usageRouteSubsGeili{subs: map[int64]*service.UserSubscription{}},
		stats: &usageRouteStatsGeili{t: t},
	}
	cfg := &config.Config{RunMode: config.RunModeStandard, Gateway: config.GatewayConfig{MaxBodySize: 1024 * 1024, TextMaxBodySize: 1024 * 1024}}
	keys := service.NewAPIKeyService(f.keys, f.users, nil, nil, nil, nil, cfg)
	subs := service.NewSubscriptionService(nil, f.subs, nil, nil, cfg)
	t.Cleanup(subs.Stop)
	users := service.NewUserService(f.users, nil, nil, nil)
	usage := service.NewUsageService(f.stats, f.users, nil, nil)
	h := handler.NewGatewayHandler(nil, nil, nil, nil, users, nil, nil, usage, keys, nil, nil, nil, nil, cfg, nil)
	f.router = gin.New()
	RegisterGatewayRoutes(f.router, &handler.Handlers{Gateway: h, OpenAIGateway: &handler.OpenAIGatewayHandler{}}, middleware.NewAPIKeyAuthMiddleware(keys, subs, cfg), keys, subs, nil, nil, nil, cfg)
	return f
}

func (f *usageRouteFixtureGeili) key(id int64, source, mode string) *service.APIKey {
	key := &service.APIKey{ID: id, Key: fmt.Sprintf("usage-key-%d", id), UserID: 7, User: f.users.user, Status: service.StatusAPIKeyActive, BillingSource: source, RoutingMode: mode}
	if mode == service.KeyRoutingComposite {
		key.GroupIDs = []int64{131, 134, 129, 135, 147}
	} else {
		groupID := int64(131)
		key.GroupID = &groupID
		// Deliberately contradict balance settlement to catch group-type inference.
		key.Group = &service.Group{ID: groupID, Name: "Supplier route", Hydrated: true, Status: service.StatusActive, Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription}
	}
	f.keys.keys[key.Key] = key
	return key
}

func (f *usageRouteFixtureGeili) request(t *testing.T, path, credential string, want int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path+"?days=7&timezone=Asia%2FShanghai&start_date=2026-11-01&end_date=2026-11-02", nil)
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	require.Equal(t, want, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body
}

func TestGatewayUsageGeiliSettlementAndKeyIsolation(t *testing.T) {
	for _, path := range []string{"/v1/usage", "/antigravity/v1/usage"} {
		for _, source := range []string{service.BillingSourceBalance, service.BillingSourceSubscription} {
			for _, mode := range []string{service.KeyRoutingSingle, service.KeyRoutingComposite} {
				t.Run(path+"/"+source+"/"+mode, func(t *testing.T) {
					f := newUsageRouteFixtureGeili(t)
					daily := 10.0
					for _, id := range []int64{101, 202} {
						key := f.key(id, source, mode)
						if source == service.BillingSourceSubscription {
							key.SubscriptionID = &key.ID
							f.subs.subs[id] = &service.UserSubscription{ID: id, UserID: 7, Status: service.SubscriptionStatusActive, StartsAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour), Plan: &service.SubscriptionQuotaPlan{Name: fmt.Sprintf("Plan %d", id), DailyLimitUSD: &daily}, DailyUsageUSD: float64(id) / 100}
							// A standard supplier group cannot dictate subscription billing.
							if key.Group != nil {
								key.Group.SubscriptionType = service.SubscriptionTypeStandard
							}
						}
						body := f.request(t, path, key.Key, http.StatusOK)
						require.Equal(t, "unrestricted", body["mode"])
						require.Equal(t, "USD", body["unit"])
						if source == service.BillingSourceBalance {
							require.Equal(t, 17.0, body["remaining"])
							require.Equal(t, "钱包余额", body["planName"])
						} else {
							require.InDelta(t, 10-float64(id)/100, body["remaining"], 1e-10)
							require.Equal(t, fmt.Sprintf("Plan %d", id), body["planName"])
							require.NotContains(t, body, "balance")
						}
						usage := body["usage"].(map[string]any)["total"].(map[string]any)
						require.Equal(t, float64(id), usage["requests"])
						require.Equal(t, float64(id), body["daily_usage"].([]any)[0].(map[string]any)["requests"])
						require.Equal(t, fmt.Sprintf("key-%d", id), body["model_stats"].([]any)[0].(map[string]any)["model"])
					}
					require.Equal(t, []int64{101, 101, 101, 202, 202, 202}, f.stats.keys)
					require.Equal(t, []int64{101, 202}, f.keys.touched)
					require.Equal(t, "2026-11-01", f.stats.modelStart.Format("2006-01-02"))
					require.Equal(t, "2026-11-03", f.stats.modelEnd.Format("2006-01-02"))
					if source == service.BillingSourceSubscription {
						require.Equal(t, []int64{101, 202}, f.subs.lookups)
					} else {
						require.Empty(t, f.subs.lookups)
					}
				})
			}
		}
	}
}

func TestGatewayUsageGeiliAuthAndDegradation(t *testing.T) {
	for _, path := range []string{"/v1/usage", "/antigravity/v1/usage"} {
		for _, tc := range []struct {
			name       string
			change     func(*usageRouteFixtureGeili, *service.APIKey)
			credential string
			want       int
		}{
			{"missing", nil, "", 401},
			{"invalid", nil, "unknown", 401},
			{"disabled-key", func(_ *usageRouteFixtureGeili, k *service.APIKey) { k.Status = "disabled" }, "key", 401},
			{"disabled-user", func(f *usageRouteFixtureGeili, _ *service.APIKey) { f.users.user.Status = "disabled" }, "key", 401},
			{"ip-denied", func(_ *usageRouteFixtureGeili, k *service.APIKey) { k.IPWhitelist = []string{"198.51.100.1"} }, "key", 403},
			{"expired", func(_ *usageRouteFixtureGeili, k *service.APIKey) { k.Status = service.StatusAPIKeyExpired }, "key", 200},
			{"quota-exhausted", func(_ *usageRouteFixtureGeili, k *service.APIKey) { k.Status = service.StatusAPIKeyQuotaExhausted }, "key", 200},
			{"zero-wallet", func(f *usageRouteFixtureGeili, _ *service.APIKey) { f.users.user.Balance = 0 }, "key", 200},
			{"stats-unavailable", func(f *usageRouteFixtureGeili, _ *service.APIKey) { f.stats.err = errors.New("stats unavailable") }, "key", 200},
			{"wallet-unavailable", func(f *usageRouteFixtureGeili, _ *service.APIKey) { f.users.err = errors.New("wallet unavailable") }, "key", 500},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				f := newUsageRouteFixtureGeili(t)
				key := f.key(101, service.BillingSourceBalance, service.KeyRoutingComposite)
				if tc.change != nil {
					tc.change(f, key)
				}
				credential := tc.credential
				if credential == "key" {
					credential = key.Key
				}
				body := f.request(t, path, credential, tc.want)
				if tc.want == 401 || tc.want == 403 {
					require.Empty(t, f.stats.keys)
					require.Empty(t, f.keys.touched)
				}
				if tc.name == "stats-unavailable" {
					require.NotContains(t, body, "usage")
					require.NotContains(t, body, "daily_usage")
					require.NotContains(t, body, "model_stats")
					require.Equal(t, 17.0, body["remaining"])
				}
			})
		}
	}
}

func TestGatewayUsageGeiliMissingSubscriptionNeverFallsBackToWallet(t *testing.T) {
	for _, path := range []string{"/v1/usage", "/antigravity/v1/usage"} {
		for _, state := range []string{"missing", "expired", "foreign"} {
			t.Run(path+"/"+state, func(t *testing.T) {
				f := newUsageRouteFixtureGeili(t)
				key := f.key(101, service.BillingSourceSubscription, service.KeyRoutingComposite)
				key.SubscriptionID = &key.ID
				if state != "missing" {
					sub := &service.UserSubscription{ID: 101, UserID: 7, Status: service.SubscriptionStatusActive, StartsAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour)}
					if state == "expired" {
						sub.ExpiresAt = time.Now().Add(-time.Second)
					} else {
						sub.UserID = 8
					}
					f.subs.subs[101] = sub
				}
				body := f.request(t, path, key.Key, 200)
				require.Equal(t, "订阅额度", body["planName"])
				require.Contains(t, body, "usage")
				require.NotContains(t, body, "balance")
				require.NotContains(t, body, "remaining")
				require.NotContains(t, body, "subscription")
			})
		}
	}
}

func TestGatewayUsageGeiliKeyQuotaAndLegacyCompatibility(t *testing.T) {
	for _, path := range []string{"/v1/usage", "/antigravity/v1/usage"} {
		for _, mode := range []string{"quota", "rate", "legacy"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				f := newUsageRouteFixtureGeili(t)
				key := f.key(101, service.BillingSourceSubscription, service.KeyRoutingComposite)
				switch mode {
				case "quota":
					key.Quota = 8
					key.QuotaUsed = 3
				case "rate":
					key.RateLimit5h = 8
				case "legacy":
					key = f.key(101, "", service.KeyRoutingSingle)
					daily := 10.0
					key.Group.DailyLimitUSD = &daily
					f.subs.subs[101] = &service.UserSubscription{ID: 101, UserID: 7, GroupID: *key.GroupID, Status: service.SubscriptionStatusActive, StartsAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour), DailyUsageUSD: 3}
				}
				body := f.request(t, path, key.Key, 200)
				if mode == "legacy" {
					require.Equal(t, "unrestricted", body["mode"])
					require.Equal(t, 7.0, body["remaining"])
				} else {
					require.Equal(t, "quota_limited", body["mode"])
					if mode == "quota" {
						require.Equal(t, 5.0, body["remaining"])
					} else {
						require.Equal(t, 6.0, body["rate_limits"].([]any)[0].(map[string]any)["remaining"])
					}
				}
			})
		}
	}
}

func TestGatewayUsageGeiliOtherRoutesKeepModelAndGroupGuards(t *testing.T) {
	f := newUsageRouteFixtureGeili(t)
	key := f.key(101, service.BillingSourceBalance, service.KeyRoutingComposite)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/files", ""},
		{http.MethodGet, "/v1/responses", ""},
		{http.MethodPost, "/v1/messages", `{}`},
		{http.MethodPost, "/v1/chat/completions", `{}`},
		{http.MethodPost, "/v1/responses", `{}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+key.Key)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, req)
		require.Equal(t, 400, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "MODEL_REQUIRED")
	}
	require.Empty(t, f.stats.keys)
}

func TestGatewayUsageGeiliQuotaProjectionThroughAuth(t *testing.T) {
	for _, path := range []string{"/v1/usage", "/antigravity/v1/usage"} {
		for _, tc := range []struct {
			name            string
			used, remaining float64
		}{
			{"stacked", 10, 80},
			{"exhausted", 90, 0},
			{"new-beijing-day", 90, 90},
			{"unlimited", 0, -1},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				f := newUsageRouteFixtureGeili(t)
				key := f.key(101, service.BillingSourceSubscription, service.KeyRoutingComposite)
				key.SubscriptionID = &key.ID
				now := time.Now()
				sub := &service.UserSubscription{
					ID: 101, UserID: 7, Status: service.SubscriptionStatusActive,
					StartsAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(48 * time.Hour),
					QuotaUsageDate: geilisub.DayStart(now), DailyUsageUSD: tc.used,
					WeeklyUsageUSD: 10000, MonthlyUsageUSD: 10000,
					Contract: &geilisub.Contract{Mode: geilisub.ContractModeV2, Status: "active", PlanName: "Stacked quota", UnitDailyUSD: 45, Quantity: 2, StartsAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(48 * time.Hour)},
				}
				if tc.name == "new-beijing-day" {
					sub.QuotaUsageDate = sub.QuotaUsageDate.AddDate(0, 0, -1)
				}
				if tc.name == "unlimited" {
					sub.Contract = nil
					sub.Entitlements = []service.SubscriptionEntitlement{{Status: "active", StartsAt: sub.StartsAt, ExpiresAt: sub.ExpiresAt}}
				}
				f.subs.subs[sub.ID] = sub
				body := f.request(t, path, key.Key, 200)
				require.Equal(t, tc.remaining, body["remaining"])
				require.NotContains(t, body, "balance")
				require.Equal(t, tc.used, sub.DailyUsageUSD, "the read must not reset stored ledger evidence")
				require.Equal(t, []int64{101}, f.subs.lookups)
			})
		}
	}
}
