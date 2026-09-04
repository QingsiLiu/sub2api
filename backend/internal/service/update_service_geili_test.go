//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// 上游自带的 update_service_test.go 假定在线更新可用。测试二进制里把策略切回
// enabled，让上游用例保持原语义；本文件的用例再显式切到 disabled 验证守卫。
func init() {
	selfUpdatePolicy = "enabled"
}

func withSelfUpdatePolicy(t *testing.T, policy string) {
	t.Helper()
	previous := selfUpdatePolicy
	selfUpdatePolicy = policy
	t.Cleanup(func() { selfUpdatePolicy = previous })
}

func newGeiliUpdateService(current string) *UpdateService {
	return NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{
			release: &GitHubRelease{TagName: "v9.9.9"},
			recentReleases: []*GitHubRelease{
				{TagName: "v9.9.9"},
				{TagName: "v0.1.0"},
			},
		},
		current,
		"release",
	)
}

func TestGeiliSelfUpdateDisabledRejectsMutations(t *testing.T) {
	withSelfUpdatePolicy(t, "disabled")
	svc := newGeiliUpdateService("0.2.0-geili.1")

	err := svc.PerformUpdate(context.Background())
	require.True(t, errors.Is(err, ErrSelfUpdateDisabled), "PerformUpdate must be refused: %v", err)

	err = svc.Rollback()
	require.True(t, errors.Is(err, ErrSelfUpdateDisabled), "Rollback must be refused: %v", err)

	err = svc.RollbackToVersion(context.Background(), "0.1.0")
	require.True(t, errors.Is(err, ErrSelfUpdateDisabled), "RollbackToVersion must be refused: %v", err)

	versions, err := svc.ListRollbackVersions(context.Background())
	require.NoError(t, err)
	require.Empty(t, versions, "no rollback targets may be offered when self-update is disabled")
}

func TestGeiliSelfUpdateDisabledKeepsCheckUpdate(t *testing.T) {
	withSelfUpdatePolicy(t, "disabled")
	svc := newGeiliUpdateService("0.2.0-geili.1")

	info, err := svc.CheckUpdate(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, "0.2.0-geili.1", info.CurrentVersion)
	require.Equal(t, "9.9.9", info.LatestVersion)
	require.True(t, info.HasUpdate, "upstream release notifications must keep working")
}

func TestGeiliVersionSuffixIsIgnoredWhenComparingWithUpstream(t *testing.T) {
	// v0.2.0-geili.3 与官方 v0.2.0 视为同一基线，不应误报“有新版本”。
	require.Equal(t, 0, compareVersions("0.2.0-geili.3", "0.2.0"))
	require.Equal(t, -1, compareVersions("0.2.0-geili.3", "0.2.1"))
	require.Equal(t, 1, compareVersions("0.2.1-geili.1", "0.2.0"))
}

func TestGeiliSelfUpdateEnabledRestoresUpstreamBehaviour(t *testing.T) {
	withSelfUpdatePolicy(t, "enabled")
	svc := newGeiliUpdateService("9.9.9")

	err := svc.PerformUpdate(context.Background())
	require.True(t, errors.Is(err, ErrNoUpdateAvailable), "with policy=enabled upstream logic must run: %v", err)
}
