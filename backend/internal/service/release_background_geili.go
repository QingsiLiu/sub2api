package service

import "github.com/Wei-Shaw/sub2api/internal/pkg/releaselifecycle"

// ReleaseControl holds a shared-volume flock for the sole background owner.
// Claim admission and accounting are atomic with pause/drain. Cache subscribers,
// per-request refreshes and lease heartbeats intentionally remain instance-local.
func beginReleaseBackgroundGeili(kind string) (func(), bool) {
	return releaselifecycle.Default.BeginBackground(kind)
}

func beginReleaseWorkGeili(kind string) func() { return releaselifecycle.Default.Begin(kind) }
