package trainingdata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

type spoolEventKind uint8

const (
	spoolStart spoolEventKind = iota + 1
	spoolClientResponseHeaders
	spoolClientResponseChunk
	spoolAttemptStart
	spoolAttemptResponseHeaders
	spoolAttemptResponseChunk
	spoolAttemptFinish
	spoolFinalize
)

type spoolEvent struct {
	kind      spoolEventKind
	captureID string
	attemptID int
	manifest  *Manifest
	headers   HeaderSnapshot
	attempt   *AttemptManifest
	data      []byte
	atMS      int64
	errText   string
	complete  bool
	finalize  finalizeEvent
}

type finalizeEvent struct {
	captureID        string
	finishedAt       time.Time
	status           int
	headers          httpHeaderAlias
	incompleteReason []string
}

// httpHeaderAlias avoids retaining caller-owned header maps on the finalizer
// channel while keeping the spool package's event shape compact.
type httpHeaderAlias map[string][]string

type spoolCaptureState struct {
	dir                   string
	manifest              Manifest
	files                 map[string]*os.File
	eventsSinceCheckpoint int
	lastCheckpoint        time.Time
}

const (
	spoolCheckpointEveryEvents = 128
	spoolCheckpointEvery       = time.Second
)

type spoolShard struct {
	manager *Manager
	events  chan spoolEvent
	states  map[string]*spoolCaptureState
}

func newSpoolShard(manager *Manager, capacity int) *spoolShard {
	if capacity < 1 {
		capacity = 1
	}
	return &spoolShard{
		manager: manager,
		events:  make(chan spoolEvent, capacity),
		states:  make(map[string]*spoolCaptureState),
	}
}

func (s *spoolShard) run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case event := <-s.events:
			s.handleEvent(event)
			s.manager.queuedBytes.Add(-int64(len(event.data)))
			continue
		default:
		}
		select {
		case <-ctx.Done():
			s.drainOnStop()
			return
		case event := <-s.events:
			s.handleEvent(event)
			s.manager.queuedBytes.Add(-int64(len(event.data)))
		}
	}
}

func (s *spoolShard) handleEvent(event spoolEvent) {
	checkpoint := false
	forceCheckpoint := false
	switch event.kind {
	case spoolStart:
		s.handleStart(event)
		checkpoint = true
		forceCheckpoint = true
	case spoolClientResponseHeaders:
		if state := s.states[event.captureID]; state != nil {
			state.manifest.ClientResponse = event.headers
			checkpoint = true
			forceCheckpoint = true
		}
	case spoolClientResponseChunk:
		if state := s.states[event.captureID]; state != nil {
			offset := state.manifest.ClientResponseBytes
			if s.appendFile(state, "client/response.body", event.data) {
				state.manifest.ClientResponseFile = "client/response.body"
				state.manifest.ClientResponseBytes += int64(len(event.data))
				state.manifest.ClientResponseChunks = append(state.manifest.ClientResponseChunks, ChunkRecord{Offset: offset, Length: len(event.data), AtMS: event.atMS})
				checkpoint = true
			}
		}
	case spoolAttemptStart:
		if state := s.states[event.captureID]; state != nil && event.attempt != nil {
			state.manifest.Attempts = append(state.manifest.Attempts, *event.attempt)
			if len(event.data) > 0 {
				index := len(state.manifest.Attempts) - 1
				path := attemptRequestPath(event.attemptID)
				if s.appendFile(state, path, event.data) {
					state.manifest.Attempts[index].RequestFile = path
					state.manifest.Attempts[index].RequestBytes = int64(len(event.data))
				}
			}
			checkpoint = true
			forceCheckpoint = true
		}
	case spoolAttemptResponseHeaders:
		if attempt := s.attempt(event.captureID, event.attemptID); attempt != nil {
			attempt.ResponseHeaders = event.headers.Headers
			attempt.HTTPStatus = event.headers.Status
			checkpoint = true
			forceCheckpoint = true
		}
	case spoolAttemptResponseChunk:
		state := s.states[event.captureID]
		attempt := s.attempt(event.captureID, event.attemptID)
		if state != nil && attempt != nil {
			offset := attempt.ResponseBytes
			path := attemptResponsePath(event.attemptID)
			if s.appendFile(state, path, event.data) {
				attempt.ResponseFile = path
				attempt.ResponseBytes += int64(len(event.data))
				attempt.ResponseChunks = append(attempt.ResponseChunks, ChunkRecord{Offset: offset, Length: len(event.data), AtMS: event.atMS})
				checkpoint = true
			}
		}
	case spoolAttemptFinish:
		if attempt := s.attempt(event.captureID, event.attemptID); attempt != nil {
			finishedAt := time.Now().UTC()
			attempt.FinishedAt = &finishedAt
			attempt.Error = event.errText
			attempt.Complete = event.complete
			checkpoint = true
			forceCheckpoint = true
		}
	case spoolFinalize:
		s.handleFinalize(event.finalize)
		return
	}
	if checkpoint {
		if state := s.states[event.captureID]; state != nil {
			s.checkpoint(state, forceCheckpoint)
		}
	}
}

