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
	"sort"
	"strings"
	"time"

	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/trainingdata"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "training-data:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("a subcommand is required")
	}
	switch args[0] {
	case "subject-ref":
		return runSubjectRef(args[1:], stdout, stderr)
	case "rights-upsert":
		return runRightsUpsert(ctx, args[1:], stdout, stderr)
	case "rights-withdraw":
		return runRightsWithdraw(ctx, args[1:], stdout, stderr)
	case "deletion-request":
		return runDeletionRequest(ctx, args[1:], stdout, stderr)
	case "curate":
		return runCurate(ctx, args[1:], stdout, stderr)
	case "bundle":
		return runBundle(ctx, args[1:], stdout, stderr)
	case "verify":
		return runVerify(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func runSubjectRef(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("subject-ref", stderr)
	scope := flags.String("scope", "", "rights scope: user or api_key")
	numericID := flags.Int64("id", 0, "numeric user/API-key ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("subject-ref does not accept positional arguments")
	}
	cfg, err := config.LoadForBootstrap()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ref, err := subjectRef(cfg, *scope, "", *numericID)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, ref)
	return err
}

func runRightsUpsert(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("rights-upsert", stderr)
	scope := flags.String("scope", "", "rights scope: user or api_key")
	scopeRef := flags.String("scope-ref", "", "precomputed HMAC scope reference")
	numericID := flags.Int64("id", 0, "numeric user/API-key ID (alternative to --scope-ref)")
	statusRaw := flags.String("status", string(trainingdata.RightsEligible), "rights status")
	contract := flags.String("contract", "", "consent or contract identifier")
	purposes := flags.String("purposes", "model_training", "comma-separated allowed purposes")
	datasetTypes := flags.String("dataset-types", "chat,code", "comma-separated chat/code/eval types")
	recipients := flags.String("recipients", "", "comma-separated permitted recipient identifiers")
	region := flags.String("region", "", "governing region")
	effectiveAtRaw := flags.String("effective-at", "", "RFC3339 effective time (default now)")
	expiresAtRaw := flags.String("expires-at", "", "optional RFC3339 expiry")
	evidenceURI := flags.String("evidence-uri", "", "URI of consent/contract evidence")
	evidenceSHA256 := flags.String("evidence-sha256", "", "SHA-256 of evidence")
	actor := flags.String("actor", "", "operator/audit actor reference")
	reason := flags.String("reason", "", "audit reason")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("rights-upsert does not accept positional arguments")
	}
	if strings.TrimSpace(*actor) == "" || strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("--actor and --reason are required for rights changes")
	}
	cfg, db, closeDB, err := openDatabase()
	if err != nil {
		return err
	}
	defer closeDB()
	resolvedRef, err := subjectRef(cfg, *scope, *scopeRef, *numericID)
	if err != nil {
		return err
	}
	status := trainingdata.RightsStatus(strings.ToLower(strings.TrimSpace(*statusRaw)))
	if !trainingdata.ValidRightsStatus(status) {
		return fmt.Errorf("unsupported rights status %q", status)
	}
	effectiveAt, err := optionalTime(*effectiveAtRaw, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("effective-at: %w", err)
	}
	expiresAt, err := optionalTimePointer(*expiresAtRaw)
	if err != nil {
		return fmt.Errorf("expires-at: %w", err)
	}
	allowedPurposes := splitList(*purposes)
	allowedDatasetTypes := splitList(*datasetTypes)
	allowedRecipients := splitList(*recipients)
	for _, datasetType := range allowedDatasetTypes {
		if !trainingdata.ValidDatasetType(datasetType) {
			return fmt.Errorf("unsupported dataset type %q", datasetType)
		}
	}
	if status == trainingdata.RightsEligible {
		if strings.TrimSpace(*contract) == "" {
			return fmt.Errorf("--contract is required for eligible rights")
		}
		if len(allowedPurposes) == 0 || len(allowedDatasetTypes) == 0 || len(allowedRecipients) == 0 {
			return fmt.Errorf("eligible rights require purposes, dataset types and recipients")
		}
	}
	rightsID, err := trainingdata.UpsertRights(ctx, db, trainingdata.RightsUpsertInput{
		ScopeType: strings.ToLower(strings.TrimSpace(*scope)), ScopeRef: resolvedRef, Status: status,
		ConsentOrContractID: strings.TrimSpace(*contract), AllowedPurposes: allowedPurposes,
		AllowedDatasetTypes: allowedDatasetTypes, AllowedRecipients: allowedRecipients,
		Region: strings.TrimSpace(*region), EffectiveAt: effectiveAt, ExpiresAt: expiresAt,
		EvidenceURI: strings.TrimSpace(*evidenceURI), EvidenceSHA256: strings.ToLower(strings.TrimSpace(*evidenceSHA256)),
	}, strings.TrimSpace(*actor), strings.TrimSpace(*reason))
	if err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{"rights_id": rightsID, "scope_ref": resolvedRef, "status": status})
}

