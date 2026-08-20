package trainingdata

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCaptureLifecyclePersistsClientAndUpstreamPayloads(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		cfg: config.TrainingDataConfig{
			SpoolMaxBytes: 1 << 20, QueueMaxBytes: 1 << 19,
			CaptureMaxBodyBytes: 1 << 16,
		},
		spoolDir: root,
		openDir:  filepath.Join(root, "open"),
		readyDir: filepath.Join(root, "ready"),
		ctx:      ctx,
		cancel:   cancel,
	}
	require.NoError(t, os.MkdirAll(manager.openDir, 0o700))
	require.NoError(t, os.MkdirAll(manager.readyDir, 0o700))
	shard := newSpoolShard(manager, 128)
	manager.shards = []*spoolShard{shard}
	manager.rights.Store(&rightsSnapshot{bySubject: map[string]RightsGrant{
		rightsLookupKey("user", "user-ref"): {
			RightsID: "00000000-0000-0000-0000-000000000001", ScopeType: "user", ScopeRef: "user-ref",
			Version: 1, Status: RightsEligible, AllowedPurposes: []string{"model_training"},
		},
	}})
	manager.active.Store(true)
	var wg sync.WaitGroup
	wg.Add(1)
	go shard.run(ctx, &wg)
	t.Cleanup(func() {
		manager.active.Store(false)
		cancel()
		wg.Wait()
	})

	clientRequest := []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`)
	session := manager.Begin(BeginInput{
		UserSubjectRef: "user-ref", APIKeySubjectRef: "key-ref", Method: http.MethodPost,
		Route: "/v1/chat/completions", Protocol: "openai_chat_completions", ClientModel: "client-model",
		Headers: http.Header{"Authorization": []string{"Bearer hidden"}}, RequestBody: clientRequest,
	})
	require.NotNil(t, session)

	upstreamRequestBody := []byte(`{"model":"upstream-model","messages":[{"role":"user","content":"hello"}]}`)
	upstreamRequest, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", bytes.NewReader(upstreamRequestBody))
	require.NoError(t, err)
	upstreamRequest.Header.Set("Authorization", "Bearer upstream-hidden")
	attempt := session.BeginUpstreamAttempt(upstreamRequest)
	require.NotNil(t, attempt)
	upstreamResponseBody := []byte(`{"choices":[{"message":{"role":"assistant","content":"world"}}]}`)
	upstreamResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(upstreamResponseBody)),
	}
	attempt.RecordResponse(upstreamResponse)
	consumed, err := io.ReadAll(upstreamResponse.Body)
	require.NoError(t, err)
	require.Equal(t, upstreamResponseBody, consumed)
	require.NoError(t, upstreamResponse.Body.Close())

	clientResponseBody := []byte(`{"choices":[{"message":{"role":"assistant","content":"world"}}]}`)
	session.RecordClientResponseHeaders(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}})
	session.RecordClientResponseChunk(clientResponseBody)
	session.Finish(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}})

	readyDir := filepath.Join(manager.readyDir, session.CaptureID())
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(readyDir, "manifest.json"))
		return err == nil
	}, 3*time.Second, 10*time.Millisecond)

	var manifest Manifest
	require.NoError(t, readJSONFile(filepath.Join(readyDir, "manifest.json"), &manifest))
	require.True(t, manifest.CaptureComplete)
	require.Equal(t, RightsEligible, manifest.RightsStatus)
	require.Len(t, manifest.Attempts, 1)
	require.Equal(t, "upstream-model", manifest.Attempts[0].Model)
	require.True(t, manifest.Attempts[0].Complete)
	require.Equal(t, http.StatusOK, manifest.Attempts[0].HTTPStatus)
	require.Equal(t, "<redacted>", manifest.ClientRequest.Headers.Get("Authorization"))
	require.Equal(t, "<redacted>", manifest.Attempts[0].RequestHeaders.Get("Authorization"))
	require.Len(t, manifest.Files, 4)
	require.NoError(t, verifyCaptureArtifacts(readyDir, manifest))

	requireCaptureBody(t, readyDir, manifest.ClientRequestFile, clientRequest)
	requireCaptureBody(t, readyDir, manifest.Attempts[0].RequestFile, upstreamRequestBody)
	requireCaptureBody(t, readyDir, manifest.Attempts[0].ResponseFile, upstreamResponseBody)
	requireCaptureBody(t, readyDir, manifest.ClientResponseFile, clientResponseBody)
}

func requireCaptureBody(t *testing.T, root, relative string, expected []byte) {
	t.Helper()
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(relative)))
	require.NoError(t, err)
	decoded, err := DecodeCaptureBody(file, relative)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.JSONEq(t, string(expected), string(decoded))
}