func (s *spoolShard) handleStart(event spoolEvent) {
	if event.manifest == nil || strings.TrimSpace(event.captureID) == "" {
		return
	}
	dir := filepath.Join(s.manager.openDir, event.captureID)
	if err := os.MkdirAll(filepath.Join(dir, "client"), 0o700); err != nil {
		slog.Error("training_data_spool_start_failed", "capture_id", event.captureID, "error", err)
		return
	}
	state := &spoolCaptureState{dir: dir, manifest: *event.manifest, files: make(map[string]*os.File)}
	s.states[event.captureID] = state
	if len(event.data) > 0 {
		if s.appendFile(state, "client/request.body", event.data) {
			state.manifest.ClientRequestFile = "client/request.body"
			state.manifest.ClientRequestBytes = int64(len(event.data))
		}
	}
}

func (s *spoolShard) checkpoint(state *spoolCaptureState, force bool) {
	if state == nil {
		return
	}
	state.eventsSinceCheckpoint++
	now := time.Now()
	if !force && state.eventsSinceCheckpoint < spoolCheckpointEveryEvents && now.Sub(state.lastCheckpoint) < spoolCheckpointEvery {
		return
	}
	partialManifest := filepath.Join(state.dir, "manifest.partial.json")
	beforeManifest := regularFileSize(partialManifest)
	if err := writeJSONAtomic(partialManifest, state.manifest); err != nil {
		state.manifest.IncompleteReasons = uniqueSortedStrings(append(state.manifest.IncompleteReasons, "partial_manifest_write_failed"))
		slog.Warn("training_data_spool_checkpoint_failed", "capture_id", state.manifest.CaptureID, "error", err)
		return
	}
	s.manager.spoolBytes.Add(regularFileSize(partialManifest) - beforeManifest)
	state.eventsSinceCheckpoint = 0
	state.lastCheckpoint = now
}

