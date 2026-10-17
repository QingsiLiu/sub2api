package main

import (
	"math"
	"os"
	"runtime/debug"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/memoryguard"
	"go.uber.org/zap"
)

// configureContainerMemoryGeili leaves headroom below the kernel hard limit
// for non-Go memory and request bursts. The Go limit is a GC target, not an
// admission limit; explicit operator and earlier runtime choices take priority.
func configureContainerMemoryGeili() {
	limit, changed := configureMemoryLimitGeili(os.LookupEnv, memoryguard.ContainerMemoryLimit, debug.SetMemoryLimit)
	if changed {
		logger.L().Info("runtime.container_memory_limit", zap.Int64("go_memory_limit_bytes", limit), zap.Int("container_limit_percent", 75))
	}
}

func configureMemoryLimitGeili(lookupEnv func(string) (string, bool), containerLimit func() (int64, bool), setLimit func(int64) int64) (int64, bool) {
	if _, explicit := lookupEnv("GOMEMLIMIT"); explicit {
		return 0, false
	}
	// A negative argument queries the current limit without changing it.
	current := setLimit(-1)
	if current != math.MaxInt64 {
		return current, false
	}
	container, ok := containerLimit()
	if !ok || container <= 0 {
		return current, false
	}
	target := container - container/4
	setLimit(target)
	return target, true
}