func runRightsWithdraw(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("rights-withdraw", stderr)
	scope := flags.String("scope", "", "rights scope: user or api_key")
	scopeRef := flags.String("scope-ref", "", "precomputed HMAC scope reference")
	numericID := flags.Int64("id", 0, "numeric user/API-key ID (alternative to --scope-ref)")
	actor := flags.String("actor", "", "operator/audit actor reference")
	reason := flags.String("reason", "", "withdrawal reason")
	requestDeletion := flags.Bool("request-deletion", true, "create deletion-propagation tasks")
	source := flags.String("source", "training-data-cli", "deletion request source")
	idempotencyKey := flags.String("idempotency-key", "", "optional stable deletion idempotency key")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("rights-withdraw does not accept positional arguments")
	}
	if strings.TrimSpace(*actor) == "" || strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("--actor and --reason are required for rights withdrawal")
	}
	cfg, db, closeDB, err := openDatabase()
	if err != nil {
		return err
	}
	defer closeDB()
	resolvedRef, err := subjectRef(cfg, *scope, *scopeRef, *numericID)
	if err != nil {
		return err
	}
	rightsID, version, err := trainingdata.WithdrawRights(ctx, db, *scope, resolvedRef, strings.TrimSpace(*actor), strings.TrimSpace(*reason))
	if err != nil {
		return err
	}
	result := map[string]any{"rights_id": rightsID, "version": version, "status": trainingdata.RightsWithdrawn}
	if *requestDeletion {
		deletionID, err := trainingdata.CreateDeletionRequest(ctx, db, *scope, resolvedRef, strings.TrimSpace(*source), strings.TrimSpace(*reason), strings.TrimSpace(*idempotencyKey))
		if err != nil {
			return fmt.Errorf("rights withdrawn but deletion request failed: %w", err)
		}
		result["deletion_id"] = deletionID
	}
	return writeJSON(stdout, result)
}

func runDeletionRequest(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("deletion-request", stderr)
	scope := flags.String("scope", "", "rights scope: user or api_key")
	scopeRef := flags.String("scope-ref", "", "precomputed HMAC scope reference")
	numericID := flags.Int64("id", 0, "numeric user/API-key ID (alternative to --scope-ref)")
	source := flags.String("source", "training-data-cli", "request source")
	reason := flags.String("reason", "", "request reason")
	idempotencyKey := flags.String("idempotency-key", "", "optional stable idempotency key")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("deletion-request does not accept positional arguments")
	}
	if strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("--reason is required for deletion requests")
	}
	cfg, db, closeDB, err := openDatabase()
	if err != nil {
		return err
	}
	defer closeDB()
	resolvedRef, err := subjectRef(cfg, *scope, *scopeRef, *numericID)
	if err != nil {
		return err
	}
	deletionID, err := trainingdata.CreateDeletionRequest(ctx, db, *scope, resolvedRef, strings.TrimSpace(*source), strings.TrimSpace(*reason), strings.TrimSpace(*idempotencyKey))
	if err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{"deletion_id": deletionID, "scope_ref": resolvedRef})
}

func runCurate(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("curate", stderr)
	input := flags.String("input", "", "local raw-capture export directory")
	output := flags.String("output", "", "new immutable curated bundle directory")
	datasetType := flags.String("dataset-type", "chat", "chat, code or eval")
	recipient := flags.String("recipient", "", "optional recipient filter")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("curate does not accept positional arguments")
	}
	_, db, closeDB, err := openDatabase()
	if err != nil {
		return err
	}
	defer closeDB()
	at := time.Now().UTC()
	rights, err := trainingdata.LoadCurationRights(ctx, db, at)
	if err != nil {
		return err
	}
	manifest, err := trainingdata.CurateLocalCaptures(*input, *output, trainingdata.CurationOptions{
		DatasetType: *datasetType, Recipient: *recipient, GeneratedAt: at, RightsByID: rights,
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, manifest)
}

