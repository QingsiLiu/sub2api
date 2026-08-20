package trainingdata

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type CurationOptions struct {
	DatasetType string
	Recipient   string
	GeneratedAt time.Time
	RightsByID  map[string]RightsGrant
}

type DeliveryOptions struct {
	ReleaseID         string
	Recipient         string
	Contract          string
	AllowedPurpose    string
	IncludeProvenance bool
	ReviewedBy        string
	ReviewReference   string
	CreatedAt         time.Time
	RightsByID        map[string]RightsGrant
}

// LoadCurationRights loads the current effective ledger snapshot used by both
// curation and delivery. Re-checking at both boundaries ensures a withdrawal
// after capture prevents future dataset creation or buyer delivery.
func LoadCurationRights(ctx context.Context, db *sql.DB, now time.Time) (map[string]RightsGrant, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return LoadRightsByID(ctx, db, now.UTC())
}

// CurateLocalCaptures builds an immutable JSONL + provenance bundle from a
// local export of raw capture directories. It never includes raw payload files
// in the output and refuses symlink/path traversal inputs.
func CurateLocalCaptures(inputDir, outputDir string, options CurationOptions) (CuratedDatasetManifest, error) {
	inputDir = strings.TrimSpace(inputDir)
	outputDir = strings.TrimSpace(outputDir)
	if inputDir == "" || outputDir == "" {
		return CuratedDatasetManifest{}, fmt.Errorf("input and output directories are required")
	}
	if err := ensureDistinctDirectories(inputDir, outputDir); err != nil {
		return CuratedDatasetManifest{}, err
	}
	info, err := os.Stat(inputDir)
	if err != nil {
		return CuratedDatasetManifest{}, fmt.Errorf("stat capture input directory: %w", err)
	}
	if !info.IsDir() {
		return CuratedDatasetManifest{}, fmt.Errorf("capture input %q is not a directory", inputDir)
	}
	if !ValidDatasetType(options.DatasetType) {
		return CuratedDatasetManifest{}, fmt.Errorf("dataset type must be chat, code or eval")
	}
	options.DatasetType = normalizeDatasetType(options.DatasetType)
	options.Recipient = strings.TrimSpace(options.Recipient)
	if options.GeneratedAt.IsZero() {
		options.GeneratedAt = time.Now().UTC()
	} else {
		options.GeneratedAt = options.GeneratedAt.UTC()
	}

	manifestPaths, err := findCaptureManifests(inputDir)
	if err != nil {
		return CuratedDatasetManifest{}, err
	}
	stats := CurationStats{ManifestCount: len(manifestPaths), ExcludedByReason: make(map[string]int)}
	samples := make([]TrainingSample, 0, len(manifestPaths))
	provenance := make([]ProvenanceRecord, 0, len(manifestPaths))
	seenContent := make(map[string]struct{}, len(manifestPaths))

	exclude := func(reason string) {
		stats.ExcludedCount++
		stats.ExcludedByReason[reason]++
	}
	for _, manifestPath := range manifestPaths {
		var capture Manifest
		if err := readJSONFile(manifestPath, &capture); err != nil {
			exclude("manifest_invalid")
			continue
		}
		grant, reason := eligibleGrantForArtifact(capture, options.RightsByID, options.DatasetType, options.Recipient, options.GeneratedAt)
		if reason != "" {
			exclude(reason)
			continue
		}
		captureDir := filepath.Dir(manifestPath)
		requestBody, err := readCaptureBody(captureDir, capture.ClientRequestFile)
		if err != nil {
			exclude("client_request_unreadable")
			continue
		}
		responseBody, err := readCaptureBody(captureDir, capture.ClientResponseFile)
		if err != nil {
			exclude("client_response_unreadable")
			continue
		}
		sample, err := BuildTrainingSample(capture, requestBody, responseBody, options.DatasetType)
		if err != nil {
			exclude("sample_unusable")
			continue
		}
		if options.DatasetType == "code" && containsString(sample.QualityFlags, "code_signal_weak") {
			exclude("code_signal_weak")
			continue
		}
		if _, duplicate := seenContent[sample.ContentSHA256]; duplicate {
			exclude("duplicate_content")
			continue
		}
		seenContent[sample.ContentSHA256] = struct{}{}
		manifestRelative, err := filepath.Rel(inputDir, manifestPath)
		if err != nil {
			exclude("manifest_path_invalid")
			continue
		}
		finishedAt := ""
		if capture.FinishedAt != nil {
			finishedAt = capture.FinishedAt.UTC().Format(time.RFC3339Nano)
		}
		provenance = append(provenance, ProvenanceRecord{
			SampleID: sample.SampleID, SourceCaptureID: capture.CaptureID,
			SourceManifest: filepath.ToSlash(manifestRelative), RightsRef: grant.RightsID,
			UserSubjectRef: capture.UserSubjectRef, APIKeySubjectRef: capture.APIKeySubjectRef,
			RightsStatus: string(grant.Status), RightsVersion: grant.Version,
			AllowedPurposes:     append([]string(nil), grant.AllowedPurposes...),
			AllowedDatasetTypes: append([]string(nil), grant.AllowedDatasetTypes...),
			AllowedRecipients:   append([]string(nil), grant.AllowedRecipients...),
			DatasetType:         sample.DatasetType, Split: sample.Split, ClientModel: sample.ClientModel,
			AssistantSourceModel: sample.AssistantSourceModel,
			StartedAt:            capture.StartedAt.UTC().Format(time.RFC3339Nano), FinishedAt: finishedAt,
			RequestSHA256: hashBytes(requestBody), ResponseSHA256: hashBytes(responseBody),
			ContentSHA256: sample.ContentSHA256, TransformVersion: sample.TransformVersion,
		})
		samples = append(samples, sample)
		stats.IncludedCount++
	}

	manifest := CuratedDatasetManifest{
		SchemaVersion: CuratedDatasetSchemaVersion, DatasetType: options.DatasetType,
		SourceKind: "local_capture_export", GeneratedAt: options.GeneratedAt.Format(time.RFC3339Nano),
		TransformVersion: TransformVersion, RedactionVersion: RedactionVersion,
		RightsPolicy:         "current_eligible+model_training+dataset_type+allowed_recipient",
		CaptureManifestCount: stats.ManifestCount, SampleCount: stats.IncludedCount,
		ExcludedCount: stats.ExcludedCount, ExcludedByReason: stats.ExcludedByReason,
		HumanReviewRequired: true,
		Files:               make(map[string]DatasetArtifact),
	}
	err = buildAtomicDirectory(outputDir, func(temporaryDir string) error {
		sampleName := options.DatasetType + ".jsonl"
		provenanceName := "provenance.ndjson"
		cardName := "dataset-card.md"
		if err := writeJSONLines(filepath.Join(temporaryDir, sampleName), samples); err != nil {
			return err
		}
		if err := writeJSONLines(filepath.Join(temporaryDir, provenanceName), provenance); err != nil {
			return err
		}
		if err := writePrivateText(filepath.Join(temporaryDir, cardName), curatedDatasetCard(manifest)); err != nil {
			return err
		}
		for _, name := range []string{sampleName, provenanceName, cardName} {
			artifact, err := hashFile(filepath.Join(temporaryDir, name))
			if err != nil {
				return err
			}
			manifest.Files[name] = artifact
		}
		if err := writeJSONFile(filepath.Join(temporaryDir, "manifest.json"), manifest); err != nil {
			return err
		}
		return writeSHA256Sums(temporaryDir, []string{sampleName, provenanceName, cardName, "manifest.json"})
	})
	if err != nil {
		return CuratedDatasetManifest{}, err
	}
	return manifest, nil
}

