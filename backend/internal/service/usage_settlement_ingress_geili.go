package service

// Geili ingress WAL bridges the gap between final upstream usage and a reachable
// SQL database. Files contain only the financial command and whitelisted usage
// fields; this layer never applies a debit. Successful SQL preparation is the
// only acknowledgment that permits removal.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

var ErrUsageSettlementIngressDeferred = errors.New("usage settlement retained in durable ingress for SQL retry")

const (
	usageSettlementIngressDir      = "usage-settlement-ingress"
	usageSettlementIngressVersion  = 1
	usageSettlementIngressMaxBytes = 4 << 20
)

type UsageSettlementIngressReplayStats struct {
	Scanned  int `json:"scanned"`
	Prepared int `json:"prepared"`
	Deferred int `json:"deferred"`
	Invalid  int `json:"invalid"`
}

type usageSettlementIngressRecord struct {
	Version int                    `json:"version"`
	Command UsageBillingCommand    `json:"command"`
	Detail  *UsageSettlementDetail `json:"detail"`
}

// ensureUsageSettlementIngress validates the configured mounted data directory.
// Empty configuration is supported only by legacy in-memory test fixtures; the
// production provider must call this with the loaded nonempty default data dir.
func ensureUsageSettlementIngress(cfg *config.Config) error {
	root, err := openUsageSettlementIngress(cfg)
	if root != nil {
		defer root.Close()
	}
	return err
}

func openUsageSettlementIngress(cfg *config.Config) (*os.Root, error) {
	if cfg == nil || strings.TrimSpace(cfg.Pricing.DataDir) == "" {
		return nil, nil
	}
	// Startup normally provisions this directory. Established requests only
	// validate/open it; unnecessary mkdir/chmod/fsync amplifies burst latency.
	if root, err := openExistingUsageSettlementIngress(cfg); err == nil {
		return root, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	base := filepath.Clean(cfg.Pricing.DataDir)
	if err := os.MkdirAll(base, 0700); err != nil {
		return nil, fmt.Errorf("create settlement data directory: %w", err)
	}
	before, err := os.Lstat(base)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("settlement data directory must be a real directory")
	}
	data, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer data.Close()
	after, err := data.Stat(".")
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) {
		return nil, errors.New("settlement data directory changed while opening")
	}
	if err = data.Mkdir(usageSettlementIngressDir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	before, err = data.Lstat(usageSettlementIngressDir)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("settlement ingress must not be a symlink")
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
		return fail(errors.New("settlement ingress directory changed while opening"))
	}
	directory, err := root.Open(".")
	if err != nil {
		return fail(err)
	}
	if err = directory.Chmod(0700); err == nil {
		err = directory.Sync()
	}
	_ = directory.Close()
	if err != nil {
		return fail(err)
	}
	if err = root.Mkdir(".locks", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return fail(err)
	}
	lockInfo, err := root.Lstat(".locks")
	if err != nil {
		return fail(err)
	}
	if !lockInfo.IsDir() || lockInfo.Mode()&os.ModeSymlink != 0 {
		return fail(errors.New("settlement ingress locks must not be a symlink"))
	}
	if err = syncUsageSettlementDirectory(data); err != nil {
		return fail(err)
	}
	return root, nil
}

func syncUsageSettlementDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func settlementIngressFilename(cmd *UsageBillingCommand) string {
	identity := strconv.FormatInt(cmd.APIKeyID, 10) + "\x00" + cmd.RequestID
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:]) + ".json"
}

