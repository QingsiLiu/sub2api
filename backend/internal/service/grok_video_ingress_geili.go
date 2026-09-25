package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// Accepted provider IDs must survive even when SQL becomes unavailable between
// upstream create and its response. This mounted journal holds safe task data,
// not prompts or credentials, until the immutable SQL task is acknowledged.
func openGrokVideoIngress(cfg *config.Config) (*os.Root, error) {
	root, err := openUsageSettlementIngress(cfg)
	if root == nil || err != nil {
		return nil, err
	}
	defer root.Close()
	if err = root.Mkdir("video-tasks", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := root.Lstat("video-tasks")
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("video ingress must be a real directory")
	}
	sub, err := root.OpenRoot("video-tasks")
	if err != nil {
		return nil, err
	}
	after, e := sub.Stat(".")
	if e != nil || !os.SameFile(info, after) {
		sub.Close()
		return nil, errors.New("video ingress directory changed")
	}
	directory, e := sub.Open(".")
	if e != nil {
		sub.Close()
		return nil, e
	}
	e = directory.Chmod(0700)
	if e == nil {
		e = directory.Sync()
	}
	directory.Close()
	if e != nil {
		sub.Close()
		return nil, e
	}
	if err = syncUsageSettlementDirectory(root); err != nil {
		sub.Close()
		return nil, err
	}
	return sub, nil
}
func videoIngressName(s *GrokVideoTaskSnapshot) string {
	return settlementIngressFilename(&UsageBillingCommand{RequestID: s.FinancialRequestID, APIKeyID: s.APIKeyID})
}
func readGrokVideoIngress(root *os.Root, name string) (*GrokVideoTaskSnapshot, error) {
	file, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("invalid video ingress record")
	}
	var snap GrokVideoTaskSnapshot
	d := json.NewDecoder(io.LimitReader(file, 1<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&snap); err != nil {
		return nil, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, errors.New("trailing video ingress data")
	}
	if videoIngressName(&snap) != name || snap.Version != 1 || snap.UserID <= 0 || snap.APIKeyID <= 0 || snap.AccountID <= 0 {
		return nil, errors.New("invalid video ingress identity")
	}
	return &snap, nil
}
func persistGrokVideoIngress(ctx context.Context, cfg *config.Config, repo GrokVideoTaskRepository, snap *GrokVideoTaskSnapshot) error {
	root, err := openGrokVideoIngress(cfg)
	if err != nil {
		return err
	}
	if root == nil {
		return repo.StoreGrokVideoTask(ctx, snap)
	}
	defer root.Close()
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return errors.New("video ingress too large")
	}
	name := videoIngressName(snap)
	existing, err := readGrokVideoIngress(root, name)
	if err == nil {
		other, _ := json.Marshal(existing)
		if !bytes.Equal(other, raw) {
			return ErrUsageBillingRequestConflict
		}
	} else if errors.Is(err, os.ErrNotExist) {
		temp := ".pending-" + uuid.NewString() + ".tmp"
		f, e := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
		if e != nil {
			return e
		}
		defer root.Remove(temp)
		defer f.Close()
		if _, e = f.Write(raw); e != nil {
			return e
		}
		if e = f.Sync(); e != nil {
			return e
		}
		if e = f.Close(); e != nil {
			return e
		}
		if e = root.Link(temp, name); e != nil && !errors.Is(e, os.ErrExist) {
			return e
		}
		if e = syncUsageSettlementDirectory(root); e != nil {
			return e
		}
		existing, e = readGrokVideoIngress(root, name)
		if e != nil {
			return e
		}
		other, _ := json.Marshal(existing)
		if !bytes.Equal(other, raw) {
			return ErrUsageBillingRequestConflict
		}
	} else {
		return err
	}
	// A persisted snapshot is enough to acknowledge accepted creation. SQL
	// failures retain it; the runtime replay never creates another upstream task.
	if err = repo.StoreGrokVideoTask(ctx, snap); err != nil {
		if errors.Is(err, ErrUsageBillingRequestConflict) {
			return err
		}
		return nil
	}
	if err = root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncUsageSettlementDirectory(root)
}
func replayGrokVideoIngress(ctx context.Context, cfg *config.Config, repo GrokVideoTaskRepository) error {
	root, err := openGrokVideoIngress(cfg)
	if root == nil || err != nil {
		return err
	}
	defer root.Close()
	names, err := readUsageSettlementIngressNames(root, 32)
	if err != nil {
		return err
	}
	var first error
	for _, name := range names {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		snap, e := readGrokVideoIngress(root, name)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e == nil {
			e = repo.StoreGrokVideoTask(ctx, snap)
		}
		if e == nil {
			e = root.Remove(name)
			if errors.Is(e, os.ErrNotExist) {
				e = nil
			}
		}
		if e != nil && first == nil {
			first = e
		}
	}
	if err = syncUsageSettlementDirectory(root); first == nil {
		first = err
	}
	return first
}

// Bounded health scan reports truncation explicitly: its count/age are then
// lower bounds, never a false empty queue. Runtime exposes a scan error too.
func grokVideoIngressHealth(ctx context.Context, cfg *config.Config) (int64, float64, bool, error) {
	root, err := openGrokVideoIngress(cfg)
	if root == nil || err != nil {
		return 0, 0, false, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return 0, 0, false, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(10001)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, 0, false, err
	}
	truncated := len(entries) > 10000
	if truncated {
		entries = entries[:10000]
	}
	var count int64
	oldest := 0.0
	for _, entry := range entries {
		if ctx.Err() != nil {
			return count, oldest, truncated, ctx.Err()
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			if errors.Is(e, os.ErrNotExist) {
				continue
			}
			return count, oldest, truncated, e
		}
		count++
		age := time.Since(info.ModTime()).Seconds()
		if age > oldest {
			oldest = age
		}
	}
	return count, oldest, truncated, nil
}
