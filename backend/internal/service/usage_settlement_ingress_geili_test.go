package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type settlementIngressRepoProbe struct {
	UsageSettlementRepository
	mu         sync.Mutex
	prepareErr error
	records    map[string]*usageSettlementIngressRecord
	calls      int
}

func (r *settlementIngressRepoProbe) PrepareSettlement(ctx context.Context, cmd *UsageBillingCommand, log *UsageLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.prepareErr != nil {
		return r.prepareErr
	}
	frozen, err := freezeUsageSettlementIngress(cmd, log)
	if err != nil {
		return err
	}
	if r.records == nil {
		r.records = map[string]*usageSettlementIngressRecord{}
	}
	name := settlementIngressFilename(&frozen.Command)
	if before := r.records[name]; before != nil && !sameUsageSettlementIngressFinancial(before, frozen) {
		return ErrUsageBillingRequestConflict
	}
	r.records[name] = frozen
	return nil
}
func ingressFixture(t *testing.T) (*config.Config, *UsageBillingCommand, *UsageLog) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Pricing.DataDir = t.TempDir()
	cmd := &UsageBillingCommand{RequestID: "financial-test", APIKeyID: 2, UserID: 1, AccountID: 3, Model: "fixture", BalanceCost: .25, CompletedAt: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)}
	log := &UsageLog{RequestID: "log-test", APIKeyID: 2, UserID: 1, AccountID: 3, Model: "fixture", ActualCost: .25, TotalCost: .25, RateMultiplier: 1, CreatedAt: cmd.CompletedAt}
	return cfg, cmd, log
}
func ingressFiles(t *testing.T, cfg *config.Config) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir))
	require.NoError(t, err)
	var out []os.DirEntry
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			out = append(out, entry)
		}
	}
	return out
}
func replayIngressAll(t *testing.T, cfg *config.Config, repo UsageSettlementRepository) UsageSettlementIngressReplayStats {
	t.Helper()
	var total UsageSettlementIngressReplayStats
	for i := 0; i < 12; i++ {
		s, err := replayUsageSettlementIngress(context.Background(), cfg, repo, 2)
		require.NoError(t, err)
		total.Scanned += s.Scanned
		total.Prepared += s.Prepared
		total.Deferred += s.Deferred
		total.Invalid += s.Invalid
		if len(ingressFiles(t, cfg)) == 0 {
			return total
		}
	}
	t.Fatal("replay did not drain bounded test records")
	return total
}