func (s *spoolShard) handleFinalize(event finalizeEvent) {
	state := s.states[event.captureID]
	if state == nil {
		return
	}
	for _, file := range state.files {
		_ = file.Sync()
		_ = file.Close()
	}
	state.files = nil
	state.manifest.FinishedAt = &event.finishedAt
	state.manifest.ClientResponse.Status = event.status
	state.manifest.ClientResponse.Headers = cloneHeaderMap(event.headers)
	state.manifest.IncompleteReasons = uniqueSortedStrings(append(state.manifest.IncompleteReasons, event.incompleteReason...))
	for index := range state.manifest.Attempts {
		if state.manifest.Attempts[index].FinishedAt == nil {
			state.manifest.Attempts[index].Complete = false
			state.manifest.IncompleteReasons = append(state.manifest.IncompleteReasons, fmt.Sprintf("attempt_%04d_unfinished", state.manifest.Attempts[index].AttemptID))
		}
	}
	state.manifest.IncompleteReasons = uniqueSortedStrings(state.manifest.IncompleteReasons)
	state.manifest.CaptureComplete = len(state.manifest.IncompleteReasons) == 0
	beforeCompression := directorySize(state.dir)
	if err := compressCaptureBodies(state.dir, &state.manifest); err != nil {
		state.manifest.CaptureComplete = false
		state.manifest.IncompleteReasons = uniqueSortedStrings(append(state.manifest.IncompleteReasons, "compression_failed"))
	}
	_ = os.Remove(filepath.Join(state.dir, "manifest.partial.json"))
	artifacts, artifactErr := buildCaptureArtifacts(state.dir)
	if artifactErr != nil {
		state.manifest.CaptureComplete = false
		state.manifest.IncompleteReasons = uniqueSortedStrings(append(state.manifest.IncompleteReasons, "artifact_hash_failed"))
	} else {
		state.manifest.Files = artifacts
	}
	if err := writeJSONAtomic(filepath.Join(state.dir, "manifest.json"), state.manifest); err != nil {
		state.manifest.CaptureComplete = false
		state.manifest.IncompleteReasons = uniqueSortedStrings(append(state.manifest.IncompleteReasons, "manifest_write_failed"))
		_ = writeJSONAtomic(filepath.Join(state.dir, "manifest.json"), state.manifest)
	}
	afterCompression := directorySize(state.dir)
	s.manager.spoolBytes.Add(afterCompression - beforeCompression)
	readyDir := filepath.Join(s.manager.readyDir, event.captureID)
	if err := os.Rename(state.dir, readyDir); err != nil {
		state.manifest.CaptureComplete = false
		state.manifest.IncompleteReasons = uniqueSortedStrings(append(state.manifest.IncompleteReasons, "ready_transition_failed"))
		manifestPath := filepath.Join(state.dir, "manifest.json")
		beforeManifest := regularFileSize(manifestPath)
		if writeErr := writeJSONAtomic(manifestPath, state.manifest); writeErr == nil {
			s.manager.spoolBytes.Add(regularFileSize(manifestPath) - beforeManifest)
		}
		slog.Warn("training_data_spool_ready_transition_failed", "capture_id", event.captureID, "error", err)
	}
	delete(s.states, event.captureID)
}

func (s *spoolShard) appendFile(state *spoolCaptureState, relative string, data []byte) bool {
	if state == nil || len(data) == 0 {
		return true
	}
	file := state.files[relative]
	if file == nil {
		fullPath := filepath.Join(state.dir, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
			state.manifest.IncompleteReasons = append(state.manifest.IncompleteReasons, "spool_mkdir_failed")
			return false
		}
		created, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			state.manifest.IncompleteReasons = append(state.manifest.IncompleteReasons, "spool_open_failed")
			return false
		}
		file = created
		state.files[relative] = file
	}
	written, err := file.Write(data)
	if written > 0 {
		s.manager.spoolBytes.Add(int64(written))
	}
	if err != nil || written != len(data) {
		state.manifest.IncompleteReasons = append(state.manifest.IncompleteReasons, "spool_write_failed")
		return false
	}
	return true
}

func (s *spoolShard) attempt(captureID string, attemptID int) *AttemptManifest {
	state := s.states[captureID]
	if state == nil {
		return nil
	}
	for index := range state.manifest.Attempts {
		if state.manifest.Attempts[index].AttemptID == attemptID {
			return &state.manifest.Attempts[index]
		}
	}
	return nil
}

