// billing-reconcile-geili is read-only by default; apply is manifest-bound and
// can only add financial evidence. It cannot debit, refund or grant entitlement.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	_ "github.com/lib/pq"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "billing reconciliation failed:", err)
		os.Exit(1)
	}
}
func run(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("billing-reconcile-geili", flag.ContinueOnError)
	fs.SetOutput(errOut)
	mode := fs.String("mode", "scan", "scan (read-only) or apply")
	fromText := fs.String("from", "", "required inclusive settlement timestamp (RFC3339)")
	cutoffText := fs.String("cutoff", "", "required exclusive settlement timestamp (RFC3339); must exactly match manifest for apply")
	path := fs.String("manifest", "", "manifest file; scan creates a new 0600 file, apply reads it")
	sha := fs.String("sha256", "", "required for apply: independently approved canonical manifest SHA256")
	env := fs.String("dsn-env", "BILLING_RECONCILE_DSN", "name of env variable containing PostgreSQL DSN; never printed")
	batch := fs.Int("batch-size", 100, "apply transaction size, 1..500")
	timeout := fs.Duration("timeout", 10*time.Minute, "overall command deadline")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *mode != "scan" && *mode != "apply" {
		return errors.New("mode must be scan or apply")
	}
	if *path == "" {
		return errors.New("explicit manifest file is required")
	}
	cutoff, err := time.Parse(time.RFC3339Nano, *cutoffText)
	if err != nil {
		return errors.New("explicit cutoff with timezone is required")
	}
	var from time.Time
	var manifest repository.UsageRecoveryManifest
	if *mode == "scan" {
		from, err = time.Parse(time.RFC3339Nano, *fromText)
		if err != nil {
			return errors.New("scan requires explicit from with timezone")
		}
		if *sha != "" {
			return errors.New("sha256 is only accepted for apply")
		}
	} else {
		if *sha == "" {
			return errors.New("apply requires the independently approved manifest SHA256")
		}
		f, e := os.Open(*path)
		if e != nil {
			return e
		}
		defer f.Close()
		d := json.NewDecoder(io.LimitReader(f, 128<<20))
		d.DisallowUnknownFields()
		if e = d.Decode(&manifest); e != nil {
			return e
		}
		var trailing any
		if e = d.Decode(&trailing); e != io.EOF {
			return errors.New("manifest contains trailing or oversized data")
		}
		actual, e := repository.UsageRecoveryManifestDigest(&manifest)
		if e != nil {
			return e
		}
		if actual != *sha {
			return errors.New("manifest SHA256 mismatch")
		}
		if !manifest.Cutoff.Equal(cutoff) {
			return errors.New("cutoff does not match manifest")
		}
	}
	dsn := os.Getenv(*env)
	if dsn == "" {
		return fmt.Errorf("DSN environment variable %s is empty", *env)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return errors.New("invalid PostgreSQL connection configuration")
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, deadline := context.WithTimeout(ctx, *timeout)
	defer deadline()
	if err = db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to reconciliation database: %w", err)
	}
	repo := repository.NewUsageRecoveryRepository(db)
	if *mode == "scan" {
		m, e := repo.Scan(ctx, from, cutoff)
		if e != nil {
			return e
		}
		digest, e := repository.UsageRecoveryManifestDigest(m)
		if e != nil {
			return e
		}
		raw, e := json.MarshalIndent(m, "", "  ")
		if e != nil {
			return e
		}
		raw = append(raw, '\n')
		if e = writeManifest(*path, raw); e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(map[string]any{"mode": "read_only_scan", "manifest": *path, "sha256": digest, "cutoff": m.Cutoff, "summary": m.Summary, "financial_mutations": 0})
	}
	result, e := repo.Apply(ctx, &manifest, *sha, cutoff, *batch)
	if e != nil {
		_ = json.NewEncoder(out).Encode(map[string]any{"mode": "apply", "committed_progress": result, "financial_mutations": 0})
		return e
	}
	return json.NewEncoder(out).Encode(map[string]any{"mode": "apply", "result": result, "financial_mutations": 0})
}
func writeManifest(path string, raw []byte) error {
	// Hard-link publication is atomic and does not overwrite an existing approved
	// manifest, even if another process races the same path.
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".billing-manifest-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(raw); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Link(tmp, path)
}
