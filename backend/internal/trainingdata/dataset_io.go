package trainingdata

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	CuratedDatasetSchemaVersion  = "curated-v1"
	DeliveryDatasetSchemaVersion = "delivery-v1"
)

type DatasetArtifact struct {
	SHA256      string `json:"sha256"`
	Bytes       int64  `json:"bytes"`
	ContentType string `json:"content_type"`
}

type CurationStats struct {
	ManifestCount    int            `json:"manifest_count"`
	IncludedCount    int            `json:"included_count"`
	ExcludedCount    int            `json:"excluded_count"`
	ExcludedByReason map[string]int `json:"excluded_by_reason"`
}

type CuratedDatasetManifest struct {
	SchemaVersion        string                     `json:"schema_version"`
	DatasetType          string                     `json:"dataset_type"`
	SourceKind           string                     `json:"source_kind"`
	GeneratedAt          string                     `json:"generated_at"`
	TransformVersion     string                     `json:"transform_version"`
	RedactionVersion     string                     `json:"redaction_version"`
	RightsPolicy         string                     `json:"rights_policy"`
	CaptureManifestCount int                        `json:"capture_manifest_count"`
	SampleCount          int                        `json:"sample_count"`
	ExcludedCount        int                        `json:"excluded_count"`
	ExcludedByReason     map[string]int             `json:"excluded_by_reason"`
	HumanReviewRequired  bool                       `json:"human_review_required"`
	Files                map[string]DatasetArtifact `json:"files"`
}

type DeliveryDatasetManifest struct {
	SchemaVersion        string                     `json:"schema_version"`
	ReleaseID            string                     `json:"release_id"`
	DatasetType          string                     `json:"dataset_type"`
	Recipient            string                     `json:"recipient"`
	Contract             string                     `json:"contract"`
	AllowedPurpose       string                     `json:"allowed_purpose"`
	CreatedAt            string                     `json:"created_at"`
	SourceManifestSHA256 string                     `json:"source_manifest_sha256"`
	SourceSampleCount    int                        `json:"source_sample_count"`
	SampleCount          int                        `json:"sample_count"`
	ProvenanceIncluded   bool                       `json:"provenance_included"`
	RawCaptureIncluded   bool                       `json:"raw_capture_included"`
	Immutable            bool                       `json:"immutable"`
	ReviewedBy           string                     `json:"reviewed_by"`
	ReviewReference      string                     `json:"review_reference"`
	Files                map[string]DatasetArtifact `json:"files"`
}

type ProvenanceRecord struct {
	SampleID             string   `json:"sample_id"`
	SourceCaptureID      string   `json:"source_capture_id"`
	SourceManifest       string   `json:"source_manifest"`
	RightsRef            string   `json:"rights_ref"`
	UserSubjectRef       string   `json:"user_subject_ref,omitempty"`
	APIKeySubjectRef     string   `json:"api_key_subject_ref,omitempty"`
	RightsStatus         string   `json:"rights_status"`
	RightsVersion        int64    `json:"rights_version,omitempty"`
	AllowedPurposes      []string `json:"allowed_purposes"`
	AllowedDatasetTypes  []string `json:"allowed_dataset_types"`
	AllowedRecipients    []string `json:"allowed_recipients"`
	DatasetType          string   `json:"dataset_type"`
	Split                string   `json:"split"`
	ClientModel          string   `json:"client_model,omitempty"`
	AssistantSourceModel string   `json:"assistant_source_model,omitempty"`
	StartedAt            string   `json:"started_at,omitempty"`
	FinishedAt           string   `json:"finished_at,omitempty"`
	RequestSHA256        string   `json:"request_sha256"`
	ResponseSHA256       string   `json:"response_sha256"`
	ContentSHA256        string   `json:"content_sha256"`
	TransformVersion     string   `json:"transform_version"`
}

func readJSONLines[T any](filename string) ([]T, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filename, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var result []T
	for row := 1; ; row++ {
		var value T
		if err := decoder.Decode(&value); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode %s row %d: %w", filename, row, err)
		}
		result = append(result, value)
	}
	return result, nil
}

func readJSONFile(filename string, target any) error {
	encoded, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read %s: %w", filename, err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		return fmt.Errorf("decode %s: %w", filename, err)
	}
	return nil
}

func writeJSONFile(filename string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", filename, err)
	}
	encoded = append(encoded, '\n')
	if err := writePrivateFile(filename, encoded); err != nil {
		return fmt.Errorf("write %s: %w", filename, err)
	}
	return nil
}

func writeJSONLines[T any](filename string, values []T) error {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", filename, err)
	}
	closeFile := func() error {
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("close %s: %w", filename, closeErr)
		}
		return nil
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	for index, value := range values {
		if err := encoder.Encode(value); err != nil {
			_ = file.Close()
			return fmt.Errorf("encode %s row %d: %w", filename, index+1, err)
		}
	}
	return closeFile()
}