func (s *spoolShard) drainOnStop() {
	for {
		select {
		case event := <-s.events:
			s.handleEvent(event)
			s.manager.queuedBytes.Add(-int64(len(event.data)))
		default:
		}
		select {
		case event := <-s.events:
			s.handleEvent(event)
			s.manager.queuedBytes.Add(-int64(len(event.data)))
		default:
			for captureID := range s.states {
				s.handleFinalize(finalizeEvent{
					captureID: captureID, finishedAt: time.Now().UTC(),
					incompleteReason: []string{"process_shutdown"},
				})
			}
			return
		}
	}
}

func attemptRequestPath(attemptID int) string {
	return fmt.Sprintf("upstream/attempt-%04d/request.body", attemptID)
}

func attemptResponsePath(attemptID int) string {
	return fmt.Sprintf("upstream/attempt-%04d/response.body", attemptID)
}

func compressCaptureBodies(root string, manifest *Manifest) error {
	if manifest == nil {
		return nil
	}
	paths := []*string{&manifest.ClientRequestFile, &manifest.ClientResponseFile}
	for index := range manifest.Attempts {
		paths = append(paths, &manifest.Attempts[index].RequestFile, &manifest.Attempts[index].ResponseFile)
	}
	for _, relative := range paths {
		if relative == nil || strings.TrimSpace(*relative) == "" || strings.HasSuffix(*relative, ".zst") {
			continue
		}
		compressed, err := compressFile(filepath.Join(root, filepath.FromSlash(*relative)))
		if err != nil {
			return err
		}
		*relative = filepath.ToSlash(strings.TrimPrefix(compressed, root+string(os.PathSeparator)))
	}
	return nil
}

func compressFile(filename string) (string, error) {
	source, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer source.Close()
	targetName := filename + ".zst"
	temporaryName := targetName + ".tmp"
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(temporaryName)
		}
	}()
	target, err := os.OpenFile(temporaryName, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	encoder, err := zstd.NewWriter(target, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		_ = target.Close()
		return "", err
	}
	_, copyErr := io.Copy(encoder, source)
	closeEncoderErr := encoder.Close()
	closeTargetErr := target.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeEncoderErr != nil {
		return "", closeEncoderErr
	}
	if closeTargetErr != nil {
		return "", closeTargetErr
	}
	if err := os.Rename(temporaryName, targetName); err != nil {
		return "", err
	}
	if err := os.Remove(filename); err != nil {
		return "", err
	}
	committed = true
	return targetName, nil
}

func writeJSONAtomic(filename string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		return err
	}
	temporary := filename + ".tmp"
	if err := os.WriteFile(temporary, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, filename)
}

