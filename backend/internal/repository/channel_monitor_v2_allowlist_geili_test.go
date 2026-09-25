package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestGeiliChannelMonitorV2StrictModels(t *testing.T) {
	cfg := service.ChannelMonitorV2Config{Platforms: []service.ChannelMonitorV2PlatformConfig{
		{Platform: "openai", Enabled: true, Models: []string{" gpt-5 ", "gpt-5", "shared", "", "__other__"}},
		{Platform: "grok", Enabled: true, Models: []string{"grok-4"}},
		{Platform: "anthropic", Enabled: true},
		{Platform: "gemini", Enabled: false, Models: []string{"shared"}},
	}}
	for _, tt := range []struct {
		platform, model string
		filter          service.ChannelMonitorV2Filter
		want            bool
	}{
		{"openai", "gpt-5", service.ChannelMonitorV2Filter{}, true},
		{"openai", " gpt-5 ", service.ChannelMonitorV2Filter{}, true},
		{"openai", "GPT-5", service.ChannelMonitorV2Filter{}, false},
		{"openai", "gpt-5-mini", service.ChannelMonitorV2Filter{}, false},
		{"openai", "bogus", service.ChannelMonitorV2Filter{}, false},
		{"grok", "shared", service.ChannelMonitorV2Filter{}, false},
		{"anthropic", "claude-sonnet", service.ChannelMonitorV2Filter{}, false},
		{"gemini", "shared", service.ChannelMonitorV2Filter{}, false},
		{"missing", "gpt-5", service.ChannelMonitorV2Filter{}, false},
		{"openai", "", service.ChannelMonitorV2Filter{}, false},
		{"openai", "__other__", service.ChannelMonitorV2Filter{}, false},
		{"openai", "gpt-5", service.ChannelMonitorV2Filter{Models: []string{"gpt-5"}}, true},
		{"openai", "gpt-5", service.ChannelMonitorV2Filter{Models: []string{"shared"}}, false},
		{"openai", "bogus", service.ChannelMonitorV2Filter{Models: []string{"bogus"}}, false},
		{"openai", "gpt-5", service.ChannelMonitorV2Filter{Models: []string{"__other__"}}, false},
		{"openai", "gpt-5", service.ChannelMonitorV2Filter{Platforms: []string{"grok"}}, false},
	} {
		t.Run(fmt.Sprintf("%s/%s/%v/%v", tt.platform, tt.model, tt.filter.Platforms, tt.filter.Models), func(t *testing.T) {
			require.Equal(t, tt.want, channelMonitorV2ModelSelected(tt.filter, cfg, tt.platform, tt.model))
		})
	}
	require.Equal(t, []string{"openai", "grok"}, channelMonitorV2EnabledPlatforms(cfg))
	require.Equal(t, []string{"gpt-5", "shared"}, configuredChannelMonitorV2Models(cfg, "openai", service.ChannelMonitorV2Filter{}))
	require.Empty(t, configuredChannelMonitorV2Models(cfg, "anthropic", service.ChannelMonitorV2Filter{Models: []string{"gpt-5"}}))
}

func TestGeiliChannelMonitorV2SQLScopePairsPlatformsAndModels(t *testing.T) {
	cfg := service.ChannelMonitorV2Config{Platforms: []service.ChannelMonitorV2PlatformConfig{
		{Platform: "openai", Enabled: true, Models: []string{"gpt-5", "shared"}},
		{Platform: "grok", Enabled: true, Models: []string{"shared"}},
		{Platform: "anthropic", Enabled: true},
	}, GroupIDs: []int64{1}}
	filter := service.ChannelMonitorV2Filter{Start: time.Unix(1, 0), End: time.Unix(2, 0), Bucket: 5 * time.Minute}
	for _, alias := range []string{"m", "h", "e"} {
		where, args, seconds := channelMonitorV2WhereWithRollup(filter, cfg, alias)
		require.Contains(t, where, fmt.Sprintf("(%s.platform, %s.model) IN (SELECT * FROM unnest($5::text[], $6::text[]))", alias, alias))
		require.Equal(t, pq.Array([]string{"openai", "openai", "grok"}), args[4])
		require.Equal(t, pq.Array([]string{"gpt-5", "shared", "shared"}), args[5])
		require.Contains(t, where, alias+".bucket_seconds = $7")
		require.Equal(t, 300, seconds)
		require.Equal(t, 300, args[6])
	}
	filter.Models = []string{"shared", "bogus"}
	_, args := channelMonitorV2Where(filter, cfg, "m")
	require.Equal(t, pq.Array([]string{"openai", "grok"}), args[4])
	require.Equal(t, pq.Array([]string{"shared", "shared"}), args[5])
	for _, models := range [][]string{{"bogus"}, {"__other__"}} {
		filter.Models = models
		where, args := channelMonitorV2Where(filter, cfg, "m")
		require.Contains(t, where, "AND FALSE")
		require.Len(t, args, 4)
	}
	// Even unusual names stay bound parameters, not interpolated SQL.
	cfg.Platforms[0].Models = []string{"x'); DROP TABLE users; --"}
	where, args := channelMonitorV2Where(service.ChannelMonitorV2Filter{}, cfg, "m")
	require.NotContains(t, where, "DROP TABLE")
	require.Equal(t, pq.Array([]string{"x'); DROP TABLE users; --", "shared"}), args[5])
}

func TestGeiliChannelMonitorV2MatrixSeedsOnlyAllowedModels(t *testing.T) {
	cfg := service.ChannelMonitorV2Config{Platforms: []service.ChannelMonitorV2PlatformConfig{
		{Platform: "openai", Enabled: true, Models: []string{"gpt-5"}},
		{Platform: "grok", Enabled: true},
		{Platform: "gemini", Enabled: false, Models: []string{"gemini-pro"}},
	}, GroupIDs: []int64{1, 2, 3}}
	groups := map[int64]channelMonitorV2GroupInfo{
		1: {name: "allowed", platform: "openai"}, 2: {name: "empty", platform: "grok"}, 3: {name: "disabled", platform: "gemini"},
	}
	for _, groupBy := range []service.ChannelMonitorV2GroupBy{
		service.ChannelMonitorV2GroupByPlatform, service.ChannelMonitorV2GroupByPlatformGroup,
		service.ChannelMonitorV2GroupByPlatformModel, service.ChannelMonitorV2GroupByPlatformGroupModel,
	} {
		t.Run(string(groupBy), func(t *testing.T) {
			accs := seedChannelMonitorV2MatrixAccumulators(service.ChannelMonitorV2Filter{}, cfg, groupBy, groups)
			require.Len(t, accs, 1)
			for key, acc := range accs {
				require.Equal(t, "openai", key.platform)
				health := service.ChannelMonitorV2HealthFor(acc.total.metric(1, true))
				require.Equal(t, "unknown", health.Overall)
				require.Nil(t, health.Score)
			}
			for _, models := range [][]string{{"bogus"}, {"__other__"}} {
				require.Empty(t, seedChannelMonitorV2MatrixAccumulators(service.ChannelMonitorV2Filter{Models: models}, cfg, groupBy, groups))
			}
		})
	}
}
