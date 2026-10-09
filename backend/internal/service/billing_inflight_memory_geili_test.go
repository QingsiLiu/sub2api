//go:build unit

package service

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// HeapAlloc measures the whole test process, including lingering workers from
// unrelated tests. Re-execute this one memory assertion in the same test binary
// so the original workload, DB/snapshot checks and 8 MiB threshold stay intact.
func geiliRunIsolatedInflightMemoryTest(t *testing.T) bool {
	t.Helper()
	const marker = "GEILI_INFLIGHT_MEMORY_TEST_PROCESS"
	if os.Getenv(marker) == "1" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	binary, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestInflightEstimate_AccountMappingNoDBAndBoundedMemory$", "-test.count=1", "-test.timeout=30s")
	cmd.Env = append(os.Environ(), marker+"=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "isolated original memory assertion: %s", output)
	require.Contains(t, string(output), "PASS")
	return true
}
