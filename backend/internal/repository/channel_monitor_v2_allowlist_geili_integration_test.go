//go:build integration

package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Exercise real PostgreSQL queries rather than returning pre-filtered sqlmock
// rows: both sides of the ratio, latency, error ignores and every rollup matter.
func TestGeiliChannelMonitorV2AllowlistPostgres(t *testing.T) {
	ctx := context.Background()
	repo := &channelMonitorV2Repository{db: integrationDB}
	original, err := repo.GetConfig(ctx)
	require.NoError(t, err)
	var usageStart, errorStart, through, successful, backfill sql.NullTime
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT usage_coverage_start,error_coverage_start,data_through,last_successful_at,backfill_cursor FROM channel_monitor_v2_watermarks WHERE id=1`).Scan(&usageStart, &errorStart, &through, &successful, &backfill))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, `UPDATE channel_monitor_v2_watermarks SET usage_coverage_start=$1,error_coverage_start=$2,data_through=$3,last_successful_at=$4,backfill_cursor=$5 WHERE id=1`, usageStart, errorStart, through, successful, backfill)
		require.NoError(t, err)
		current, err := repo.GetConfig(ctx)
		require.NoError(t, err)
		_, err = repo.UpdateConfig(ctx, *original, current.Version)
		require.NoError(t, err)
	})

	groupIDs := []int64{}
	for _, platform := range []string{"openai", "grok", "anthropic", "gemini", "composite"} {
		group := mustCreateGroup(t, integrationEntClient, &service.Group{Name: t.Name() + "-" + platform, Platform: platform, RateMultiplier: 1})
		groupIDs = append(groupIDs, group.ID)
		t.Cleanup(func() { require.NoError(t, integrationEntClient.Group.DeleteOneID(group.ID).Exec(ctx)) })
	}
	user := mustCreateUser(t, integrationEntClient, &service.User{Username: "monitor-allowed"})
	noiseUser := mustCreateUser(t, integrationEntClient, &service.User{Username: "monitor-noise"})
	t.Cleanup(func() {
		require.NoError(t, integrationEntClient.User.DeleteOneID(user.ID).Exec(ctx))
		require.NoError(t, integrationEntClient.User.DeleteOneID(noiseUser.ID).Exec(ctx))
	})
	account := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "monitor-composite", Platform: "openai", Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture-only"}})
	t.Cleanup(func() { require.NoError(t, integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx)) })
	tables := map[string]string{
		"metrics":            "platform,group_id,model,success_requests,error_requests,upstream_affected_requests,upstream_attempt_count,input_tokens,output_tokens,cache_creation_tokens,cache_read_tokens,ttft_sum_ms,ttft_count,duration_sum_ms,duration_count,computed_at",
		"user_metrics":       "platform,group_id,model,user_id,success_requests,error_requests,input_tokens,output_tokens,cache_creation_tokens,cache_read_tokens,ttft_sum_ms,ttft_count,duration_sum_ms,duration_count,computed_at",
		"error_metrics":      "platform,group_id,model,error_category,taxonomy_version,error_requests",
		"latency_histograms": "platform,group_id,model,user_id,metric,upper_bound_ms,sample_count",
	}
	t.Cleanup(func() {
		for table := range tables {
			for _, suffix := range []string{"1m", "rollup"} {
				_, err := integrationDB.ExecContext(ctx, "DELETE FROM channel_monitor_v2_"+table+"_"+suffix+" WHERE group_id=ANY($1)", pq.Array(groupIDs))
				require.NoError(t, err)
			}
		}
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM ops_error_logs WHERE group_id=ANY($1)`, pq.Array(groupIDs))
		require.NoError(t, err)
	})
	start := time.Now().UTC().Truncate(24 * time.Hour).Add(-48 * time.Hour)
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	exec(`UPDATE channel_monitor_v2_watermarks SET usage_coverage_start=$1,error_coverage_start=$1,data_through=$2,last_successful_at=NOW(),backfill_cursor=$3 WHERE id=1`, start, start.Add(24*time.Hour), start.Add(-31*24*time.Hour))

	for _, f := range []struct {
		platform, model                     string
		group, uid, success, failures, ttft int64
	}{
		{"openai", "gpt-5", groupIDs[0], user.ID, 90, 10, 100},
		{"openai", "bogus", groupIDs[0], noiseUser.ID, 700, 900, 999999},
		{"grok", "gpt-5", groupIDs[1], noiseUser.ID, 50, 50, 999999},              // same name, wrong platform
		{"anthropic", "claude-sonnet", groupIDs[2], noiseUser.ID, 50, 50, 999999}, // empty allowlist
		{"gemini", "gemini-pro", groupIDs[3], noiseUser.ID, 50, 50, 999999},       // disabled platform
	} {
		exec(`INSERT INTO channel_monitor_v2_metrics_1m (bucket_start,platform,group_id,model,success_requests,error_requests,input_tokens,output_tokens,cache_read_tokens,ttft_sum_ms,ttft_count,duration_sum_ms,duration_count)
		VALUES ($1,$2,$3,$4,$5::bigint,$6,$5::bigint*10,$5::bigint*2,$5::bigint*10,$5::bigint*$7::bigint,$5::bigint,$5::bigint*1000,$5::bigint)`, start, f.platform, f.group, f.model, f.success, f.failures, f.ttft)
		exec(`INSERT INTO channel_monitor_v2_user_metrics_1m (bucket_start,platform,group_id,model,user_id,success_requests,error_requests,input_tokens,output_tokens,cache_read_tokens,ttft_sum_ms,ttft_count,duration_sum_ms,duration_count)
		VALUES ($1,$2,$3,$4,$5,$6::bigint,$7,$6::bigint*10,$6::bigint*2,$6::bigint*10,$6::bigint*$8::bigint,$6::bigint,$6::bigint*1000,$6::bigint)`, start, f.platform, f.group, f.model, f.uid, f.success, f.failures, f.ttft)
		category := "model_unsupported"
		if f.model == "gpt-5" && f.platform == "openai" {
			category = "timeout"
		}
		exec(`INSERT INTO channel_monitor_v2_error_metrics_1m (bucket_start,platform,group_id,model,error_category,taxonomy_version,error_requests) VALUES ($1,$2,$3,$4,$5,1,$6)`, start, f.platform, f.group, f.model, category, f.failures)
		for _, uid := range []int64{0, f.uid} {
			exec(`INSERT INTO channel_monitor_v2_latency_histograms_1m (bucket_start,platform,group_id,model,user_id,metric,upper_bound_ms,sample_count) VALUES ($1,$2,$3,$4,$5,'ttft',$6,$7),($1,$2,$3,$4,$5,'duration',1000,$7)`, start, f.platform, f.group, f.model, uid, f.ttft, f.success)
		}
	}
	for table, columns := range tables {
		for _, seconds := range []int{300, 3600, 43200, 86400} {
			exec("INSERT INTO channel_monitor_v2_"+table+"_rollup (bucket_start,bucket_seconds,"+columns+") SELECT bucket_start,$1,"+columns+" FROM channel_monitor_v2_"+table+"_1m WHERE group_id=ANY($2)", seconds, pq.Array(groupIDs))
		}
	}
	// More than the 400-sample limit, each with a higher count than the real error.
	// Filtering only after LIMIT would lose the single legitimate timeout sample.
	exec(`INSERT INTO ops_error_logs (created_at,platform,group_id,model,error_phase,error_type,status_code,error_message)
		SELECT $1,'openai',$2,'fake-' || n,'upstream','model_unsupported',404,'unsupported model' FROM generate_series(1,401) n CROSS JOIN generate_series(1,2) copies`, start, groupIDs[0])
	exec(`INSERT INTO ops_error_logs (created_at,platform,group_id,model,requested_model,error_phase,error_type,status_code,error_message)
		VALUES ($1,'openai',$2,'upstream-remap','gpt-5','upstream','timeout',504,'allowed timeout')`, start, groupIDs[0])
	exec(`INSERT INTO ops_error_logs (created_at,platform,group_id,account_id,model,requested_model,error_phase,error_type,status_code,error_message)
		VALUES ($1,'composite',$2,$3,'upstream-remap','gpt-5','upstream','timeout',504,'composite timeout')`, start, groupIDs[4], account.ID)

	cfg := *original
	cfg.Enabled = true
	cfg.GroupIDs = groupIDs
	cfg.Platforms = []service.ChannelMonitorV2PlatformConfig{
		{Platform: "openai", Enabled: true, Models: []string{"gpt-5", "idle"}},
		{Platform: "grok", Enabled: true, Models: []string{"grok-4"}},
		{Platform: "anthropic", Enabled: true, Models: []string{}},
		{Platform: "gemini", Enabled: false, Models: []string{"gemini-pro"}},
	}
	cfg.IgnoredErrorCategories = []string{"model_unsupported"}
	svc := service.NewChannelMonitorV2Service(repo)
	saved, err := svc.UpdateConfig(ctx, cfg, original.Version, user.ID)
	require.NoError(t, err)
	loaded, err := repo.GetConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, saved.Platforms, loaded.Platforms)
	cfg = *loaded
	assertMetrics := func(t *testing.T, m service.ChannelMonitorV2Metric) {
		t.Helper()
		require.Equal(t, int64(100), m.RequestCount)
		require.Equal(t, int64(90), m.SuccessRequests)
		require.Equal(t, int64(10), m.ErrorRequests)
		require.InDelta(t, .9, m.SuccessRate, .000001)
		require.InDelta(t, .1, m.ErrorRate, .000001) // bogus ignored errors must NOT reduce this
		require.Equal(t, int64(1980), m.TokenCount)
		require.Equal(t, int64(90), m.TTFT.SampleCount)
		require.NotNil(t, m.TTFT.P95Ms)
		require.Equal(t, int64(100), *m.TTFT.P95Ms)
	}
	for _, bucket := range []time.Duration{time.Minute, 5 * time.Minute, time.Hour, 12 * time.Hour, 24 * time.Hour} {
		t.Run(bucket.String(), func(t *testing.T) {
			filter := service.ChannelMonitorV2Filter{Start: start, End: start.Add(bucket), Bucket: bucket}
			snap, err := repo.GetSnapshot(ctx, filter, cfg, true)
			require.NoError(t, err)
			assertMetrics(t, snap.Metrics)
			require.Len(t, snap.Trend, 1)
			assertMetrics(t, snap.Trend[0].Metrics)
			models, err := repo.GetModels(ctx, filter, cfg, true)
			require.NoError(t, err)
			require.Len(t, models.Items, 3) // includes allowed models without traffic
			for _, row := range models.Items {
				if row.Model == "gpt-5" {
					assertMetrics(t, row.Metrics)
				} else {
					require.Zero(t, row.Metrics.RequestCount)
					require.Equal(t, "unknown", row.Health.Overall)
					require.Nil(t, row.Health.Score)
				}
			}
			for _, groupBy := range []service.ChannelMonitorV2GroupBy{service.ChannelMonitorV2GroupByPlatform, service.ChannelMonitorV2GroupByPlatformGroup, service.ChannelMonitorV2GroupByPlatformModel, service.ChannelMonitorV2GroupByPlatformGroupModel} {
				matrix, err := repo.GetMatrix(ctx, filter, cfg, groupBy, true)
				require.NoError(t, err)
				var total int64
				for _, row := range matrix.Items {
					require.Contains(t, []string{"openai", "grok"}, row.Platform)
					require.NotEqual(t, "__other__", row.Model)
					total += row.Metrics.RequestCount
					if row.Metrics.RequestCount > 0 {
						assertMetrics(t, row.Metrics)
						require.Len(t, row.Buckets, 1)
						assertMetrics(t, row.Buckets[0].Metrics)
					}
				}
				require.Equal(t, int64(100), total)
			}
			users, err := repo.GetUsers(ctx, filter, cfg, true)
			require.NoError(t, err)
			require.Len(t, users.Items, 1)
			require.Equal(t, user.ID, *users.Items[0].UserID)
			assertMetrics(t, users.Items[0].Metrics)
			errors, err := repo.GetErrors(ctx, filter, cfg, true)
			require.NoError(t, err)
			require.Len(t, errors.Items, 1)
			require.Equal(t, "timeout", errors.Items[0].Category)
			require.Equal(t, int64(10), errors.Items[0].Count)
			require.False(t, errors.Items[0].Ignored)
			require.Len(t, errors.Items[0].Details, 2)
			for _, detail := range errors.Items[0].Details {
				require.Equal(t, "openai", detail.Platform)
				require.Equal(t, "gpt-5", detail.Model)
			}
			dims, err := repo.GetDimensions(ctx, filter, cfg)
			require.NoError(t, err)
			require.Len(t, dims.Platforms, 2)
			require.Len(t, dims.Models, 3)
			for _, model := range dims.Models {
				require.False(t, strings.ContainsRune(model.Value, '\x00'))
				require.Equal(t, model.Value, model.Label)
				require.True(t, channelMonitorV2ModelSelected(filter, cfg, model.Platform, model.Value))
			}
			filtered := filter
			filtered.Models = []string{"gpt-5"}
			selected, err := repo.GetSnapshot(ctx, filtered, cfg, true)
			require.NoError(t, err)
			assertMetrics(t, selected.Metrics)
		})
	}

	filter := service.ChannelMonitorV2Filter{Start: start, End: start.Add(time.Minute), Bucket: time.Minute}
	t.Run("filters cannot expand allowlist or group access", func(t *testing.T) {
		for _, models := range [][]string{{"__other__"}, {"bogus"}} {
			f := filter
			f.Models = models
			snap, err := repo.GetSnapshot(ctx, f, cfg, true)
			require.NoError(t, err)
			require.Zero(t, snap.Metrics.RequestCount)
			require.Equal(t, "unknown", snap.Health.Overall)
			require.Nil(t, snap.Health.Score)
			rows, err := repo.GetModels(ctx, f, cfg, true)
			require.NoError(t, err)
			require.Empty(t, rows.Items)
		}
		f := filter
		f.RestrictGroups = true
		f.AllowedGroupIDs = []int64{groupIDs[1]}
		snap, err := repo.GetSnapshot(ctx, f, cfg, true)
		require.NoError(t, err)
		require.Zero(t, snap.Metrics.RequestCount)
		f.AllowedGroupIDs = []int64{groupIDs[0]}
		snap, err = repo.GetSnapshot(ctx, f, cfg, true)
		require.NoError(t, err)
		assertMetrics(t, snap.Metrics)
		f.GroupIDs = []int64{groupIDs[1]}
		snap, err = repo.GetSnapshot(ctx, f, cfg, true)
		require.NoError(t, err)
		require.Zero(t, snap.Metrics.RequestCount)
	})
	t.Run("listed errors retain category ignore behavior", func(t *testing.T) {
		ignored := cfg
		ignored.IgnoredErrorCategories = []string{"timeout"}
		snap, err := repo.GetSnapshot(ctx, filter, ignored, true)
		require.NoError(t, err)
		require.Zero(t, snap.Metrics.ErrorRate)
		require.InDelta(t, .9, snap.Metrics.SuccessRate, .000001)
		require.Equal(t, int64(100), snap.Metrics.RequestCount)
	})
	t.Run("empty configuration returns no data everywhere", func(t *testing.T) {
		empty := cfg
		empty.Platforms = []service.ChannelMonitorV2PlatformConfig{{Platform: "openai", Enabled: true, Models: []string{}}}
		saved, err := svc.UpdateConfig(ctx, empty, cfg.Version, user.ID)
		require.NoError(t, err)
		cfg.Version = saved.Version
		loaded, err := repo.GetConfig(ctx)
		require.NoError(t, err)
		require.Empty(t, loaded.Platforms[0].Models)
		snap, err := repo.GetSnapshot(ctx, filter, *loaded, true)
		require.NoError(t, err)
		require.Zero(t, snap.Metrics.RequestCount)
		require.Empty(t, snap.Trend)
		require.Equal(t, "unknown", snap.Health.Overall)
		require.Nil(t, snap.Health.Score)
		dims, err := repo.GetDimensions(ctx, filter, *loaded)
		require.NoError(t, err)
		require.Empty(t, dims.Platforms)
		require.Empty(t, dims.Groups)
		require.Empty(t, dims.Models)
		models, err := repo.GetModels(ctx, filter, *loaded, true)
		require.NoError(t, err)
		require.Empty(t, models.Items)
		for _, by := range []service.ChannelMonitorV2GroupBy{service.ChannelMonitorV2GroupByPlatform, service.ChannelMonitorV2GroupByPlatformGroup, service.ChannelMonitorV2GroupByPlatformModel, service.ChannelMonitorV2GroupByPlatformGroupModel} {
			matrix, err := repo.GetMatrix(ctx, filter, *loaded, by, true)
			require.NoError(t, err)
			require.Empty(t, matrix.Items)
		}
		users, err := repo.GetUsers(ctx, filter, *loaded, true)
		require.NoError(t, err)
		require.Empty(t, users.Items)
		errors, err := repo.GetErrors(ctx, filter, *loaded, true)
		require.NoError(t, err)
		require.Empty(t, errors.Items)
	})
	t.Run("changed allowlist reuses retained historical facts", func(t *testing.T) {
		cfg.Platforms = []service.ChannelMonitorV2PlatformConfig{{Platform: "openai", Enabled: true, Models: []string{"gpt-5", "bogus"}}}
		saved, err := svc.UpdateConfig(ctx, cfg, cfg.Version, user.ID)
		require.NoError(t, err)
		snap, err := repo.GetSnapshot(ctx, filter, *saved, true)
		require.NoError(t, err)
		require.Equal(t, int64(1700), snap.Metrics.RequestCount)
		require.Equal(t, int64(790), snap.Metrics.SuccessRequests)
		require.Equal(t, int64(910), snap.Metrics.ErrorRequests)
		var logs int64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM ops_error_logs WHERE group_id=ANY($1)`, pq.Array(groupIDs)).Scan(&logs))
		require.Equal(t, int64(804), logs, "raw error logs remain intact")
	})
}
