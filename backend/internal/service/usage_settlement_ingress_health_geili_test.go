package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSettlementIngressReadinessCachesForAtMostOneSecond(t *testing.T) {
	cfg, _, _ := ingressFixture(t)
	now := time.Now()
	calls := atomic.Int64{}
	probe := func(*config.Config) (uint64, error) { calls.Add(1); return 5 << 30, nil }
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := usageSettlementIngressReadinessResult(cfg, now, probe)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int64(1), calls.Load())
	_, _, err := usageSettlementIngressReadinessResult(cfg, now.Add(999*time.Millisecond), probe)
	require.NoError(t, err)
	require.Equal(t, int64(1), calls.Load())
	diskErr := errors.New("disk full")
	failed := func(*config.Config) (uint64, error) { calls.Add(1); return 0, diskErr }
	_, _, err = usageSettlementIngressReadinessResult(cfg, now.Add(time.Second+time.Millisecond), failed)
	require.ErrorIs(t, err, diskErr)
	require.Equal(t, int64(2), calls.Load())
	_, _, err = usageSettlementIngressReadinessResult(cfg, now.Add(1500*time.Millisecond), probe)
	require.ErrorIs(t, err, diskErr)
	require.Equal(t, int64(2), calls.Load())
	_, _, err = usageSettlementIngressReadinessResult(cfg, now.Add(2*time.Second+2*time.Millisecond), probe)
	require.NoError(t, err)
	require.Equal(t, int64(3), calls.Load())
}
func TestSettlementIngressReserveUsesBytesNotDiskPercent(t *testing.T) {
	require.Error(t, checkUsageSettlementIngressFreeBytes(usageSettlementIngressMinFreeBytes-1))
	require.NoError(t, checkUsageSettlementIngressFreeBytes(usageSettlementIngressMinFreeBytes))
	require.NoError(t, checkUsageSettlementIngressFreeBytes(5<<30), "5GiB free on a 95%-used disk remains safe")
}
func TestSettlementIngressReadinessRejectsMissingOrChangedDirectory(t *testing.T) {
	cfg, _, _ := ingressFixture(t)
	require.Error(t, checkUsageSettlementIngressReady(cfg), "preflight does not provision missing mounted state")
	require.NoError(t, ensureUsageSettlementIngress(cfg))
	key, err := usageSettlementIngressConfigKey(cfg)
	require.NoError(t, err)
	usageSettlementIngressReadinessCache.Delete(key)
	require.NoError(t, checkUsageSettlementIngressReady(cfg))
	entries, err := os.ReadDir(filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir))
	require.NoError(t, err)
	require.Len(t, entries, 1, "probe leaves only private lock directory")
	require.NoError(t, os.Chmod(filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir), 0755))
	usageSettlementIngressReadinessCache.Delete(key)
	require.Error(t, checkUsageSettlementIngressReady(cfg), "private directory permissions are mandatory")
	require.NoError(t, os.Chmod(filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir), 0700))
	require.NoError(t, os.Remove(filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir, ".locks")))
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir, ".locks")))
	usageSettlementIngressReadinessCache.Delete(key)
	require.Error(t, checkUsageSettlementIngressReady(cfg))
}
func TestSettlementIngressHealthCountsPendingInvalidAndOldest(t *testing.T) {
	cfg, cmd, log := ingressFixture(t)
	offline := &settlementIngressRepoProbe{prepareErr: errors.New("offline")}
	require.ErrorIs(t, prepareUsageSettlementDurably(context.Background(), cfg, offline, cmd, log), ErrUsageSettlementIngressDeferred)
	path := filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir, ingressFiles(t, cfg)[0].Name())
	old := time.Now().Add(-121 * time.Second)
	require.NoError(t, os.Chtimes(path, old, old))
	other := *cmd
	other.RequestID = "corrupt"
	other.Normalize()
	invalidPath := filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir, settlementIngressFilename(&other))
	require.NoError(t, os.WriteFile(invalidPath, []byte(`{"broken":true}`), 0600))
	stats, err := usageSettlementIngressHealth(cfg)
	require.NoError(t, err)
	require.True(t, stats.Enabled)
	require.True(t, stats.Ready)
	require.Equal(t, int64(2), stats.Pending)
	require.Equal(t, int64(1), stats.Invalid)
	require.GreaterOrEqual(t, stats.OldestPendingSeconds, 120.0)
	require.True(t, stats.ScanComplete)
	require.Greater(t, stats.AvailableBytes, usageSettlementIngressMinFreeBytes)
	require.NoError(t, os.Remove(invalidPath))
	replayIngressAll(t, cfg, &settlementIngressRepoProbe{})
	stats, err = usageSettlementIngressHealth(cfg)
	require.NoError(t, err)
	require.Zero(t, stats.Pending)
	require.Zero(t, stats.Invalid)
	require.Zero(t, stats.OldestPendingSeconds)
}
func TestSettlementIngressHealthUsesBoundedIncrementalScan(t *testing.T) {
	cfg, _, _ := ingressFixture(t)
	require.NoError(t, ensureUsageSettlementIngress(cfg))
	directory := filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir)
	for i := 0; i < usageSettlementIngressHealthScanLimit+7; i++ {
		f, err := os.CreateTemp(directory, ".partial-")
		require.NoError(t, err)
		require.NoError(t, f.Close())
	}
	stats, err := usageSettlementIngressHealth(cfg)
	require.NoError(t, err)
	require.False(t, stats.ScanComplete)
	require.Equal(t, int64(usageSettlementIngressHealthScanLimit), stats.Scanned)
	stats, err = usageSettlementIngressHealth(cfg)
	require.NoError(t, err)
	require.True(t, stats.ScanComplete)
	require.Equal(t, int64(usageSettlementIngressHealthScanLimit+8), stats.Scanned)
	require.Zero(t, stats.Pending)
}
func TestSettlementIngressReadinessLegacyNilConfig(t *testing.T) {
	require.NoError(t, checkUsageSettlementIngressReady(nil))
	stats, err := usageSettlementIngressHealth(nil)
	require.NoError(t, err)
	require.False(t, stats.Enabled)
	require.True(t, stats.Ready)
	require.True(t, stats.ScanComplete)
}