func freezeUsageSettlementIngress(cmd *UsageBillingCommand, log *UsageLog) (*usageSettlementIngressRecord, error) {
	if cmd == nil {
		return nil, errors.New("nil settlement ingress command")
	}
	command := *cmd
	command.Normalize()
	if log == nil {
		log = cmd.UsageDetail
	}
	command.UsageDetail = log
	if err := ValidateUsageSettlementCommand(&command); err != nil {
		return nil, err
	}
	if command.TerminalFailure && log != nil {
		return nil, errors.New("terminal failure cannot carry usage detail")
	}
	if command.CompletedAt.IsZero() {
		command.CompletedAt = time.Now().UTC().Truncate(time.Microsecond)
	}
	detail, err := NewUsageSettlementDetail(log)
	if err != nil {
		return nil, err
	}
	if detail != nil {
		if detail.UserID != command.UserID || detail.APIKeyID != command.APIKeyID || detail.AccountID != command.AccountID || !ingressSameOptionalID(detail.SubscriptionID, command.SubscriptionID) {
			return nil, ErrUsageBillingRequestConflict
		}
		if strings.TrimSpace(detail.RequestID) == "" {
			return nil, ErrUsageBillingRequestIDRequired
		}
		detail.ActualCost = QuantizeUsageBillingAmount(command.BalanceCost + command.SubscriptionCost)
		if detail.RouteBillingSnapshot != nil {
			detail.RouteBillingSnapshot.ActualCost = detail.ActualCost
		}
		if detail.CreatedAt.IsZero() {
			detail.CreatedAt = command.CompletedAt
		}
	}
	command.UsageDetail = nil
	record := &usageSettlementIngressRecord{Version: usageSettlementIngressVersion, Command: command, Detail: detail}
	// Freeze all pointers/maps so request cleanup cannot alter the queued evidence.
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if len(raw) > usageSettlementIngressMaxBytes {
		return nil, errors.New("settlement ingress record exceeds size limit")
	}
	var frozen usageSettlementIngressRecord
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return nil, err
	}
	return &frozen, nil
}

