package trainingdata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

type Manager struct {
	cfg            config.TrainingDataConfig
	subjectHMACKey []byte
	db             *sql.DB
	rawStore       ObjectStore
	spoolDir       string
	openDir        string
	readyDir       string
	shards         []*spoolShard
	rights         atomicRightsSnapshot
	active         atomic.Bool
	started        atomic.Bool
	spoolBytes     atomic.Int64
	queuedBytes    atomic.Int64
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

func NewManager(db *sql.DB, cfg *config.Config) *Manager {
	manager := &Manager{}
	if cfg == nil {
		return manager
	}
	manager.db = db
	manager.cfg = cfg.TrainingData
	manager.subjectHMACKey = []byte(strings.TrimSpace(cfg.TrainingData.SubjectHMACKey))
	manager.spoolDir = strings.TrimSpace(cfg.TrainingData.SpoolDir)
	manager.openDir = filepath.Join(manager.spoolDir, "open")
	manager.readyDir = filepath.Join(manager.spoolDir, "ready")
	return manager
}

func (m *Manager) Enabled() bool {
	return m != nil && m.cfg.Enabled
}

func (m *Manager) Active() bool {
	return m != nil && m.active.Load()
}

func (m *Manager) Start(parent context.Context) error {
	if m == nil || !m.cfg.Enabled {
		return nil
	}
	if !m.started.CompareAndSwap(false, true) {
		return nil
	}
	started := false
	defer func() {
		if !started {
			m.started.Store(false)
		}
	}()
	if parent == nil {
		parent = context.Background()
	}
	if m.db == nil {
		return errors.New("training data requires the application database")
	}
	store, err := NewS3ObjectStore(parent, m.cfg.RawStore)
	if err != nil {
		return err
	}
	if err := store.HeadBucket(parent); err != nil {
		return err
	}
	m.rawStore = store
	if err := os.MkdirAll(m.openDir, 0o700); err != nil {
		return fmt.Errorf("create training data open spool: %w", err)
	}
	if err := os.MkdirAll(m.readyDir, 0o700); err != nil {
		return fmt.Errorf("create training data ready spool: %w", err)
	}
	if err := m.recoverOpenCaptures(); err != nil {
		slog.Warn("training_data_spool_recovery_incomplete", "error", err)
	}
	m.spoolBytes.Store(directorySize(m.spoolDir))
	refreshCtx, cancel := context.WithTimeout(parent, 5*time.Second)
	err = m.refreshRights(refreshCtx)
	cancel()
	if err != nil {
		return err
	}
	m.ctx, m.cancel = context.WithCancel(parent)
	shardCount := m.cfg.WriterShards
	if shardCount <= 0 {
		shardCount = 1
	}
	perShardCapacity := (m.cfg.QueueCapacity + shardCount - 1) / shardCount
	m.shards = make([]*spoolShard, 0, shardCount)
	for index := 0; index < shardCount; index++ {
		shard := newSpoolShard(m, perShardCapacity)
		m.shards = append(m.shards, shard)
		m.wg.Add(1)
		go shard.run(m.ctx, &m.wg)
	}
	m.wg.Add(2)
	go m.rightsRefreshLoop()
	go m.uploadLoop()
	m.active.Store(true)
	started = true
	slog.Info("training_data_capture_started",
		"writer_shards", shardCount,
		"queue_capacity", m.cfg.QueueCapacity,
		"queue_max_bytes", m.cfg.QueueMaxBytes,
		"spool_dir", m.spoolDir,
		"spool_max_bytes", m.cfg.SpoolMaxBytes,
	)
	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	if m == nil || !m.started.Load() {
		return nil
	}
	m.active.Store(false)
	if m.cancel != nil {
		m.cancel()
	}
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (m *Manager) Begin(input BeginInput) *Session {
	if !m.Active() || strings.TrimSpace(input.UserSubjectRef) == "" {
		return nil
	}
	grant, eligible := m.eligibleGrant(input.UserSubjectRef, input.APIKeySubjectRef, time.Now().UTC())
	if !eligible && !m.cfg.CaptureUnknownRights {
		return nil
	}
	rightsStatus := RightsUnknown
	rightsRef := ""
	if eligible {
		rightsStatus = grant.Status
		rightsRef = grant.RightsID
	}
	now := time.Now().UTC()
	captureID := uuid.NewString()
	manifest := &Manifest{
		SchemaVersion:     "capture-v1",
		CaptureID:         captureID,
		RequestID:         strings.TrimSpace(input.RequestID),
		ClientRequestID:   strings.TrimSpace(input.ClientRequestID),
		UserSubjectRef:    strings.TrimSpace(input.UserSubjectRef),
		APIKeySubjectRef:  strings.TrimSpace(input.APIKeySubjectRef),
		RightsID:          rightsRef,
		RightsVersion:     grant.Version,
		RightsStatus:      rightsStatus,
		StartedAt:         now,
		Method:            strings.TrimSpace(input.Method),
		Route:             strings.TrimSpace(input.Route),
		Protocol:          strings.TrimSpace(input.Protocol),
		ClientModel:       strings.TrimSpace(input.ClientModel),
		Stream:            input.Stream,
		ClientRequest:     HeaderSnapshot{Headers: SanitizeHeaders(input.Headers)},
		IncompleteReasons: uniqueSortedStrings(input.IncompleteReasons),
		RedactionVersion:  RedactionVersion,
	}
	event := spoolEvent{kind: spoolStart, captureID: captureID, manifest: manifest, data: append([]byte(nil), input.RequestBody...)}
	if !m.tryEnqueue(event) {
		return nil
	}
	return &Session{
		manager:   m,
		captureID: captureID,
		startedAt: now,
		reasons:   make(map[string]struct{}),
	}
}

func (m *Manager) tryEnqueue(event spoolEvent) bool {
	if !m.Active() || len(m.shards) == 0 {
		return false
	}
	dataBytes := int64(len(event.data))
	if dataBytes > 0 {
		queueMaxBytes := m.cfg.QueueMaxBytes
		if queueMaxBytes <= 0 {
			queueMaxBytes = m.cfg.SpoolMaxBytes
		}
		for {
			queued := m.queuedBytes.Load()
			if queued+dataBytes > queueMaxBytes || m.spoolBytes.Load()+queued+dataBytes > m.cfg.SpoolMaxBytes {
				return false
			}
			if m.queuedBytes.CompareAndSwap(queued, queued+dataBytes) {
				break
			}
		}
	}
	shard := m.shardFor(event.captureID)
	select {
	case shard.events <- event:
		return true
	default:
		if dataBytes > 0 {
			m.queuedBytes.Add(-dataBytes)
		}
		return false
	}
}

func (m *Manager) enqueueFinalize(event finalizeEvent) {
	if m == nil || len(m.shards) == 0 {
		return
	}
	shard := m.shardFor(event.captureID)
	queued := spoolEvent{kind: spoolFinalize, captureID: event.captureID, finalize: event}
	select {
	case shard.events <- queued:
	default:
		go func() {
			select {
			case shard.events <- queued:
			case <-m.ctx.Done():
			}
		}()
	}
}

func (m *Manager) shardFor(captureID string) *spoolShard {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(captureID))
	return m.shards[int(hash.Sum32())%len(m.shards)]
}

func (m *Manager) rightsRefreshLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(time.Duration(m.cfg.RightsRefreshSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
			err := m.refreshRights(ctx)
			cancel()
			if err != nil {
				slog.Warn("training_data_rights_refresh_failed", "error", err)
			}
		}
	}
}

