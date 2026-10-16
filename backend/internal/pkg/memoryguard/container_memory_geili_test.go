package memoryguard

import (
	"fmt"
	"os"
	"testing"
)

func TestContainerMemoryLimitGeili(t *testing.T) {
	const gib = int64(1 << 30)
	v2Mount := "29 23 0:26 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n"
	v1Mount := "29 23 0:26 / /sys/fs/cgroup/memory rw - cgroup cgroup rw,memory\n"
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  int64
	}{
		{"v2 finite standard mount", map[string]string{"/sys/fs/cgroup/memory.max": "6442450944\n"}, 6 * gib},
		{"unlimited v2", map[string]string{"/sys/fs/cgroup/memory.max": "max\n"}, 0},
		{"missing cgroup on host", nil, 0},
		{"invalid and overflowing values", map[string]string{"/sys/fs/cgroup/memory.max": "18446744073709551615", "/sys/fs/cgroup/memory/memory.limit_in_bytes": "invalid"}, 0},
		{"v1 unlimited sentinel", map[string]string{"/sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712\n"}, 0},
		{"zero limit", map[string]string{"/sys/fs/cgroup/memory.max": "0"}, 0},
		{"v2 finite parent of unlimited child", map[string]string{
			"/proc/self/cgroup": "0::/slice/app\n", "/proc/self/mountinfo": v2Mount,
			"/sys/fs/cgroup/slice/app/memory.max": "max", "/sys/fs/cgroup/slice/memory.max": "6442450944", "/sys/fs/cgroup/memory.max": "max",
		}, 6 * gib},
		{"v2 smallest child limit", map[string]string{
			"/proc/self/cgroup": "0::/slice/app\n", "/proc/self/mountinfo": v2Mount,
			"/sys/fs/cgroup/slice/app/memory.max": "2147483648", "/sys/fs/cgroup/slice/memory.max": "6442450944",
		}, 2 * gib},
		{"v1 finite ancestor", map[string]string{
			"/proc/self/cgroup": "3:cpu,cpuacct:/unrelated\n7:memory:/slice/app\n", "/proc/self/mountinfo": v1Mount,
			"/sys/fs/cgroup/memory/slice/app/memory.limit_in_bytes": "8589934592", "/sys/fs/cgroup/memory/slice/memory.limit_in_bytes": "4294967296",
		}, 4 * gib},
		{"mount physical root", map[string]string{
			"/proc/self/cgroup": "0::/docker/id/child\n", "/proc/self/mountinfo": "29 23 0:26 /docker/id /cg rw - cgroup2 cgroup rw\n",
			"/cg/child/memory.max": "max", "/cg/memory.max": "6442450944",
		}, 6 * gib},
		{"namespaced root", map[string]string{
			"/proc/self/cgroup": "0::/\n", "/proc/self/mountinfo": "29 23 0:26 /docker/id /cg rw - cgroup2 cgroup rw\n", "/cg/memory.max": "6442450944",
		}, 6 * gib},
		{"namespaced child", map[string]string{
			"/proc/self/cgroup": "0::/child\n", "/proc/self/mountinfo": "29 23 0:26 /docker/id /cg rw - cgroup2 cgroup rw\n", "/cg/child/memory.max": "2147483648", "/cg/memory.max": "6442450944",
		}, 2 * gib},
		{"escaped mountpoint", map[string]string{
			"/proc/self/cgroup": "0::/child\n", "/proc/self/mountinfo": `29 23 0:26 / /memory\040group rw - cgroup2 cgroup rw` + "\n", "/memory group/child/memory.max": "2147483648",
		}, 2 * gib},
		{"reject traversal", map[string]string{
			"/proc/self/cgroup": "0::/../outside\n", "/proc/self/mountinfo": v2Mount, "/sys/fs/outside/memory.max": "1024",
		}, 0},
		{"ignore non-memory controller", map[string]string{
			"/proc/self/cgroup": "7:memory_not_real:/child\n", "/proc/self/mountinfo": v1Mount, "/sys/fs/cgroup/memory/child/memory.limit_in_bytes": "1024",
		}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readFile := func(name string) ([]byte, error) {
				if value, ok := tc.files[name]; ok {
					return []byte(value), nil
				}
				return nil, os.ErrNotExist
			}
			got, ok := containerMemoryLimitGeili(readFile)
			if got != tc.want || ok != (tc.want > 0) {
				t.Fatalf("ContainerMemoryLimit = (%d, %v), want (%d, %v)", got, ok, tc.want, tc.want > 0)
			}
		})
	}
}

func TestContainerMemoryLimitGeiliStopsAtMountedRoot(t *testing.T) {
	files := map[string]string{
		"/proc/self/cgroup":         "0::/tenant/app\n",
		"/proc/self/mountinfo":      "29 23 0:26 / /cg rw - cgroup2 cgroup rw\n",
		"/cg/tenant/app/memory.max": "max", "/cg/tenant/memory.max": "4096", "/cg/memory.max": "8192",
	}
	got, ok := containerMemoryLimitGeili(func(name string) ([]byte, error) {
		if name == "/memory.max" {
			t.Fatal("walk escaped the mounted cgroup root")
		}
		if value, found := files[name]; found {
			return []byte(value), nil
		}
		return nil, fmt.Errorf("missing %s", name)
	})
	if !ok || got != 4096 {
		t.Fatalf("got (%d, %v), want (4096, true)", got, ok)
	}
}