func writePrivateFile(filename string, content []byte) error {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writePrivateText(filename, content string) error {
	return writePrivateFile(filename, []byte(content))
}

func copyPrivateFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open %s: %w", source, err)
	}
	defer input.Close()

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", destination, err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("copy %s to %s: %w", source, destination, err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close %s: %w", destination, err)
	}
	return nil
}

func hashFile(filename string) (DatasetArtifact, error) {
	file, err := os.Open(filename)
	if err != nil {
		return DatasetArtifact{}, fmt.Errorf("open %s for hashing: %w", filename, err)
	}
	defer file.Close()

	hash := sha256.New()
	bytesWritten, err := io.Copy(hash, file)
	if err != nil {
		return DatasetArtifact{}, fmt.Errorf("hash %s: %w", filename, err)
	}
	return DatasetArtifact{
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		Bytes:       bytesWritten,
		ContentType: contentTypeForFile(filename),
	}, nil
}

func hashBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func contentTypeForFile(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".jsonl", ".ndjson":
		return "application/x-ndjson"
	case ".json":
		return "application/json"
	case ".md":
		return "text/markdown; charset=utf-8"
	case ".zst":
		return "application/zstd"
	default:
		return "application/octet-stream"
	}
}

func safeExistingBundlePath(root, relative string) (string, error) {
	root = strings.TrimSpace(root)
	relative = strings.TrimSpace(relative)
	if root == "" || relative == "" {
		return "", fmt.Errorf("bundle path requires a root and relative path")
	}
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("bundle path %q must be relative", relative)
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("bundle path %q escapes the capture directory", relative)
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve bundle root %q: %w", root, err)
	}
	target := filepath.Join(rootAbs, clean)
	rel, err := filepath.Rel(rootAbs, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("bundle path %q escapes the capture directory", relative)
	}
	info, err := os.Lstat(target)
	if err != nil {
		return "", fmt.Errorf("stat capture body %q: %w", relative, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("capture body %q must not be a symlink", relative)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("capture body %q is not a regular file", relative)
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("resolve capture root %q: %w", root, err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", fmt.Errorf("resolve capture body %q: %w", relative, err)
	}
	resolvedRel, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil || resolvedRel == ".." || strings.HasPrefix(resolvedRel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("capture body %q resolves outside the capture directory", relative)
	}
	return target, nil
}

func ensureDistinctDirectories(inputDir, outputDir string) error {
	inputAbs, err := filepath.Abs(inputDir)
	if err != nil {
		return fmt.Errorf("resolve input directory: %w", err)
	}
	outputAbs, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	inputAbs = filepath.Clean(inputAbs)
	outputAbs = filepath.Clean(outputAbs)
	if inputAbs == outputAbs {
		return fmt.Errorf("input and output directories must be different")
	}
	rel, err := filepath.Rel(inputAbs, outputAbs)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "../" {
		return fmt.Errorf("output directory %q cannot be inside input directory %q", outputDir, inputDir)
	}
	return nil
}

func sortedArtifactNames(files map[string]DatasetArtifact) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func writeSHA256Sums(directory string, names []string) error {
	sort.Strings(names)
	var builder strings.Builder
	for _, name := range names {
		filename, err := safeOutputPath(directory, name)
		if err != nil {
			return err
		}
		artifact, err := hashFile(filename)
		if err != nil {
			return err
		}
		builder.WriteString(artifact.SHA256)
		builder.WriteString("  ")
		builder.WriteString(name)
		builder.WriteByte('\n')
	}
	return writePrivateText(filepath.Join(directory, "SHA256SUMS"), builder.String())
}

func parseSHA256Sums(content []byte) (map[string]string, error) {
	result := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || len(parts[0]) != sha256.Size*2 {
			return nil, fmt.Errorf("invalid SHA256SUMS line %d", lineNumber)
		}
		if _, err := hex.DecodeString(parts[0]); err != nil {
			return nil, fmt.Errorf("invalid SHA256SUMS digest on line %d: %w", lineNumber, err)
		}
		name := strings.TrimSpace(parts[1])
		if name == "" {
			return nil, fmt.Errorf("empty SHA256SUMS filename on line %d", lineNumber)
		}
		if _, err := safeOutputPath(".", name); err != nil {
			return nil, fmt.Errorf("invalid SHA256SUMS filename %q: %w", name, err)
		}
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("duplicate SHA256SUMS filename %q", name)
		}
		result[name] = strings.ToLower(parts[0])
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SHA256SUMS: %w", err)
	}
	return result, nil
}

func safeOutputPath(root, name string) (string, error) {
	if strings.TrimSpace(name) == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("output filename %q must be relative", name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("output filename %q escapes the output directory", name)
	}
	return filepath.Join(root, clean), nil
}
