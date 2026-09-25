package service

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const (
	usageSettlementIngressMinFreeBytes    uint64 = 32 << 20
	usageSettlementIngressReadinessTTL           = time.Second
	usageSettlementIngressHealthScanLimit        = 256
)

// Counts describe the files observed during a bounded scan, not an atomic
// snapshot. ScanComplete=false must never imply unseen files are healthy.
// Monitoring should keep polling; a scan holds at most one directory descriptor.
type UsageSettlementIngressHealth struct {
	Enabled              bool      `json:"enabled"`
	Ready                bool      `json:"ready"`
	Pending              int64     `json:"pending"`
	Invalid              int64     `json:"invalid"`
	OldestPendingSeconds float64   `json:"oldest_pending_seconds"`
	AvailableBytes       uint64    `json:"available_bytes"`
	MinAvailableBytes    uint64    `json:"min_available_bytes"`
	ScanComplete         bool      `json:"scan_complete"`
	Scanned              int64     `json:"scanned"`
	CheckedAt            time.Time `json:"checked_at"`
}

type usageSettlementIngressReadiness struct {
	mu             sync.Mutex
	checkedAt      time.Time
	availableBytes uint64
	err            error
}

var usageSettlementIngressReadinessCache sync.Map

type usageSettlementIngressProbe func(*config.Config) (uint64, error)

// checkUsageSettlementIngressReady is cheap on the request path: one cached
// readiness result per data directory for at most one second. Neither directory
// enumeration nor one fsync per incoming request is performed here.
func checkUsageSettlementIngressReady(cfg *config.Config) error {
	_, _, err := usageSettlementIngressReadinessResult(cfg, time.Now(), probeUsageSettlementIngress)
	return err
}

func usageSettlementIngressConfigKey(cfg *config.Config) (string, error) {
	if cfg == nil || strings.TrimSpace(cfg.Pricing.DataDir) == "" {
		return "", nil
	}
	return filepath.Abs(filepath.Clean(cfg.Pricing.DataDir))
}

func usageSettlementIngressReadinessResult(cfg *config.Config, now time.Time, probe usageSettlementIngressProbe) (uint64, time.Time, error) {
	key, err := usageSettlementIngressConfigKey(cfg)
	if err != nil {
		return 0, now, err
	}
	if key == "" {
		return 0, now, nil
	}
	value, _ := usageSettlementIngressReadinessCache.LoadOrStore(key, &usageSettlementIngressReadiness{})
	state := value.(*usageSettlementIngressReadiness)
	waitStarted := time.Now()
	state.mu.Lock()
	defer state.mu.Unlock()
	// Include waiting for another probe in the freshness calculation. A
	// request queued before a slow fsync must not reuse an already stale result.
	now = now.Add(time.Since(waitStarted))
	if now.Before(state.checkedAt) {
		// A concurrent caller can have sampled its clock just before the caller
		// which populated the cache; never move the monotonic checkpoint back.
		now = state.checkedAt
	}
	age := now.Sub(state.checkedAt)
	if !state.checkedAt.IsZero() && age >= 0 && age < usageSettlementIngressReadinessTTL {
		return state.availableBytes, state.checkedAt, state.err
	}
	// Timestamp the start, not the end: a slow failed probe must not extend the
	// permitted staleness and accidentally admit work on an old success.
	state.checkedAt = now
	probeStarted := time.Now()
	state.availableBytes, state.err = probe(cfg)
	if state.err == nil && time.Since(probeStarted) >= usageSettlementIngressReadinessTTL {
		state.err = errors.New("settlement ingress persistence probe exceeded freshness deadline")
	}
	return state.availableBytes, state.checkedAt, state.err
}

// This opener never creates/chmods directories and never fsyncs. Startup owns
// provisioning; the preflight must reject a changed or missing mounted path.
func openExistingUsageSettlementIngress(cfg *config.Config) (*os.Root, error) {
	key, err := usageSettlementIngressConfigKey(cfg)
	if err != nil || key == "" {
		return nil, err
	}
	before, err := os.Lstat(key)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("settlement data directory is not a real directory")
	}
	data, err := os.OpenRoot(key)
	if err != nil {
		return nil, err
	}
	defer data.Close()
	after, err := data.Stat(".")
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) {
		return nil, errors.New("settlement data directory changed")
	}
	before, err = data.Lstat(usageSettlementIngressDir)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || before.Mode().Perm()&0077 != 0 {
		return nil, errors.New("settlement ingress directory is not private or is invalid")
	}
	root, err := data.OpenRoot(usageSettlementIngressDir)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.Root, error) { _ = root.Close(); return nil, err }
	after, err = root.Stat(".")
	if err != nil {
		return fail(err)
	}
	if !os.SameFile(before, after) {
		return fail(errors.New("settlement ingress directory changed"))
	}
	locks, err := root.Lstat(".locks")
	if err != nil {
		return fail(err)
	}
	if !locks.IsDir() || locks.Mode()&os.ModeSymlink != 0 || locks.Mode().Perm()&0077 != 0 {
		return fail(errors.New("settlement ingress lock directory is not private or is invalid"))
	}
	return root, nil
}

func checkUsageSettlementIngressFreeBytes(available uint64) error {
	if available < usageSettlementIngressMinFreeBytes {
		return fmt.Errorf("settlement ingress free space below %d-byte safety reserve", usageSettlementIngressMinFreeBytes)
	}
	return nil
}