func (m *Manager) uploadLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(time.Duration(m.cfg.UploadIntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		m.uploadReadyCaptures(m.ctx)
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Manager) uploadReadyCaptures(ctx context.Context) {
	entries, err := os.ReadDir(m.readyDir)
	if err != nil {
		return
	}
	workerCount := m.cfg.UploadConcurrency
	if workerCount <= 0 {
		workerCount = 1
	}
	if workerCount > len(entries) {
		workerCount = len(entries)
	}
	if workerCount == 0 {
		return
	}
	jobs := make(chan string)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for index := 0; index < workerCount; index++ {
		go func() {
			defer workers.Done()
			for dir := range jobs {
				if err := m.uploadCaptureDir(ctx, dir); err != nil {
					slog.Warn("training_data_capture_upload_failed", "capture_id", filepath.Base(dir), "error", err)
				}
			}
		}()
	}

dispatchLoop:
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		select {
		case jobs <- filepath.Join(m.readyDir, entry.Name()):
		case <-ctx.Done():
			break dispatchLoop
		}
	}
	close(jobs)
	workers.Wait()
}

func (m *Manager) uploadCaptureDir(ctx context.Context, dir string) error {
	encoded, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}
	var manifest Manifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return err
	}
	if err := verifyCaptureArtifacts(dir, manifest); err != nil {
		return fmt.Errorf("verify capture before upload: %w", err)
	}
	prefix := captureObjectPrefix(manifest.StartedAt, manifest.CaptureID)
	var files []string
	err = filepath.WalkDir(dir, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			files = append(files, filename)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(files, func(i, j int) bool {
		return filepath.Base(files[i]) != "manifest.json" && filepath.Base(files[j]) == "manifest.json"
	})
	for _, filename := range files {
		relative, err := filepath.Rel(dir, filename)
		if err != nil {
			return err
		}
		key := prefix + "/" + filepath.ToSlash(relative)
		contentType := mime.TypeByExtension(filepath.Ext(filename))
		if strings.HasSuffix(filename, ".zst") {
			contentType = "application/zstd"
		} else if strings.HasSuffix(filename, ".json") {
			contentType = "application/json"
		}
		if _, err := m.rawStore.UploadFile(ctx, key, filename, contentType); err != nil {
			return err
		}
	}
	index := captureIndexFromManifest(manifest, prefix)
	if err := upsertCaptureIndex(ctx, m.db, index); err != nil {
		return err
	}
	size := directorySize(dir)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	m.spoolBytes.Add(-size)
	return nil
}

