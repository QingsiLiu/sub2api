package service

import (
	"runtime/metrics"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

type runtimeMemoryStatsGeili struct {
	heapAlloc, heapInuse, heapIdle, heapReleased uint64
	heapLive, heapGoal, sys, stackInuse          uint64
	numGC, softLimit                             uint64
}

func logRuntimeMemoryGeili(cgroupUsed, cgroupLimit uint64, cgroupOK bool) {
	logger.L().Info("runtime.memory", runtimeMemoryLogFieldsGeili(readRuntimeMemoryStatsGeili(), cgroupUsed, cgroupLimit, cgroupOK)...)
}

func readRuntimeMemoryStatsGeili() runtimeMemoryStatsGeili {
	samples := []metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/heap/unused:bytes"},
		{Name: "/memory/classes/heap/free:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
		{Name: "/memory/classes/heap/stacks:bytes"},
		{Name: "/memory/classes/total:bytes"},
		{Name: "/gc/heap/live:bytes"},
		{Name: "/gc/heap/goal:bytes"},
		{Name: "/gc/cycles/total:gc-cycles"},
		{Name: "/gc/gomemlimit:bytes"},
	}
	metrics.Read(samples)
	value := func(index int) uint64 {
		if samples[index].Value.Kind() != metrics.KindUint64 {
			return 0
		}
		return samples[index].Value.Uint64()
	}
	return runtimeMemoryStatsGeili{
		heapAlloc: value(0), heapInuse: value(0) + value(1),
		heapIdle: value(2) + value(3), heapReleased: value(3),
		stackInuse: value(4), sys: value(5), heapLive: value(6),
		heapGoal: value(7), numGC: value(8), softLimit: value(9),
	}
}

func runtimeMemoryLogFieldsGeili(stats runtimeMemoryStatsGeili, cgroupUsed, cgroupLimit uint64, cgroupOK bool) []zap.Field {
	return []zap.Field{
		zap.String("component", "service.ops_metrics_collector"),
		// Keep diagnostics out of the database-backed system-log queue.
		zap.Bool(logger.OpsSystemLogSkipField, true),
		zap.Uint64("heap_alloc_bytes", stats.heapAlloc),
		zap.Uint64("heap_inuse_bytes", stats.heapInuse),
		zap.Uint64("heap_idle_bytes", stats.heapIdle),
		zap.Uint64("heap_free_bytes", stats.heapIdle-stats.heapReleased),
		zap.Uint64("heap_released_bytes", stats.heapReleased),
		zap.Uint64("heap_live_bytes", stats.heapLive),
		zap.Uint64("heap_goal_bytes", stats.heapGoal),
		zap.Uint64("sys_bytes", stats.sys),
		zap.Uint64("stack_inuse_bytes", stats.stackInuse),
		zap.Uint64("num_gc", stats.numGC),
		zap.Uint64("go_memory_limit_bytes", stats.softLimit),
		zap.Bool("cgroup_available", cgroupOK),
		zap.Uint64("cgroup_memory_current_bytes", cgroupUsed),
		zap.Uint64("cgroup_memory_limit_bytes", cgroupLimit),
	}
}