func probeUsageSettlementIngress(cfg *config.Config) (uint64, error) {
	root, err := openExistingUsageSettlementIngress(cfg)
	if err != nil {
		return 0, fmt.Errorf("settlement ingress unavailable: %w", err)
	}
	if root == nil {
		return 0, nil
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return 0, err
	}
	defer directory.Close()
	var fs unix.Statfs_t
	if err = unix.Fstatfs(int(directory.Fd()), &fs); err != nil {
		return 0, fmt.Errorf("settlement ingress free-space check: %w", err)
	}
	available := uint64(fs.Bavail) * uint64(fs.Bsize)
	if err = checkUsageSettlementIngressFreeBytes(available); err != nil {
		return available, err
	}
	// A successful statfs alone cannot establish a writable mount or free inode.
	// One private small durable probe per cache interval verifies both data and
	// directory persistence without relying on mode bits/root permission bypass.
	name := ".readiness-" + uuid.NewString() + ".tmp"
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return available, fmt.Errorf("settlement ingress write probe: %w", err)
	}
	defer func() { _ = file.Close(); _ = root.Remove(name) }()
	if _, err = file.Write([]byte("settlement-ingress-ready\n")); err != nil {
		return available, fmt.Errorf("settlement ingress write probe: %w", err)
	}
	if err = file.Sync(); err != nil {
		return available, fmt.Errorf("settlement ingress sync probe: %w", err)
	}
	if err = file.Close(); err != nil {
		return available, err
	}
	if err = directory.Sync(); err != nil {
		return available, err
	}
	if err = root.Remove(name); err != nil {
		return available, err
	}
	if err = directory.Sync(); err != nil {
		return available, err
	}
	return available, nil
}

type usageSettlementIngressHealthCursor struct {
	mu                        sync.Mutex
	directory                 *os.File
	pending, invalid, scanned int64
	oldest                    time.Time
}

var usageSettlementIngressHealthCursors sync.Map

func usageSettlementIngressHealth(cfg *config.Config) (UsageSettlementIngressHealth, error) {
	var stats UsageSettlementIngressHealth
	key, err := usageSettlementIngressConfigKey(cfg)
	if err != nil {
		return stats, err
	}
	if key == "" {
		stats.Ready = true
		stats.ScanComplete = true
		return stats, nil
	}
	stats.Enabled = true
	stats.MinAvailableBytes = usageSettlementIngressMinFreeBytes
	stats.AvailableBytes, stats.CheckedAt, err = usageSettlementIngressReadinessResult(cfg, time.Now(), probeUsageSettlementIngress)
	stats.Ready = err == nil
	readinessErr := err
	root, err := openExistingUsageSettlementIngress(cfg)
	if err != nil {
		return stats, errors.Join(readinessErr, err)
	}
	defer root.Close()
	value, _ := usageSettlementIngressHealthCursors.LoadOrStore(key, &usageSettlementIngressHealthCursor{})
	cursor := value.(*usageSettlementIngressHealthCursor)
	cursor.mu.Lock()
	defer cursor.mu.Unlock()
	if cursor.directory != nil {
		current, e := root.Stat(".")
		opened, e2 := cursor.directory.Stat()
		if e != nil || e2 != nil || !os.SameFile(current, opened) {
			_ = cursor.directory.Close()
			cursor.directory = nil
		}
	}
	if cursor.directory == nil {
		cursor.pending, cursor.invalid, cursor.scanned = 0, 0, 0
		cursor.oldest = time.Time{}
		cursor.directory, err = root.Open(".")
		if err != nil {
			return stats, errors.Join(readinessErr, err)
		}
	}
	entries, scanErr := cursor.directory.ReadDir(usageSettlementIngressHealthScanLimit)
	for _, entry := range entries {
		cursor.scanned++
		name := entry.Name()
		if len(name) != 69 || !strings.HasSuffix(name, ".json") {
			continue
		}
		if _, e := hex.DecodeString(name[:64]); e != nil {
			continue
		}
		info, e := root.Lstat(name)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			cursor.invalid++
			continue
		}
		cursor.pending++
		if cursor.oldest.IsZero() || info.ModTime().Before(cursor.oldest) {
			cursor.oldest = info.ModTime()
		}
		// Validate the persisted evidence without invoking SQL or settlement. A
		// concurrent acknowledgment can remove the file; that is not corruption.
		if _, e = readUsageSettlementIngress(root, name); e != nil && !errors.Is(e, os.ErrNotExist) {
			cursor.invalid++
		}
	}
	stats.Pending, stats.Invalid, stats.Scanned = cursor.pending, cursor.invalid, cursor.scanned
	if !cursor.oldest.IsZero() {
		stats.OldestPendingSeconds = math.Max(0, time.Since(cursor.oldest).Seconds())
	}
	if errors.Is(scanErr, io.EOF) || len(entries) < usageSettlementIngressHealthScanLimit {
		stats.ScanComplete = true
		_ = cursor.directory.Close()
		cursor.directory = nil
		if errors.Is(scanErr, io.EOF) {
			scanErr = nil
		}
	}
	return stats, errors.Join(readinessErr, scanErr)
}

// The interface remains deliberately narrow; readiness and health never call
// PrepareSettlement, Apply, or any other operation that can change finances.
