package repository

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// geiliChannelMonitorV2Models makes the configured names a strict, platform-scoped
// allowlist. Empty/disabled platforms deny all; request filters can only narrow it.
func geiliChannelMonitorV2Models(cfg service.ChannelMonitorV2Config, platform string, filter service.ChannelMonitorV2Filter) []string {
	models := []string{}
	if len(filter.Platforms) > 0 && !containsString(filter.Platforms, platform) {
		return models
	}
	for _, p := range cfg.Platforms {
		if p.Platform != platform || !p.Enabled {
			continue
		}
		seen := make(map[string]bool, len(p.Models))
		for _, value := range p.Models {
			model := strings.TrimSpace(value)
			if model == "" || model == service.ChannelMonitorV2OtherModel || seen[model] {
				continue
			}
			if len(filter.Models) > 0 && !containsString(filter.Models, model) {
				continue
			}
			seen[model] = true
			models = append(models, model)
		}
		break
	}
	return models
}

// geiliChannelMonitorV2ModelScopeSQL applies the same allowlist before aggregation
// and LIMIT. Paired arrays preserve platform/model identity without interpolating
// user input or adding one SQL parameter per configured model/platform.
// Expressions are caller-owned SQL, never request input.
func geiliChannelMonitorV2ModelScopeSQL(filter service.ChannelMonitorV2Filter, cfg service.ChannelMonitorV2Config, platformExpr, modelExpr string, args []any) (string, []any) {
	platforms, models := []string{}, []string{}
	for _, p := range cfg.Platforms {
		for _, model := range geiliChannelMonitorV2Models(cfg, p.Platform, filter) {
			platforms = append(platforms, p.Platform)
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		return "FALSE", args
	}
	args = append(args, pq.Array(platforms), pq.Array(models))
	return fmt.Sprintf("(%s, %s) IN (SELECT * FROM unnest($%d::text[], $%d::text[]))", platformExpr, modelExpr, len(args)-1, len(args)), args
}

// Match the concrete platform attribution used by the error rollups, including
// composite groups (which route to accounts on concrete platforms).
const geiliChannelMonitorV2ErrorPlatformSQL = `lower(CASE
	WHEN g.platform = 'composite' THEN COALESCE(NULLIF(TRIM(a.platform), ''), NULLIF(NULLIF(lower(TRIM(current_error.platform)), ''), 'composite'), 'unknown')
	ELSE COALESCE(NULLIF(TRIM(current_error.platform), ''), 'unknown')
END)`

const geiliChannelMonitorV2ErrorModelSQL = `COALESCE(NULLIF(TRIM(current_error.requested_model), ''), NULLIF(TRIM(current_error.model), ''), 'unknown')`