func TestSettlementIngressSurvivesSQLFailureAndRestart(t *testing.T) {
	cfg, cmd, log := ingressFixture(t)
	outage := errors.New("database unavailable")
	repo := &settlementIngressRepoProbe{prepareErr: outage}
	err := prepareUsageSettlementDurably(context.Background(), cfg, repo, cmd, log)
	require.ErrorIs(t, err, ErrUsageSettlementIngressDeferred)
	require.ErrorIs(t, err, outage)
	files := ingressFiles(t, cfg)
	require.Len(t, files, 1)
	info, err := files[0].Info()
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	dirInfo, err := os.Stat(filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm())
	// A fresh process needs only the mounted directory, not the original request,
	// service, mutable log or billing object.
	log.ActualCost = 900
	cmd.BalanceCost = 900
	restartedCfg := &config.Config{}
	restartedCfg.Pricing.DataDir = cfg.Pricing.DataDir
	restartedRepo := &settlementIngressRepoProbe{}
	stats := replayIngressAll(t, restartedCfg, restartedRepo)
	require.Equal(t, 1, stats.Prepared)
	require.Len(t, restartedRepo.records, 1)
	for _, saved := range restartedRepo.records {
		require.Equal(t, .25, saved.Command.BalanceCost)
		require.Equal(t, .25, saved.Detail.ActualCost)
		require.Equal(t, "log-test", saved.Detail.RequestID)
	}
	require.Empty(t, ingressFiles(t, cfg))
}
func TestSettlementIngressConcurrentSameIdentityIsImmutable(t *testing.T) {
	cfg, cmd, log := ingressFixture(t)
	repo := &settlementIngressRepoProbe{prepareErr: errors.New("offline")}
	const n = 16
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- prepareUsageSettlementDurably(context.Background(), cfg, repo, cmd, log)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.ErrorIs(t, err, ErrUsageSettlementIngressDeferred)
	}
	require.Len(t, ingressFiles(t, cfg), 1)
	conflict := *cmd
	conflict.BalanceCost = .5
	require.ErrorIs(t, prepareUsageSettlementDurably(context.Background(), cfg, repo, &conflict, log), ErrUsageBillingRequestConflict)
	require.Equal(t, n, repo.calls, "conflicting file must fail before SQL")
	recovered := &settlementIngressRepoProbe{}
	replayIngressAll(t, cfg, recovered)
	for _, saved := range recovered.records {
		require.Equal(t, .25, saved.Command.BalanceCost)
	}
}
func TestSettlementIngressNeverSerializesAssociationsOrRequestSecrets(t *testing.T) {
	cfg, cmd, log := ingressFixture(t)
	log.User = &User{PasswordHash: "PASSWORD_SECRET"}
	log.APIKey = &APIKey{Key: "DOWNSTREAM_KEY_SECRET"}
	log.Account = &Account{Credentials: map[string]any{"api_key": "UPSTREAM_SECRET"}}
	cmd.UsageDetail = log
	repo := &settlementIngressRepoProbe{prepareErr: errors.New("offline")}
	require.ErrorIs(t, prepareUsageSettlementDurably(context.Background(), cfg, repo, cmd, log), ErrUsageSettlementIngressDeferred)
	files := ingressFiles(t, cfg)
	require.Len(t, files, 1)
	raw, err := os.ReadFile(filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir, files[0].Name()))
	require.NoError(t, err)
	for _, secret := range []string{"PASSWORD_SECRET", "DOWNSTREAM_KEY_SECRET", "UPSTREAM_SECRET", "Credentials", "UsageDetail"} {
		require.NotContains(t, string(raw), secret)
	}
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, float64(1), decoded["version"])
}
func TestSettlementIngressIgnoresPartialAndRejectsSymlinkFiles(t *testing.T) {
	cfg, cmd, log := ingressFixture(t)
	require.NoError(t, ensureUsageSettlementIngress(cfg))
	directory := filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir)
	require.NoError(t, os.WriteFile(filepath.Join(directory, ".pending-abandoned.tmp"), []byte(`{"command":`), 0600))
	repo := &settlementIngressRepoProbe{}
	stats, err := replayUsageSettlementIngress(context.Background(), cfg, repo, 32)
	require.NoError(t, err)
	require.Zero(t, stats.Scanned)
	require.Zero(t, repo.calls)
	target := filepath.Join(t.TempDir(), "unrelated.json")
	require.NoError(t, os.WriteFile(target, []byte("DO_NOT_TOUCH"), 0600))
	normalized := *cmd
	normalized.Normalize()
	name := settlementIngressFilename(&normalized)
	require.NoError(t, os.Symlink(target, filepath.Join(directory, name)))
	err = prepareUsageSettlementDurably(context.Background(), cfg, repo, cmd, log)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrUsageSettlementIngressDeferred)
	require.Zero(t, repo.calls)
	raw, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "DO_NOT_TOUCH", string(raw))
}
func TestSettlementIngressRejectsSymlinkDirectoriesAndUnavailableDisk(t *testing.T) {
	t.Run("spool symlink", func(t *testing.T) {
		cfg, cmd, log := ingressFixture(t)
		require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir)))
		repo := &settlementIngressRepoProbe{}
		err := prepareUsageSettlementDurably(context.Background(), cfg, repo, cmd, log)
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrUsageSettlementIngressDeferred)
		require.Zero(t, repo.calls)
	})
	t.Run("lock symlink", func(t *testing.T) {
		cfg, cmd, log := ingressFixture(t)
		require.NoError(t, ensureUsageSettlementIngress(cfg))
		lockDir := filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir, ".locks")
		require.NoError(t, os.Remove(lockDir))
		require.NoError(t, os.Symlink(t.TempDir(), lockDir))
		repo := &settlementIngressRepoProbe{}
		require.Error(t, prepareUsageSettlementDurably(context.Background(), cfg, repo, cmd, log))
		require.Zero(t, repo.calls)
	})
	t.Run("data file", func(t *testing.T) {
		cfg, cmd, log := ingressFixture(t)
		cfg.Pricing.DataDir = filepath.Join(cfg.Pricing.DataDir, "file")
		require.NoError(t, os.WriteFile(cfg.Pricing.DataDir, []byte("not a dir"), 0600))
		repo := &settlementIngressRepoProbe{}
		require.Error(t, prepareUsageSettlementDurably(context.Background(), cfg, repo, cmd, log))
		require.Zero(t, repo.calls)
	})
}
func TestSettlementIngressSuccessAcknowledgesAndReplayNeverDebits(t *testing.T) {
	cfg, cmd, log := ingressFixture(t)
	repo := &settlementIngressRepoProbe{}
	require.NoError(t, prepareUsageSettlementDurably(context.Background(), cfg, repo, cmd, log))
	require.Len(t, repo.records, 1)
	require.Empty(t, ingressFiles(t, cfg))
	// Probe only implements PrepareSettlement; any attempt to use Apply/money
	// effects is impossible through this narrow interface.
	stats, err := replayUsageSettlementIngress(context.Background(), cfg, repo, 32)
	require.NoError(t, err)
	require.Zero(t, stats.Prepared)
}
func TestSettlementIngressBoundedReplayAdvancesPastInvalidRecords(t *testing.T) {
	cfg, cmd, log := ingressFixture(t)
	offline := &settlementIngressRepoProbe{prepareErr: errors.New("offline")}
	for _, id := range []string{"first", "second", "third"} {
		copy := *cmd
		copy.RequestID = id
		require.ErrorIs(t, prepareUsageSettlementDurably(context.Background(), cfg, offline, &copy, log), ErrUsageSettlementIngressDeferred)
	}
	entries := ingressFiles(t, cfg)
	require.Len(t, entries, 3)
	poison := filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir, entries[0].Name())
	require.NoError(t, os.WriteFile(poison, []byte(`{"invalid":true}`), 0600))
	healthy := &settlementIngressRepoProbe{}
	prepared, invalid := 0, 0
	for i := 0; i < 12; i++ {
		stats, _ := replayUsageSettlementIngress(context.Background(), cfg, healthy, 1)
		require.LessOrEqual(t, stats.Scanned, 1)
		prepared += stats.Prepared
		invalid += stats.Invalid
	}
	require.Equal(t, 2, prepared)
	require.Positive(t, invalid)
	require.Len(t, healthy.records, 2)
	require.Len(t, ingressFiles(t, cfg), 1, "corrupt evidence is never silently removed")
}