func runBundle(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("bundle", stderr)
	input := flags.String("input", "", "verified curated bundle directory")
	output := flags.String("output", "", "new immutable delivery bundle directory")
	releaseID := flags.String("release-id", "", "unique release identifier")
	recipient := flags.String("recipient", "", "recipient identifier from rights ledger")
	contract := flags.String("contract", "", "delivery contract identifier")
	purpose := flags.String("purpose", "model_training", "allowed delivery purpose")
	includeProvenance := flags.Bool("include-provenance", true, "include per-sample provenance")
	reviewedBy := flags.String("reviewed-by", "", "human reviewer reference")
	reviewReference := flags.String("review-reference", "", "review ticket/report identifier")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("bundle does not accept positional arguments")
	}
	_, db, closeDB, err := openDatabase()
	if err != nil {
		return err
	}
	defer closeDB()
	at := time.Now().UTC()
	rights, err := trainingdata.LoadCurationRights(ctx, db, at)
	if err != nil {
		return err
	}
	manifest, err := trainingdata.CreateDeliveryBundle(*input, *output, trainingdata.DeliveryOptions{
		ReleaseID: *releaseID, Recipient: *recipient, Contract: *contract,
		AllowedPurpose: *purpose, IncludeProvenance: *includeProvenance,
		ReviewedBy: *reviewedBy, ReviewReference: *reviewReference,
		CreatedAt: at, RightsByID: rights,
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, manifest)
}

func runVerify(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("verify", stderr)
	input := flags.String("input", "", "curated or delivery bundle directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("verify does not accept positional arguments")
	}
	if err := trainingdata.VerifyDatasetBundle(*input); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, "ok")
	return err
}

func openDatabase() (*config.Config, *sql.DB, func(), error) {
	cfg, err := config.LoadForBootstrap()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load config: %w", err)
	}
	client, db, err := repository.InitEnt(cfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("initialize database: %w", err)
	}
	return cfg, db, func() { _ = client.Close() }, nil
}

func subjectRef(cfg *config.Config, scope, explicit string, numericID int64) (string, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope != "user" && scope != "api_key" {
		return "", fmt.Errorf("--scope must be user or api_key")
	}
	explicit = strings.TrimSpace(explicit)
	if explicit != "" && numericID > 0 {
		return "", fmt.Errorf("use only one of --scope-ref or --id")
	}
	if explicit != "" {
		if !trainingdata.ValidSubjectRef(explicit) {
			return "", fmt.Errorf("--scope-ref must be a 64-character HMAC SHA-256 hex value")
		}
		return strings.ToLower(explicit), nil
	}
	if numericID <= 0 {
		return "", fmt.Errorf("one of --scope-ref or a positive --id is required")
	}
	if cfg == nil || len([]byte(strings.TrimSpace(cfg.TrainingData.SubjectHMACKey))) < 32 {
		return "", fmt.Errorf("training_data.subject_hmac_key must be configured with at least 32 bytes")
	}
	ref := trainingdata.NewManager(nil, cfg).SubjectRef(scope, numericID)
	if ref == "" {
		return "", fmt.Errorf("failed to derive subject reference")
	}
	return ref, nil
}

func optionalTime(raw string, fallback time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback.UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func optionalTimePointer(raw string) (*time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parsed, err := optionalTime(raw, time.Time{})
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func splitList(raw string) []string {
	seen := make(map[string]struct{})
	for _, value := range strings.Split(raw, ",") {
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

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: training-data <command> [flags]

Commands:
  subject-ref       Derive a stable HMAC user/API-key reference
  rights-upsert     Create or update a rights-ledger grant
  rights-withdraw   Withdraw rights and create deletion tasks
  deletion-request  Create idempotent deletion-propagation tasks
  curate            Build an immutable curated JSONL/provenance bundle
  bundle            Build a recipient-specific immutable delivery bundle
  verify            Verify bundle SHA-256 checksums`)
}
