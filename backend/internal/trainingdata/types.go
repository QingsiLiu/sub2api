package trainingdata

import (
	"net/http"
	"time"
)

const (
	RedactionVersion = "redact-v1"
	TransformVersion = "clean-v1"
)

type RightsStatus string

const (
	RightsUnknown   RightsStatus = "unknown"
	RightsEligible  RightsStatus = "eligible"
	RightsExcluded  RightsStatus = "excluded"
	RightsWithdrawn RightsStatus = "withdrawn"
	RightsExpired   RightsStatus = "expired"
	RightsLegalHold RightsStatus = "legal_hold"
)

type RightsGrant struct {
	RightsID            string
	ScopeType           string
	ScopeRef            string
	Version             int64
	ConsentOrContractID string
	Status              RightsStatus
	AllowedPurposes     []string
	AllowedDatasetTypes []string
	AllowedRecipients   []string
	Region              string
	EffectiveAt         time.Time
	ExpiresAt           *time.Time
	RevokedAt           *time.Time
	EvidenceURI         string
}

type BeginInput struct {
	UserSubjectRef    string
	APIKeySubjectRef  string
	RequestID         string
	ClientRequestID   string
	Method            string
	Route             string
	Protocol          string
	ClientModel       string
	Stream            bool
	Headers           http.Header
	RequestBody       []byte
	IncompleteReasons []string
}

type CaptureIndex struct {
	CaptureID             string
	RequestID             string
	ClientRequestID       string
	UserSubjectRef        string
	APIKeySubjectRef      string
	RightsID              string
	RightsVersion         int64
	RightsStatus          RightsStatus
	StartedAt             time.Time
	FinishedAt            time.Time
	Route                 string
	Method                string
	Protocol              string
	ClientModel           string
	UpstreamModels        []string
	Stream                bool
	HTTPStatus            int
	DurationMS            int64
	AttemptCount          int
	CaptureComplete       bool
	CaptureStatus         string
	IncompleteReasons     []string
	RequestBytes          int64
	UpstreamRequestBytes  int64
	UpstreamResponseBytes int64
	ClientResponseBytes   int64
	RawObjectPrefix       string
	RawManifestKey        string
	RedactionVersion      string
}

type ChunkRecord struct {
	Offset int64 `json:"offset"`
	Length int   `json:"length"`
	AtMS   int64 `json:"at_ms"`
}

type HeaderSnapshot struct {
	Status  int         `json:"status,omitempty"`
	Headers http.Header `json:"headers"`
}

type CaptureArtifact struct {
	SHA256      string `json:"sha256"`
	Bytes       int64  `json:"bytes"`
	ContentType string `json:"content_type"`
}

type AttemptManifest struct {
	AttemptID       int           `json:"attempt_id"`
	Method          string        `json:"method"`
	URL             string        `json:"url"`
	Model           string        `json:"model,omitempty"`
	StartedAt       time.Time     `json:"started_at"`
	FinishedAt      *time.Time    `json:"finished_at,omitempty"`
	RequestHeaders  http.Header   `json:"request_headers"`
	ResponseHeaders http.Header   `json:"response_headers,omitempty"`
	HTTPStatus      int           `json:"http_status,omitempty"`
	RequestFile     string        `json:"request_file,omitempty"`
	ResponseFile    string        `json:"response_file,omitempty"`
	ResponseChunks  []ChunkRecord `json:"response_chunks,omitempty"`
	RequestBytes    int64         `json:"request_bytes"`
	ResponseBytes   int64         `json:"response_bytes"`
	Error           string        `json:"error,omitempty"`
	Complete        bool          `json:"complete"`
}

type Manifest struct {
	SchemaVersion        string                     `json:"schema_version"`
	CaptureID            string                     `json:"capture_id"`
	RequestID            string                     `json:"request_id,omitempty"`
	ClientRequestID      string                     `json:"client_request_id,omitempty"`
	UserSubjectRef       string                     `json:"user_subject_ref"`
	APIKeySubjectRef     string                     `json:"api_key_subject_ref"`
	RightsID             string                     `json:"rights_id,omitempty"`
	RightsVersion        int64                      `json:"rights_version,omitempty"`
	RightsStatus         RightsStatus               `json:"rights_status"`
	StartedAt            time.Time                  `json:"started_at"`
	FinishedAt           *time.Time                 `json:"finished_at,omitempty"`
	Method               string                     `json:"method"`
	Route                string                     `json:"route"`
	Protocol             string                     `json:"protocol,omitempty"`
	ClientModel          string                     `json:"client_model,omitempty"`
	Stream               bool                       `json:"stream"`
	ClientRequest        HeaderSnapshot             `json:"client_request"`
	ClientRequestFile    string                     `json:"client_request_file,omitempty"`
	ClientRequestBytes   int64                      `json:"client_request_bytes"`
	ClientResponse       HeaderSnapshot             `json:"client_response"`
	ClientResponseFile   string                     `json:"client_response_file,omitempty"`
	ClientResponseBytes  int64                      `json:"client_response_bytes"`
	ClientResponseChunks []ChunkRecord              `json:"client_response_chunks,omitempty"`
	Attempts             []AttemptManifest          `json:"attempts"`
	IncompleteReasons    []string                   `json:"incomplete_reasons,omitempty"`
	RedactionVersion     string                     `json:"redaction_version"`
	CaptureComplete      bool                       `json:"capture_complete"`
	Files                map[string]CaptureArtifact `json:"files"`
}