// CreateDeliveryBundle revalidates every sample against the current rights
// ledger and filters the curated bundle to a single recipient and purpose.
func CreateDeliveryBundle(curatedDir, outputDir string, options DeliveryOptions) (DeliveryDatasetManifest, error) {
	curatedDir = strings.TrimSpace(curatedDir)
	outputDir = strings.TrimSpace(outputDir)
	if err := ensureDistinctDirectories(curatedDir, outputDir); err != nil {
		return DeliveryDatasetManifest{}, err
	}
	if err := VerifyDatasetBundle(curatedDir); err != nil {
		return DeliveryDatasetManifest{}, fmt.Errorf("verify curated bundle: %w", err)
	}
	options.ReleaseID = strings.TrimSpace(options.ReleaseID)
	options.Recipient = strings.TrimSpace(options.Recipient)
	options.Contract = strings.TrimSpace(options.Contract)
	options.AllowedPurpose = strings.TrimSpace(options.AllowedPurpose)
	options.ReviewedBy = strings.TrimSpace(options.ReviewedBy)
	options.ReviewReference = strings.TrimSpace(options.ReviewReference)
	if options.AllowedPurpose == "" {
		options.AllowedPurpose = "model_training"
	}
	if options.CreatedAt.IsZero() {
		options.CreatedAt = time.Now().UTC()
	} else {
		options.CreatedAt = options.CreatedAt.UTC()
	}
	if !validReleaseID(options.ReleaseID) {
		return DeliveryDatasetManifest{}, fmt.Errorf("release_id must use 1-128 letters, digits, dots, underscores or hyphens")
	}
	if options.Recipient == "" || options.Contract == "" {
		return DeliveryDatasetManifest{}, fmt.Errorf("recipient and contract are required")
	}
	if options.ReviewedBy == "" || options.ReviewReference == "" {
		return DeliveryDatasetManifest{}, fmt.Errorf("reviewed_by and review_reference are required before delivery")
	}

	var source CuratedDatasetManifest
	if err := readJSONFile(filepath.Join(curatedDir, "manifest.json"), &source); err != nil {
		return DeliveryDatasetManifest{}, err
	}
	if source.SchemaVersion != CuratedDatasetSchemaVersion || !ValidDatasetType(source.DatasetType) {
		return DeliveryDatasetManifest{}, fmt.Errorf("unsupported curated manifest schema or dataset type")
	}
	sampleName := normalizeDatasetType(source.DatasetType) + ".jsonl"
	samplePath, err := safeExistingBundlePath(curatedDir, sampleName)
	if err != nil {
		return DeliveryDatasetManifest{}, err
	}
	provenancePath, err := safeExistingBundlePath(curatedDir, "provenance.ndjson")
	if err != nil {
		return DeliveryDatasetManifest{}, err
	}
	samples, err := readJSONLines[TrainingSample](samplePath)
	if err != nil {
		return DeliveryDatasetManifest{}, err
	}
	provenanceRows, err := readJSONLines[ProvenanceRecord](provenancePath)
	if err != nil {
		return DeliveryDatasetManifest{}, err
	}
	provenanceBySample := make(map[string]ProvenanceRecord, len(provenanceRows))
	for _, row := range provenanceRows {
		if _, duplicate := provenanceBySample[row.SampleID]; duplicate {
			return DeliveryDatasetManifest{}, fmt.Errorf("duplicate provenance sample_id %q", row.SampleID)
		}
		provenanceBySample[row.SampleID] = row
	}
	deliveredSamples := make([]TrainingSample, 0, len(samples))
	deliveredProvenance := make([]ProvenanceRecord, 0, len(samples))
	for _, sample := range samples {
		row, ok := provenanceBySample[sample.SampleID]
		if !ok || row.ContentSHA256 != sample.ContentSHA256 {
			return DeliveryDatasetManifest{}, fmt.Errorf("sample %q has missing or mismatched provenance", sample.SampleID)
		}
		if strings.TrimSpace(row.UserSubjectRef) == "" || strings.TrimSpace(row.APIKeySubjectRef) == "" {
			return DeliveryDatasetManifest{}, fmt.Errorf("sample %q provenance lacks internal subject references", sample.SampleID)
		}
		grant, ok := options.RightsByID[row.RightsRef]
		if !ok || !grantEligibleAt(grant, options.CreatedAt) || grant.Version < row.RightsVersion {
			continue
		}
		if subjectHasCurrentVeto(options.RightsByID, row.UserSubjectRef, row.APIKeySubjectRef, options.CreatedAt) {
			continue
		}
		if !containsString(grant.AllowedPurposes, options.AllowedPurpose) ||
			!containsString(grant.AllowedDatasetTypes, sample.DatasetType) ||
			!containsString(grant.AllowedRecipients, options.Recipient) {
			continue
		}
		deliveredSamples = append(deliveredSamples, sample)
		row.RightsStatus = string(grant.Status)
		row.RightsVersion = grant.Version
		row.AllowedPurposes = append([]string(nil), grant.AllowedPurposes...)
		row.AllowedDatasetTypes = append([]string(nil), grant.AllowedDatasetTypes...)
		row.AllowedRecipients = append([]string(nil), grant.AllowedRecipients...)
		row.UserSubjectRef = ""
		row.APIKeySubjectRef = ""
		deliveredProvenance = append(deliveredProvenance, row)
	}
	if len(deliveredSamples) == 0 {
		return DeliveryDatasetManifest{}, fmt.Errorf("no samples remain eligible for recipient %q", options.Recipient)
	}
	sourceArtifact, err := hashFile(filepath.Join(curatedDir, "manifest.json"))
	if err != nil {
		return DeliveryDatasetManifest{}, err
	}
	manifest := DeliveryDatasetManifest{
		SchemaVersion: DeliveryDatasetSchemaVersion, ReleaseID: options.ReleaseID,
		DatasetType: source.DatasetType, Recipient: options.Recipient, Contract: options.Contract,
		AllowedPurpose: options.AllowedPurpose, CreatedAt: options.CreatedAt.Format(time.RFC3339Nano),
		SourceManifestSHA256: sourceArtifact.SHA256, SourceSampleCount: len(samples),
		SampleCount: len(deliveredSamples), ProvenanceIncluded: options.IncludeProvenance,
		RawCaptureIncluded: false, Immutable: true, ReviewedBy: options.ReviewedBy,
		ReviewReference: options.ReviewReference, Files: make(map[string]DatasetArtifact),
	}
	err = buildAtomicDirectory(outputDir, func(temporaryDir string) error {
		if err := writeJSONLines(filepath.Join(temporaryDir, sampleName), deliveredSamples); err != nil {
			return err
		}
		artifactNames := []string{sampleName}
		if options.IncludeProvenance {
			if err := writeJSONLines(filepath.Join(temporaryDir, "provenance.ndjson"), deliveredProvenance); err != nil {
				return err
			}
			artifactNames = append(artifactNames, "provenance.ndjson")
		}
		cardName := "delivery-card.md"
		if err := writePrivateText(filepath.Join(temporaryDir, cardName), deliveryDatasetCard(manifest)); err != nil {
			return err
		}
		artifactNames = append(artifactNames, cardName)
		for _, name := range artifactNames {
			artifact, err := hashFile(filepath.Join(temporaryDir, name))
			if err != nil {
				return err
			}
			manifest.Files[name] = artifact
		}
		if err := writeJSONFile(filepath.Join(temporaryDir, "manifest.json"), manifest); err != nil {
			return err
		}
		artifactNames = append(artifactNames, "manifest.json")
		return writeSHA256Sums(temporaryDir, artifactNames)
	})
	if err != nil {
		return DeliveryDatasetManifest{}, err
	}
	return manifest, nil
}

