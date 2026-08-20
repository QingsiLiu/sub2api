package trainingdata

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestFinalizeWaitsBehindQueuedCaptureEvents(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		cfg:      config.TrainingDataConfig{SpoolMaxBytes: 1 << 20},
		spoolDir: root,
		openDir:  filepath.Join(root, "open"),
		readyDir: filepath.Join(root, "ready"),
		ctx:      ctx,
		cancel:   cancel,
	}
	require.NoError(t, os.MkdirAll(manager.openDir, 0o700))
	require.NoError(t, os.MkdirAll(manager.readyDir, 0o700))
	shard := newSpoolShard(manager, 64)
	manager.shards = []*spoolShard{shard}
	manager.active.Store(true)

	captureID := "capture-order-test"
	manifest := &Manifest{SchemaVersion: "capture-v1", CaptureID: captureID, StartedAt: time.Now().UTC()}
	require.True(t, manager.tryEnqueue(spoolEvent{kind: spoolStart, captureID: captureID, manifest: manifest}))
	for index := 0; index < 63; index++ {
		require.True(t, manager.tryEnqueue(spoolEvent{
			kind: spoolClientResponseChunk, captureID: captureID, data: []byte("x"), atMS: int64(index),
		}))
	}
	manager.enqueueFinalize(finalizeEvent{captureID: captureID, finishedAt: time.Now().UTC(), status: httpStatusOK})

	var wg sync.WaitGroup
	wg.Add(1)
	go shard.run(ctx, &wg)
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	readyDir := filepath.Join(manager.readyDir, captureID)
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(readyDir, "manifest.json"))
		return err == nil
	}, 3*time.Second, 10*time.Millisecond)

	var finalized Manifest
	require.NoError(t, readJSONFile(filepath.Join(readyDir, "manifest.json"), &finalized))
	require.Equal(t, int64(63), finalized.ClientResponseBytes)
	require.Len(t, finalized.ClientResponseChunks, 63)
	encoded, err := os.Open(filepath.Join(readyDir, filepath.FromSlash(finalized.ClientResponseFile)))
	require.NoError(t, err)
	decoded, err := DecodeCaptureBody(encoded, finalized.ClientResponseFile)
	require.NoError(t, err)
	require.NoError(t, encoded.Close())
	require.Equal(t, strings.Repeat("x", 63), string(decoded))
	require.Equal(t, directorySize(root), manager.spoolBytes.Load())
}

func TestTryEnqueueEnforcesQueueByteLimit(t *testing.T) {
	manager := &Manager{cfg: config.TrainingDataConfig{SpoolMaxBytes: 1024, QueueMaxBytes: 4}}
	manager.shards = []*spoolShard{newSpoolShard(manager, 64)}
	manager.active.Store(true)
	require.False(t, manager.tryEnqueue(spoolEvent{kind: spoolClientResponseChunk, captureID: "capture", data: []byte("12345")}))
	require.Zero(t, manager.queuedBytes.Load())
	require.True(t, manager.tryEnqueue(spoolEvent{kind: spoolClientResponseChunk, captureID: "capture", data: []byte("1234")}))
	require.Equal(t, int64(4), manager.queuedBytes.Load())
}

func TestSpoolShardHonorsConfiguredEventCapacity(t *testing.T) {
	shard := newSpoolShard(&Manager{}, 3)
	require.Equal(t, 3, cap(shard.events))
}

func TestRecoverOpenCaptureReconcilesBytesWrittenAfterCheckpoint(t *testing.T) {
	root := t.TempDir()
	openDir := filepath.Join(root, "open", "capture-recovery")
	readyRoot := filepath.Join(root, "ready")
	require.NoError(t, os.MkdirAll(filepath.Join(openDir, "client"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(openDir, "upstream", "attempt-0001"), 0o700))
	require.NoError(t, os.MkdirAll(readyRoot, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(openDir, "client", "request.body"), []byte("request"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(openDir, "client", "response.body"), []byte("response-tail"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(openDir, "upstream", "attempt-0001", "request.body"), []byte("upstream-request"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(openDir, "upstream", "attempt-0001", "response.body"), []byte("upstream-response-tail"), 0o600))

	manifest := Manifest{
		SchemaVersion: "capture-v1", CaptureID: "capture-recovery", StartedAt: time.Now().UTC(),
		ClientRequestFile: "client/request.body", ClientRequestBytes: 7,
		ClientResponseFile: "client/response.body", ClientResponseBytes: 8,
		ClientResponseChunks: []ChunkRecord{{Offset: 0, Length: 8, AtMS: 10}},
		Attempts: []AttemptManifest{{
			AttemptID: 1, RequestFile: "upstream/attempt-0001/request.body", RequestBytes: 16,
			ResponseFile: "upstream/attempt-0001/response.body", ResponseBytes: 8,
			ResponseChunks: []ChunkRecord{{Offset: 0, Length: 8, AtMS: 20}},
		}},
	}
	require.NoError(t, writeJSONAtomic(filepath.Join(openDir, "manifest.partial.json"), manifest))
	require.NoError(t, recoverOpenCapture(openDir, readyRoot))

	readyDir := filepath.Join(readyRoot, "capture-recovery")
	var recovered Manifest
	require.NoError(t, readJSONFile(filepath.Join(readyDir, "manifest.json"), &recovered))
	require.False(t, recovered.CaptureComplete)
	require.Contains(t, recovered.IncompleteReasons, "process_restart")
	require.Equal(t, int64(len("response-tail")), recovered.ClientResponseBytes)
	require.Equal(t, int64(len("upstream-response-tail")), recovered.Attempts[0].ResponseBytes)
	require.Equal(t, int64(-1), recovered.ClientResponseChunks[len(recovered.ClientResponseChunks)-1].AtMS)
	require.Equal(t, int64(-1), recovered.Attempts[0].ResponseChunks[len(recovered.Attempts[0].ResponseChunks)-1].AtMS)

	clientResponse, err := os.Open(filepath.Join(readyDir, filepath.FromSlash(recovered.ClientResponseFile)))
	require.NoError(t, err)
	decoded, err := DecodeCaptureBody(clientResponse, recovered.ClientResponseFile)
	require.NoError(t, err)
	require.NoError(t, clientResponse.Close())
	require.Equal(t, "response-tail", string(decoded))
}

func TestRecoverFinalizedCaptureMovesItWithoutDowngradingCompleteness(t *testing.T) {
	root := t.TempDir()
	openDir := filepath.Join(root, "open", "capture-finalized")
	readyRoot := filepath.Join(root, "ready")
	require.NoError(t, os.MkdirAll(openDir, 0o700))
	require.NoError(t, os.MkdirAll(readyRoot, 0o700))
	manifest := Manifest{
		SchemaVersion: "capture-v1", CaptureID: "capture-finalized",
		StartedAt: time.Now().UTC(), CaptureComplete: true,
	}
	require.NoError(t, writeJSONAtomic(filepath.Join(openDir, "manifest.json"), manifest))
	require.NoError(t, recoverOpenCapture(openDir, readyRoot))

	var recovered Manifest
	require.NoError(t, readJSONFile(filepath.Join(readyRoot, "capture-finalized", "manifest.json"), &recovered))
	require.True(t, recovered.CaptureComplete)
	require.Empty(t, recovered.IncompleteReasons)
}

const httpStatusOK = 200