func captureIndexFromManifest(manifest Manifest, prefix string) CaptureIndex {
	finishedAt := manifest.StartedAt
	if manifest.FinishedAt != nil {
		finishedAt = *manifest.FinishedAt
	}
	upstreamModels := make([]string, 0, len(manifest.Attempts))
	var upstreamRequestBytes, upstreamResponseBytes int64
	for _, attempt := range manifest.Attempts {
		if strings.TrimSpace(attempt.Model) != "" {
			upstreamModels = append(upstreamModels, attempt.Model)
		}
		upstreamRequestBytes += attempt.RequestBytes
		upstreamResponseBytes += attempt.ResponseBytes
	}
	status := "complete"
	if !manifest.CaptureComplete {
		status = "incomplete"
	}
	return CaptureIndex{
		CaptureID: manifest.CaptureID, RequestID: manifest.RequestID, ClientRequestID: manifest.ClientRequestID,
		UserSubjectRef: manifest.UserSubjectRef, APIKeySubjectRef: manifest.APIKeySubjectRef,
		RightsID: manifest.RightsID, RightsVersion: manifest.RightsVersion,
		RightsStatus: manifest.RightsStatus, StartedAt: manifest.StartedAt, FinishedAt: finishedAt,
		Route: manifest.Route, Method: manifest.Method, Protocol: manifest.Protocol, ClientModel: manifest.ClientModel,
		UpstreamModels: uniqueSortedStrings(upstreamModels), Stream: manifest.Stream,
		HTTPStatus: manifest.ClientResponse.Status, DurationMS: finishedAt.Sub(manifest.StartedAt).Milliseconds(),
		AttemptCount: len(manifest.Attempts), CaptureComplete: manifest.CaptureComplete, CaptureStatus: status,
		IncompleteReasons: manifest.IncompleteReasons, RequestBytes: manifest.ClientRequestBytes,
		UpstreamRequestBytes: upstreamRequestBytes, UpstreamResponseBytes: upstreamResponseBytes,
		ClientResponseBytes: manifest.ClientResponseBytes, RawObjectPrefix: prefix,
		RawManifestKey: prefix + "/manifest.json", RedactionVersion: manifest.RedactionVersion,
	}
}

func captureObjectPrefix(startedAt time.Time, captureID string) string {
	startedAt = startedAt.UTC()
	return fmt.Sprintf("captures/%04d/%02d/%02d/%02d/%s", startedAt.Year(), startedAt.Month(), startedAt.Day(), startedAt.Hour(), captureID)
}

func (m *Manager) recoverOpenCaptures() error {
	entries, err := os.ReadDir(m.openDir)
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := recoverOpenCapture(filepath.Join(m.openDir, entry.Name()), m.readyDir); err != nil {
			errs = append(errs, fmt.Errorf("recover %s: %w", entry.Name(), err))
		}
	}
	return errors.Join(errs...)
}