func VerifyDatasetBundle(directory string) error {
	sumsPath, err := safeExistingBundlePath(directory, "SHA256SUMS")
	if err != nil {
		return err
	}
	encoded, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	sums, err := parseSHA256Sums(encoded)
	if err != nil {
		return err
	}
	if _, ok := sums["manifest.json"]; !ok {
		return fmt.Errorf("SHA256SUMS does not include manifest.json")
	}
	for name, expected := range sums {
		filename, err := safeExistingBundlePath(directory, name)
		if err != nil {
			return err
		}
		artifact, err := hashFile(filename)
		if err != nil {
			return err
		}
		if artifact.SHA256 != expected {
			return fmt.Errorf("checksum mismatch for %s", name)
		}
	}
	return nil
}

func findCaptureManifests(root string) ([]string, error) {
	var result []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() && entry.Name() == "manifest.json" {
			result = append(result, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan capture manifests: %w", err)
	}
	sort.Strings(result)
	return result, nil
}

func readCaptureBody(captureDir, relative string) ([]byte, error) {
	if strings.TrimSpace(relative) == "" {
		return nil, fmt.Errorf("capture body path is empty")
	}
	filename, err := safeExistingBundlePath(captureDir, relative)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return DecodeCaptureBody(file, relative)
}

func eligibleGrantForArtifact(manifest Manifest, grants map[string]RightsGrant, datasetType, recipient string, now time.Time) (RightsGrant, string) {
	if manifest.SchemaVersion != "capture-v1" || strings.TrimSpace(manifest.CaptureID) == "" {
		return RightsGrant{}, "capture_schema_invalid"
	}
	if !manifest.CaptureComplete {
		return RightsGrant{}, "capture_incomplete"
	}
	if manifest.ClientResponse.Status < 200 || manifest.ClientResponse.Status >= 300 {
		return RightsGrant{}, "client_http_status"
	}
	if !hasSuccessfulUpstreamAttempt(manifest) {
		return RightsGrant{}, "upstream_success_missing"
	}
	if strings.TrimSpace(firstUpstreamModel(manifest)) == "" {
		return RightsGrant{}, "upstream_model_missing"
	}
	if manifest.RightsStatus != RightsEligible || strings.TrimSpace(manifest.RightsID) == "" {
		return RightsGrant{}, "capture_rights_ineligible"
	}
	grant, ok := grants[manifest.RightsID]
	if !ok {
		return RightsGrant{}, "current_rights_missing"
	}
	if !grantEligibleAt(grant, now) {
		return RightsGrant{}, "current_rights_ineligible"
	}
	if subjectHasCurrentVeto(grants, manifest.UserSubjectRef, manifest.APIKeySubjectRef, now) {
		return RightsGrant{}, "current_subject_veto"
	}
	if grant.Version < manifest.RightsVersion {
		return RightsGrant{}, "rights_version_regressed"
	}
	if grant.ScopeType == "user" && grant.ScopeRef != manifest.UserSubjectRef {
		return RightsGrant{}, "rights_scope_mismatch"
	}
	if grant.ScopeType == "api_key" && grant.ScopeRef != manifest.APIKeySubjectRef {
		return RightsGrant{}, "rights_scope_mismatch"
	}
	if !containsString(grant.AllowedPurposes, "model_training") {
		return RightsGrant{}, "purpose_not_allowed"
	}
	if !containsString(grant.AllowedDatasetTypes, datasetType) {
		return RightsGrant{}, "dataset_type_not_allowed"
	}
	if len(grant.AllowedRecipients) == 0 {
		return RightsGrant{}, "recipient_not_allowed"
	}
	if recipient != "" && !containsString(grant.AllowedRecipients, recipient) {
		return RightsGrant{}, "recipient_not_allowed"
	}
	return grant, ""
}

func hasSuccessfulUpstreamAttempt(manifest Manifest) bool {
	for _, attempt := range manifest.Attempts {
		if attempt.Complete && strings.TrimSpace(attempt.Error) == "" &&
			attempt.HTTPStatus >= 200 && attempt.HTTPStatus < 300 {
			return true
		}
	}
	return false
}

func subjectHasCurrentVeto(grants map[string]RightsGrant, userSubjectRef, apiKeySubjectRef string, now time.Time) bool {
	for _, grant := range grants {
		matchesSubject := (grant.ScopeType == "user" && grant.ScopeRef == userSubjectRef) ||
			(grant.ScopeType == "api_key" && grant.ScopeRef == apiKeySubjectRef)
		if !matchesSubject {
			continue
		}
		if grant.RevokedAt != nil || (grant.ExpiresAt != nil && !grant.ExpiresAt.After(now)) {
			return true
		}
		switch grant.Status {
		case RightsExcluded, RightsWithdrawn, RightsExpired, RightsLegalHold:
			return true
		}
	}
	return false
}

func grantEligibleAt(grant RightsGrant, now time.Time) bool {
	if grant.Status != RightsEligible || grant.RevokedAt != nil {
		return false
	}
	if !grant.EffectiveAt.IsZero() && grant.EffectiveAt.After(now) {
		return false
	}
	return grant.ExpiresAt == nil || grant.ExpiresAt.After(now)
}

func buildAtomicDirectory(outputDir string, build func(string) error) error {
	outputDir, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	if _, err := os.Lstat(outputDir); err == nil {
		return fmt.Errorf("output directory %q already exists", outputDir)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect output directory: %w", err)
	}
	parent := filepath.Dir(outputDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create output parent: %w", err)
	}
	temporaryDir, err := os.MkdirTemp(parent, "."+filepath.Base(outputDir)+".tmp-")
	if err != nil {
		return fmt.Errorf("create temporary output directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporaryDir)
		}
	}()
	if err := os.Chmod(temporaryDir, 0o700); err != nil {
		return err
	}
	if err := build(temporaryDir); err != nil {
		return err
	}
	if err := os.Rename(temporaryDir, outputDir); err != nil {
		return fmt.Errorf("commit immutable output directory: %w", err)
	}
	committed = true
	return nil
}

func curatedDatasetCard(manifest CuratedDatasetManifest) string {
	return fmt.Sprintf(`# Curated training dataset

- Schema: %s
- Dataset type: %s
- Generated at: %s
- Included samples: %d
- Excluded captures: %d
- Rights policy: %s
- Raw captures included: no
- Human review required before delivery: %t

This bundle contains redacted, normalized user/assistant samples and provenance only. System, developer and tool turns are excluded from deliverable samples. Automated redaction is deliberately limited and does not replace legal, privacy, code-license or quality review.
`, manifest.SchemaVersion, manifest.DatasetType, manifest.GeneratedAt, manifest.SampleCount, manifest.ExcludedCount, manifest.RightsPolicy, manifest.HumanReviewRequired)
}

func deliveryDatasetCard(manifest DeliveryDatasetManifest) string {
	return fmt.Sprintf(`# Training dataset delivery

- Release: %s
- Recipient: %s
- Contract: %s
- Allowed purpose: %s
- Dataset type: %s
- Samples: %d
- Created at: %s
- Reviewed by: %s
- Review reference: %s
- Raw captures included: no
- Immutable bundle: yes

Use is limited to the named recipient, contract and purpose. Re-identification, resale and onward transfer are not authorized by this artifact.
`, manifest.ReleaseID, manifest.Recipient, manifest.Contract, manifest.AllowedPurpose, manifest.DatasetType, manifest.SampleCount, manifest.CreatedAt, manifest.ReviewedBy, manifest.ReviewReference)
}

func validReleaseID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}
