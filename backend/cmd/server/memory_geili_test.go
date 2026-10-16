package main

import (
	"math"
	"os"
	"reflect"
	"testing"
)

func TestConfigureMemoryLimitGeiliPreservesExplicitEnvironment(t *testing.T) {
	for _, setting := range []string{"off", "3GiB", ""} {
		t.Run(setting, func(t *testing.T) {
			t.Setenv("GOMEMLIMIT", setting)
			limit, changed := configureMemoryLimitGeili(os.LookupEnv,
				func() (int64, bool) { t.Fatal("explicit GOMEMLIMIT must skip detection"); return 0, false },
				func(int64) int64 { t.Fatal("explicit GOMEMLIMIT must not change runtime limit"); return 0 })
			if changed || limit != 0 {
				t.Fatalf("got (%d, %v), want (0, false)", limit, changed)
			}
		})
	}
}

func TestConfigureMemoryLimitGeiliRuntimeAndCgroupPriority(t *testing.T) {
	const gib = int64(1 << 30)
	for _, tc := range []struct {
		name             string
		current, cgroup  int64
		cgroupOK         bool
		want             int64
		changed          bool
		wantRuntimeCalls []int64
		wantDetect       bool
	}{
		{"finite cgroup", math.MaxInt64, 6 * gib, true, 9 * gib / 2, true, []int64{-1, 9 * gib / 2}, true},
		{"programmatic cap preserved", 2 * gib, 6 * gib, true, 2 * gib, false, []int64{-1}, false},
		{"larger programmatic cap preserved", 8 * gib, 6 * gib, true, 8 * gib, false, []int64{-1}, false},
		{"host unchanged", math.MaxInt64, 0, false, math.MaxInt64, false, []int64{-1}, true},
		{"invalid zero limit unchanged", math.MaxInt64, 0, true, math.MaxInt64, false, []int64{-1}, true},
		{"negative limit unchanged", math.MaxInt64, -1, true, math.MaxInt64, false, []int64{-1}, true},
		{"large limit cannot overflow", math.MaxInt64, math.MaxInt64 - 1, true, (math.MaxInt64 - 1) - (math.MaxInt64-1)/4, true, []int64{-1, (math.MaxInt64 - 1) - (math.MaxInt64-1)/4}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []int64
			detected := false
			limit, changed := configureMemoryLimitGeili(func(string) (string, bool) { return "", false },
				func() (int64, bool) { detected = true; return tc.cgroup, tc.cgroupOK },
				func(value int64) int64 { calls = append(calls, value); return tc.current })
			if limit != tc.want || changed != tc.changed || !reflect.DeepEqual(calls, tc.wantRuntimeCalls) || detected != tc.wantDetect {
				t.Fatalf("limit=%d changed=%v calls=%v detected=%v; want limit=%d changed=%v calls=%v detected=%v", limit, changed, calls, detected, tc.want, tc.changed, tc.wantRuntimeCalls, tc.wantDetect)
			}
		})
	}
}