func ingressSameOptionalID(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func sameUsageSettlementIngressFinancial(a, b *usageSettlementIngressRecord) bool {
	left, right := a.Command, b.Command
	left.CompletedAt, right.CompletedAt = time.Time{}, time.Time{}
	left.UsageDetail, right.UsageDetail = nil, nil
	l, e1 := json.Marshal(left)
	r, e2 := json.Marshal(right)
	if e1 != nil || e2 != nil || !bytes.Equal(l, r) {
		return false
	}
	if a.Detail == nil || b.Detail == nil {
		return a.Detail == nil && b.Detail == nil
	}
	// Timings may differ on retries; preserve the original full detail, while
	// refusing a financial ID that points to a different log or quota owner.
	return a.Detail.RequestID == b.Detail.RequestID && a.Detail.UserID == b.Detail.UserID && a.Detail.APIKeyID == b.Detail.APIKeyID && a.Detail.AccountID == b.Detail.AccountID && ingressSameOptionalID(a.Detail.SubscriptionID, b.Detail.SubscriptionID) && ingressSameOptionalID(a.Detail.GroupID, b.Detail.GroupID) && a.Detail.ActualCost == b.Detail.ActualCost
}

// acquireUsageSettlementIngressLock uses bounded, stable lock stripes. Locks
// outlive records so unlink/recreate cannot let another instance erase a newer
// record while acknowledging the earlier one. No secrets enter lock filenames.
var usageSettlementIngressLocalLocks sync.Map

func acquireUsageSettlementIngressLock(ctx context.Context, root *os.Root, name string) (func(), error) {
	// Serialize same-process waiters before flock. This avoids 128 polling
	// goroutines starving one another on an unfair advisory lock during bursts.
	key := root.Name() + "/" + name[:2]
	candidate := make(chan struct{}, 1)
	value, _ := usageSettlementIngressLocalLocks.LoadOrStore(key, candidate)
	queue := value.(chan struct{})
	select {
	case queue <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	releaseLocal := func() { <-queue }
	unlock, err := acquireUsageSettlementIngressFileLock(ctx, root, name)
	if err != nil {
		releaseLocal()
		return nil, err
	}
	return func() { unlock(); releaseLocal() }, nil
}

func acquireUsageSettlementIngressFileLock(ctx context.Context, root *os.Root, name string) (func(), error) {
	lockDirectory, err := root.OpenFile(".locks", os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer lockDirectory.Close()
	if err = lockDirectory.Chmod(0700); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(lockDirectory.Fd()), name[:2]+".lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name[:2]+".lock")
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("settlement lock must be a regular file")
	}
	if err = file.Chmod(0600); err != nil {
		_ = file.Close()
		return nil, err
	}
	for {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN); _ = file.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func readUsageSettlementIngress(root *os.Root, name string) (*usageSettlementIngressRecord, error) {
	file, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > usageSettlementIngressMaxBytes {
		return nil, errors.New("invalid settlement ingress file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, usageSettlementIngressMaxBytes+1))
	decoder.DisallowUnknownFields()
	var record usageSettlementIngressRecord
	if err = decoder.Decode(&record); err != nil {
		return nil, fmt.Errorf("decode settlement ingress record: %w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing settlement ingress data")
	}
	if record.Version != usageSettlementIngressVersion {
		return nil, errors.New("unsupported settlement ingress version")
	}
	record.Command.Normalize()
	if err = ValidateUsageSettlementCommand(&record.Command); err != nil {
		return nil, err
	}
	if settlementIngressFilename(&record.Command) != name {
		return nil, errors.New("settlement ingress filename identity mismatch")
	}
	if _, err = freezeUsageSettlementIngress(&record.Command, record.Detail.UsageLog()); err != nil {
		return nil, err
	}
	return &record, nil
}

func persistUsageSettlementIngress(root *os.Root, name string, record *usageSettlementIngressRecord) (*usageSettlementIngressRecord, error) {
	existing, err := readUsageSettlementIngress(root, name)
	if err == nil {
		if !sameUsageSettlementIngressFinancial(existing, record) {
			return nil, ErrUsageBillingRequestConflict
		}
		return existing, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	temporary := ".pending-" + uuid.NewString() + ".tmp"
	f, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close(); _ = root.Remove(temporary) }()
	if _, err = f.Write(raw); err != nil {
		return nil, err
	}
	if err = f.Sync(); err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	// Link publishes atomically without ever replacing an immutable record.
	if err = root.Link(temporary, name); err != nil {
		return nil, err
	}
	// The file contents were fsynced before linking. Persist the final namespace
	// (published link plus removed temp link) with one directory fsync.
	if err = root.Remove(temporary); err != nil {
		return nil, err
	}
	if err = syncUsageSettlementDirectory(root); err != nil {
		return nil, err
	}
	return record, nil
}

func prepareUsageSettlementDurably(ctx context.Context, cfg *config.Config, repo UsageSettlementRepository, cmd *UsageBillingCommand, log *UsageLog) error {
	if repo == nil {
		return errors.New("settlement repository is not configured")
	}
	record, err := freezeUsageSettlementIngress(cmd, log)
	if err != nil {
		return err
	}
	root, err := openUsageSettlementIngress(cfg)
	if err != nil {
		return fmt.Errorf("settlement ingress unavailable: %w", err)
	}
	if root == nil {
		return repo.PrepareSettlement(ctx, &record.Command, record.Detail.UsageLog())
	}
	defer root.Close()
	name := settlementIngressFilename(&record.Command)
	// Final upstream usage must reach disk even after the request/SQL deadline.
	// A separate bounded filesystem budget cannot be consumed by SQL waits.
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	unlock, err := acquireUsageSettlementIngressLock(persistCtx, root, name)
	if err != nil {
		persistCancel()
		return err
	}
	persisted, err := persistUsageSettlementIngress(root, name, record)
	unlock()
	persistCancel()
	if err != nil {
		return fmt.Errorf("persist settlement ingress: %w", err)
	}
	// Never retain the stripe lock across SQL. During an outage this would let
	// a blocked command stop unrelated same-stripe commands before persistence.
	if err = repo.PrepareSettlement(ctx, &persisted.Command, persisted.Detail.UsageLog()); err != nil {
		return fmt.Errorf("%w: %w", ErrUsageSettlementIngressDeferred, err)
	}
	ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer ackCancel()
	if err = acknowledgeUsageSettlementIngress(ackCtx, root, name, persisted); err != nil {
		return fmt.Errorf("acknowledge settlement ingress: %w", err)
	}
	return nil
}

// SQL can run without a file lock because an immutable, identity-checked
// acknowledgment is serialized again before removal. An already-acknowledged
// file is safe; a recreated conflicting file is retained for investigation.
func acknowledgeUsageSettlementIngress(ctx context.Context, root *os.Root, name string, accepted *usageSettlementIngressRecord) error {
	unlock, err := acquireUsageSettlementIngressLock(ctx, root, name)
	if err != nil {
		return err
	}
	defer unlock()
	current, err := readUsageSettlementIngress(root, name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !sameUsageSettlementIngressFinancial(current, accepted) {
		return ErrUsageBillingRequestConflict
	}
	if err = root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncUsageSettlementDirectory(root)
}

type usageSettlementIngressCursor struct {
	mu   sync.Mutex
	file *os.File
}

var usageSettlementIngressCursors sync.Map

// readUsageSettlementIngressNames advances one bounded directory cursor per
// configured volume. A poisoned file is retained and counted, but cannot starve
// healthy records later in the directory. EOF closes the descriptor.
func readUsageSettlementIngressNames(root *os.Root, limit int) ([]string, error) {
	absolute, err := filepath.Abs(root.Name())
	if err != nil {
		return nil, err
	}
	value, _ := usageSettlementIngressCursors.LoadOrStore(absolute, &usageSettlementIngressCursor{})
	cursor := value.(*usageSettlementIngressCursor)
	cursor.mu.Lock()
	defer cursor.mu.Unlock()
	if cursor.file != nil {
		openInfo, e := cursor.file.Stat()
		currentInfo, e2 := root.Stat(".")
		if e != nil || e2 != nil || !os.SameFile(openInfo, currentInfo) {
			_ = cursor.file.Close()
			cursor.file = nil
		}
	}
	if cursor.file == nil {
		cursor.file, err = root.Open(".")
		if err != nil {
			return nil, err
		}
	}
	entries, err := cursor.file.ReadDir(limit)
	if errors.Is(err, io.EOF) {
		_ = cursor.file.Close()
		cursor.file = nil
		err = nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if len(name) == 69 && strings.HasSuffix(name, ".json") {
			if _, e := hex.DecodeString(name[:64]); e == nil {
				names = append(names, name)
			}
		}
	}
	return names, err
}

func replayUsageSettlementIngress(ctx context.Context, cfg *config.Config, repo UsageSettlementRepository, limit int) (UsageSettlementIngressReplayStats, error) {
	var stats UsageSettlementIngressReplayStats
	if repo == nil {
		return stats, errors.New("settlement repository is not configured")
	}
	root, err := openUsageSettlementIngress(cfg)
	if err != nil {
		return stats, err
	}
	if root == nil {
		return stats, nil
	}
	defer root.Close()
	if limit <= 0 {
		limit = 32
	}
	if limit > 128 {
		limit = 128
	}
	names, err := readUsageSettlementIngressNames(root, limit)
	if err != nil {
		return stats, err
	}
	var failures []error
	for _, name := range names {
		if ctx.Err() != nil {
			return stats, ctx.Err()
		}
		stats.Scanned++
		unlock, e := acquireUsageSettlementIngressLock(ctx, root, name)
		if e != nil {
			stats.Deferred++
			failures = append(failures, e)
			continue
		}
		record, e := readUsageSettlementIngress(root, name)
		if errors.Is(e, os.ErrNotExist) {
			unlock()
			continue
		}
		if e != nil {
			stats.Invalid++
			failures = append(failures, e)
			unlock()
			continue
		}
		unlock()
		e = repo.PrepareSettlement(ctx, &record.Command, record.Detail.UsageLog())
		if e != nil {
			stats.Deferred++
			failures = append(failures, e)
			continue
		}
		ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		e = acknowledgeUsageSettlementIngress(ackCtx, root, name, record)
		cancel()
		if e != nil {
			stats.Deferred++
			failures = append(failures, e)
		} else {
			stats.Prepared++
		}
	}
	return stats, errors.Join(failures...)
}
