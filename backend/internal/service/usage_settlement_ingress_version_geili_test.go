package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSettlementIngressNewSchemaDoesNotReachLegacyReader(t *testing.T) {
	cfg, cmd, log := ingressFixture(t)
	log.CacheCreationTokens, log.CacheCreation30mTokens, log.CacheCreation12hTokens = 10, 3, 7
	offline := &settlementIngressRepoProbe{prepareErr: errors.New("owned SQL outage")}
	require.ErrorIs(t, prepareUsageSettlementDurably(context.Background(), cfg, offline, cmd, log), ErrUsageSettlementIngressDeferred)
	entries := ingressFiles(t, cfg)
	require.Len(t, entries, 1)
	name := entries[0].Name()
	// The fixed production reader only selects 64 hex characters plus .json.
	// It rejects unknown nested JSON fields, including both new TTL counters.
	require.False(t, len(name) == 69 && strings.HasSuffix(name, ".json"))
	require.True(t, strings.HasSuffix(name, ".v2.json"))
	root, err := openUsageSettlementIngress(cfg)
	require.NoError(t, err)
	defer root.Close()
	record, err := readUsageSettlementIngress(root, name)
	require.NoError(t, err)
	require.Equal(t, 2, record.Version)
	require.Equal(t, 3, record.Detail.CacheCreation30mTokens)
	require.Equal(t, 7, record.Detail.CacheCreation12hTokens)
	stats, err := usageSettlementIngressHealth(cfg)
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.Pending)
	require.Zero(t, stats.Invalid)
	recovered := &settlementIngressRepoProbe{}
	replayIngressAll(t, cfg, recovered)
	require.Empty(t, ingressFiles(t, cfg))
	require.Equal(t, 1, recovered.calls)
}

func TestSettlementIngressReadsLegacyFilesAndRejectsVersionFilenameMismatch(t *testing.T) {
	cfg, cmd, log := ingressFixture(t)
	require.NoError(t, ensureUsageSettlementIngress(cfg))
	root, err := openUsageSettlementIngress(cfg)
	require.NoError(t, err)
	defer root.Close()
	record, err := freezeUsageSettlementIngress(cmd, log)
	require.NoError(t, err)
	raw, err := json.Marshal(record)
	require.NoError(t, err)
	var legacy map[string]any
	require.NoError(t, json.Unmarshal(raw, &legacy))
	legacy["version"] = 1
	detail := legacy["detail"].(map[string]any)
	delete(detail, "cache_creation_30m_tokens")
	delete(detail, "cache_creation_12h_tokens")
	raw, err = json.Marshal(legacy)
	require.NoError(t, err)
	name := strings.TrimSuffix(settlementIngressFilename(cmd), ".v2.json") + ".json"
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = file.Write(raw)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	decoded, err := readUsageSettlementIngress(root, name)
	require.NoError(t, err)
	require.Equal(t, 1, decoded.Version)
	require.Zero(t, decoded.Detail.CacheCreation12hTokens)
	other := *cmd
	other.RequestID += "-v2"
	other.RequestFingerprint = ""
	otherLog := *log
	otherLog.RequestID += "-v2"
	otherLog.CacheCreationTokens, otherLog.CacheCreation30mTokens, otherLog.CacheCreation12hTokens = 10, 3, 7
	offline := &settlementIngressRepoProbe{prepareErr: errors.New("owned SQL outage")}
	require.ErrorIs(t, prepareUsageSettlementDurably(context.Background(), cfg, offline, &other, &otherLog), ErrUsageSettlementIngressDeferred)
	stats, err := usageSettlementIngressHealth(cfg)
	require.NoError(t, err)
	require.Equal(t, int64(2), stats.Pending, "new owner counts both generations")
	require.Zero(t, stats.Invalid)
	require.NoError(t, root.Rename(name, settlementIngressFilename(cmd)))
	_, err = readUsageSettlementIngress(root, settlementIngressFilename(cmd))
	require.ErrorContains(t, err, "filename identity mismatch")
	require.NoError(t, root.Rename(settlementIngressFilename(cmd), name))
	recovered := &settlementIngressRepoProbe{}
	replayIngressAll(t, cfg, recovered)
	require.Equal(t, 2, recovered.calls)
	require.Empty(t, ingressFiles(t, cfg))
}