type blockedSettlementIngressRepo struct {
	UsageSettlementRepository
	entered chan struct{}
}

func (r *blockedSettlementIngressRepo) PrepareSettlement(ctx context.Context, _ *UsageBillingCommand, _ *UsageLog) error {
	r.entered <- struct{}{}
	<-ctx.Done()
	return ctx.Err()
}
func TestSettlementIngress128SameStripeSQLTimeoutCannotPreventPersistence(t *testing.T) {
	cfg, seed, log := ingressFixture(t)
	require.NoError(t, ensureUsageSettlementIngress(cfg))
	const count = 128
	var commands []*UsageBillingCommand
	for i := 0; len(commands) < count; i++ {
		command := *seed
		command.RequestID = fmt.Sprintf("stripe-collision-%d", i)
		command.Normalize()
		if strings.HasPrefix(settlementIngressFilename(&command), "00") {
			commands = append(commands, &command)
		}
	}
	blocked := &blockedSettlementIngressRepo{entered: make(chan struct{}, count)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for _, command := range commands {
		wg.Add(1)
		go func(command *UsageBillingCommand) {
			defer wg.Done()
			errs <- prepareUsageSettlementDurably(ctx, cfg, blocked, command, log)
		}(command)
	}
	// All requests must pass the same stripe and reach their blocked SQL call
	// before any SQL timeout. Holding the file lock during SQL deadlocks here.
	reached := 0
	deadline := time.After(20 * time.Second)
collecting:
	for reached < count {
		select {
		case <-blocked.entered:
			reached++
		case <-deadline:
			break collecting
		}
	}
	cancel()
	wg.Wait()
	close(errs)
	var received []error
	for err := range errs {
		received = append(received, err)
	}
	require.Equal(t, count, reached, "SQL waits must never own the publish stripe; errors=%v", received)
	for _, err := range received {
		require.ErrorIs(t, err, ErrUsageSettlementIngressDeferred)
		require.ErrorIs(t, err, context.Canceled)
	}
	require.Len(t, ingressFiles(t, cfg), count)
	recovered := &settlementIngressRepoProbe{}
	prepared := 0
	for i := 0; i < 8 && prepared < count; i++ {
		stats, err := replayUsageSettlementIngress(context.Background(), cfg, recovered, 128)
		require.NoError(t, err)
		prepared += stats.Prepared
	}
	require.Equal(t, count, prepared)
	require.Empty(t, ingressFiles(t, cfg))
	require.Len(t, recovered.records, count)
}
func TestSettlementIngressCanceledCallerStillPersistsFinalUsage(t *testing.T) {
	cfg, cmd, log := ingressFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := &settlementIngressRepoProbe{}
	err := prepareUsageSettlementDurably(ctx, cfg, repo, cmd, log)
	require.ErrorIs(t, err, ErrUsageSettlementIngressDeferred)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, ingressFiles(t, cfg), 1)
}
func TestSettlementIngressAcknowledgmentCannotRemoveConflictingReplacement(t *testing.T) {
	cfg, cmd, log := ingressFixture(t)
	require.NoError(t, ensureUsageSettlementIngress(cfg))
	root, err := openExistingUsageSettlementIngress(cfg)
	require.NoError(t, err)
	defer root.Close()
	first, err := freezeUsageSettlementIngress(cmd, log)
	require.NoError(t, err)
	name := settlementIngressFilename(&first.Command)
	_, err = persistUsageSettlementIngress(root, name, first)
	require.NoError(t, err)
	require.NoError(t, root.Remove(name))
	changed := *cmd
	changed.BalanceCost = .75
	second, err := freezeUsageSettlementIngress(&changed, log)
	require.NoError(t, err)
	_, err = persistUsageSettlementIngress(root, name, second)
	require.NoError(t, err)
	require.ErrorIs(t, acknowledgeUsageSettlementIngress(context.Background(), root, name, first), ErrUsageBillingRequestConflict)
	remaining, err := readUsageSettlementIngress(root, name)
	require.NoError(t, err)
	require.Equal(t, .75, remaining.Command.BalanceCost)
}