func directorySize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func regularFileSize(filename string) int64 {
	info, err := os.Stat(filename)
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func cloneHeaderMap(headers httpHeaderAlias) http.Header {
	result := make(http.Header, len(headers))
	for key, values := range headers {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func recoverOpenCapture(dir, readyDir string) error {
	manifestPath := filepath.Join(dir, "manifest.partial.json")
	encoded, err := os.ReadFile(manifestPath)
	if err != nil {
		finalManifestPath := filepath.Join(dir, "manifest.json")
		encoded, err = os.ReadFile(finalManifestPath)
		if err != nil {
			return err
		}
		var finalized Manifest
		if err := json.Unmarshal(encoded, &finalized); err != nil {
			return err
		}
		if strings.TrimSpace(finalized.CaptureID) == "" {
			return fmt.Errorf("finalized capture manifest has no capture_id")
		}
		destination := filepath.Join(readyDir, filepath.Base(dir))
		if _, statErr := os.Lstat(destination); statErr == nil {
			return fmt.Errorf("ready capture %q already exists", destination)
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		return os.Rename(dir, destination)
	}
	var manifest Manifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return err
	}
	finishedAt := time.Now().UTC()
	manifest.FinishedAt = &finishedAt
	manifest.CaptureComplete = false
	manifest.IncompleteReasons = uniqueSortedStrings(append(manifest.IncompleteReasons, "process_restart"))
	reconcileOpenCaptureFiles(dir, &manifest)
	if err := compressCaptureBodies(dir, &manifest); err != nil {
		manifest.IncompleteReasons = uniqueSortedStrings(append(manifest.IncompleteReasons, "compression_failed"))
	}
	_ = os.Remove(manifestPath)
	artifacts, artifactErr := buildCaptureArtifacts(dir)
	if artifactErr != nil {
		manifest.IncompleteReasons = uniqueSortedStrings(append(manifest.IncompleteReasons, "artifact_hash_failed"))
	} else {
		manifest.Files = artifacts
	}
	if err := writeJSONAtomic(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return err
	}
	destination := filepath.Join(readyDir, filepath.Base(dir))
	if _, statErr := os.Lstat(destination); statErr == nil {
		return fmt.Errorf("ready capture %q already exists", destination)
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	return os.Rename(dir, destination)
}

func reconcileOpenCaptureFiles(dir string, manifest *Manifest) {
	if manifest == nil {
		return
	}
	reconcileBody := func(relative string, currentFile *string, currentBytes *int64, chunks *[]ChunkRecord) {
		filename := filepath.Join(dir, filepath.FromSlash(relative))
		size := regularFileSize(filename)
		if size <= 0 {
			return
		}
		*currentFile = relative
		if size > *currentBytes {
			missing := size - *currentBytes
			*chunks = append(*chunks, ChunkRecord{Offset: *currentBytes, Length: int(missing), AtMS: -1})
			*currentBytes = size
		}
	}
	clientRequest := filepath.Join(dir, "client", "request.body")
	if size := regularFileSize(clientRequest); size > 0 {
		manifest.ClientRequestFile = "client/request.body"
		manifest.ClientRequestBytes = size
	}
	reconcileBody("client/response.body", &manifest.ClientResponseFile, &manifest.ClientResponseBytes, &manifest.ClientResponseChunks)
	for index := range manifest.Attempts {
		attempt := &manifest.Attempts[index]
		requestPath := attemptRequestPath(attempt.AttemptID)
		if size := regularFileSize(filepath.Join(dir, filepath.FromSlash(requestPath))); size > 0 {
			attempt.RequestFile = requestPath
			attempt.RequestBytes = size
		}
		responsePath := attemptResponsePath(attempt.AttemptID)
		reconcileBody(responsePath, &attempt.ResponseFile, &attempt.ResponseBytes, &attempt.ResponseChunks)
	}
}

func buildCaptureArtifacts(root string) (map[string]CaptureArtifact, error) {
	artifacts := make(map[string]CaptureArtifact)
	err := filepath.WalkDir(root, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("capture artifact %q must not be a symlink", filename)
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "manifest.json" || relative == "manifest.partial.json" || strings.HasSuffix(relative, ".tmp") {
			return nil
		}
		artifact, err := hashFile(filename)
		if err != nil {
			return err
		}
		artifacts[relative] = CaptureArtifact{
			SHA256: artifact.SHA256, Bytes: artifact.Bytes, ContentType: artifact.ContentType,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return artifacts, nil
}

func verifyCaptureArtifacts(root string, manifest Manifest) error {
	if manifest.Files == nil {
		return fmt.Errorf("capture manifest has no artifact hashes")
	}
	actual, err := buildCaptureArtifacts(root)
	if err != nil {
		return err
	}
	if len(actual) != len(manifest.Files) {
		return fmt.Errorf("capture artifact count mismatch: manifest=%d actual=%d", len(manifest.Files), len(actual))
	}
	for name, expected := range manifest.Files {
		got, ok := actual[name]
		if !ok {
			return fmt.Errorf("capture artifact %q is missing", name)
		}
		if got.SHA256 != expected.SHA256 || got.Bytes != expected.Bytes {
			return fmt.Errorf("capture artifact %q failed SHA-256/size verification", name)
		}
	}
	return nil
}
