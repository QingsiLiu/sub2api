//go:build unit && !integration

package repository

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Real Ent queries and HTTP projections must agree; service-only stubs would
// hide a missing WithEntitlements on the active-subscription query.
func TestSubscriptionDisplayGeiliHTTP(t *testing.T) {
	for _, tc := range []struct {
		name                                                   string
		lotYesterday, parentNil, stack, expiredSibling, legacy bool
		wantDaily, wantLimit                                   float64
	}{
		{name: "current_lot_with_stale_parent", wantDaily: 90.21295472, wantLimit: 90},
		{name: "lot_crossed_midnight", lotYesterday: true, wantLimit: 90},
		{name: "activated_lot_without_parent_window", parentNil: true, wantDaily: 90.21295472, wantLimit: 90},
		{name: "stacked_independent_windows", stack: true, wantDaily: 90.21295472, wantLimit: 180},
		{name: "expired_sibling_excluded", expiredSibling: true, wantDaily: 90.21295472, wantLimit: 90},
		{name: "legacy_keeps_midnight_reset", legacy: true, wantLimit: 900},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			c, lot := auditRepoFixture(t)
			now := time.Now()
			today := timezone.StartOfDay(now)
			yesterday := today.AddDate(0, 0, -1)
			parent, err := c.UserSubscription.Get(ctx, lot.UserSubscriptionID)
			require.NoError(t, err)
			_, err = c.SubscriptionPlan.UpdateOneID(*parent.PlanID).SetDailyLimitUsd(900).SetWeeklyLimitUsd(630).SetMonthlyLimitUsd(2700).Save(ctx)
			require.NoError(t, err)
			update := c.UserSubscription.UpdateOneID(parent.ID).SetStartsAt(yesterday).SetDailyWindowStart(yesterday).SetWeeklyWindowStart(yesterday).SetMonthlyWindowStart(yesterday).SetDailyUsageUsd(90.21295472).SetWeeklyUsageUsd(181.26703904).SetMonthlyUsageUsd(181.26703904)
			if tc.parentNil {
				update.ClearDailyWindowStart().ClearWeeklyWindowStart().ClearMonthlyWindowStart()
			}
			parent, err = update.Save(ctx)
			require.NoError(t, err)
			day := today
			if tc.lotYesterday {
				day = yesterday
			}
			lot, err = c.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetStartsAt(yesterday).SetDailyLimitUsd(90).SetWeeklyLimitUsd(630).SetMonthlyLimitUsd(2700).SetDailyWindowStart(day).SetWeeklyWindowStart(yesterday).SetMonthlyWindowStart(yesterday).SetDailyUsageUsd(90.21295472).SetWeeklyUsageUsd(181.26703904).SetMonthlyUsageUsd(181.26703904).Save(ctx)
			require.NoError(t, err)
			if tc.stack || tc.expiredSibling {
				expiry := parent.ExpiresAt
				if tc.expiredSibling {
					expiry = now.Add(-time.Hour)
				}
				_, err = c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(parent.ID).SetStartsAt(yesterday.AddDate(0, 0, -30)).SetExpiresAt(expiry).SetStatus("active").SetDailyLimitUsd(90).SetWeeklyLimitUsd(630).SetMonthlyLimitUsd(2700).SetDailyWindowStart(yesterday).SetDailyUsageUsd(80).Save(ctx)
				require.NoError(t, err)
			}
			if tc.legacy {
				require.NoError(t, c.UserSubscriptionEntitlement.DeleteOneID(lot.ID).Exec(ctx))
			}
			repo := NewUserSubscriptionRepository(c)
			svc := service.NewSubscriptionService(nil, repo, nil, nil, nil)
			t.Cleanup(svc.Stop)
			h := handler.NewSubscriptionHandler(svc)
			ah := adminhandler.NewSubscriptionHandler(svc)
			for _, endpoint := range []struct {
				path    string
				run     gin.HandlerFunc
				prefix  string
				summary bool
			}{
				{"/subscriptions/active", h.GetActive, "data.0.", false},
				{"/subscriptions", h.List, "data.0.", false},
				{fmt.Sprintf("/admin/subscriptions?user_id=%d", parent.UserID), ah.List, "data.items.0.", false},
				{fmt.Sprintf("/admin/subscriptions?user_id=%d&status=active", parent.UserID), ah.List, "data.items.0.", false},
				{"/subscriptions/summary", h.GetSummary, "data.subscriptions.0.", true},
				{"/subscriptions/progress", h.GetProgress, "data.0.subscription.", false},
			} {
				t.Run(endpoint.path, func(t *testing.T) {
					w := httptest.NewRecorder()
					gc, _ := gin.CreateTestContext(w)
					gc.Request = httptest.NewRequest(http.MethodGet, endpoint.path, nil)
					gc.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: parent.UserID})
					endpoint.run(gc)
					require.Equal(t, http.StatusOK, w.Code, w.Body.String())
					body := w.Body.String()
					usageField := "daily_usage_usd"
					limitField := "quota_summary.daily_limit_usd"
					if endpoint.summary {
						usageField = "daily_used_usd"
						limitField = "daily_limit_usd"
					}
					require.InDelta(t, tc.wantDaily, gjson.Get(body, endpoint.prefix+usageField).Float(), 1e-8, body)
					if !tc.legacy || endpoint.summary {
						require.Equal(t, tc.wantLimit, gjson.Get(body, endpoint.prefix+limitField).Float(), body)
					}
					if !tc.legacy && !endpoint.summary {
						reset, err := time.Parse(time.RFC3339, gjson.Get(body, endpoint.prefix+"quota_summary.daily_reset_at").String())
						require.NoError(t, err, body)
						require.True(t, today.AddDate(0, 0, 1).Equal(reset), body)
					}
					if endpoint.path == "/subscriptions/progress" && !tc.legacy {
						require.True(t, gjson.Get(body, "data.0.progress.daily").IsObject(), body)
						require.InDelta(t, tc.wantDaily, gjson.Get(body, "data.0.progress.daily.used_usd").Float(), 1e-8, body)
						reset, err := time.Parse(time.RFC3339, gjson.Get(body, "data.0.progress.daily.resets_at").String())
						require.NoError(t, err, body)
						require.True(t, today.AddDate(0, 0, 1).Equal(reset), body)
					}
				})
			}
			current, err := repo.GetByID(ctx, parent.ID)
			require.NoError(t, err)
			if !tc.legacy {
				_, err = svc.ValidateAndCheckLimits(current, nil)
				if tc.lotYesterday || tc.stack {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, service.ErrDailyLimitExceeded)
				}
				persisted, err := c.UserSubscriptionEntitlement.Get(ctx, lot.ID)
				require.NoError(t, err)
				require.Equal(t, lot.DailyUsageUsd, persisted.DailyUsageUsd, "reads must not reset quota")
				require.Equal(t, lot.DailyWindowStart, persisted.DailyWindowStart)
			}
			persisted, err := c.UserSubscription.Get(ctx, parent.ID)
			require.NoError(t, err)
			require.Equal(t, parent.DailyUsageUsd, persisted.DailyUsageUsd)
			require.Equal(t, parent.DailyWindowStart, persisted.DailyWindowStart)
		})
	}
}
