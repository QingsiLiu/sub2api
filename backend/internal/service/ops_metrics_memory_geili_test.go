package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestRuntimeMemoryGeiliSnapshotHasSupportedMetrics(t *testing.T) {
	stats := readRuntimeMemoryStatsGeili()
	require.Positive(t, stats.heapAlloc)
	require.GreaterOrEqual(t, stats.heapInuse, stats.heapAlloc)
	require.GreaterOrEqual(t, stats.heapIdle, stats.heapReleased)
	require.GreaterOrEqual(t, stats.sys, stats.heapInuse+stats.heapIdle+stats.stackInuse)
	require.Positive(t, stats.heapGoal)
	require.Positive(t, stats.softLimit)
}

func TestRuntimeMemoryGeiliLogDistinguishesHeapAndContainer(t *testing.T) {
	stats := runtimeMemoryStatsGeili{
		heapAlloc: 100, heapInuse: 120, heapIdle: 90, heapReleased: 30,
		heapLive: 80, heapGoal: 160, sys: 300, stackInuse: 20, numGC: 7, softLimit: 450,
	}
	core, logs := observer.New(zapcore.InfoLevel)
	zap.New(core).Info("runtime.memory", runtimeMemoryLogFieldsGeili(stats, 500, 600, true)...)
	require.Len(t, logs.All(), 1)
	fields := logs.All()[0].ContextMap()
	for name, want := range map[string]uint64{
		"heap_alloc_bytes": 100, "heap_inuse_bytes": 120, "heap_idle_bytes": 90,
		"heap_free_bytes":     60,
		"heap_released_bytes": 30, "heap_live_bytes": 80, "heap_goal_bytes": 160,
		"sys_bytes": 300, "stack_inuse_bytes": 20, "num_gc": 7,
		"go_memory_limit_bytes": 450, "cgroup_memory_current_bytes": 500, "cgroup_memory_limit_bytes": 600,
	} {
		require.Equal(t, want, fields[name], name)
	}
	require.Equal(t, true, fields["cgroup_available"])
	require.Equal(t, true, fields[logger.OpsSystemLogSkipField], "runtime diagnostics must not fill the database-backed log queue")
}
