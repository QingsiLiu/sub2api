package trainingdata

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type RightsUpsertInput struct {
	RightsID            string
	ScopeType           string
	ScopeRef            string
	Status              RightsStatus
	ConsentOrContractID string
	AllowedPurposes     []string
	AllowedDatasetTypes []string
	AllowedRecipients   []string
	Region              string
	EffectiveAt         time.Time
	ExpiresAt           *time.Time
	EvidenceURI         string
	EvidenceSHA256      string
	Metadata            map[string]any
}

func UpsertRights(ctx context.Context, db *sql.DB, input RightsUpsertInput, actor, reason string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("nil database")
	}
	input.ScopeType = strings.ToLower(strings.TrimSpace(input.ScopeType))
	if input.ScopeType != "user" && input.ScopeType != "api_key" {
		return "", fmt.Errorf("scope_type must be user or api_key")
	}
	input.ScopeRef = strings.ToLower(strings.TrimSpace(input.ScopeRef))
	if !ValidSubjectRef(input.ScopeRef) {
		return "", fmt.Errorf("scope_ref must be a 64-character HMAC SHA-256 hex value")
	}
	if input.Status == "" {
		input.Status = RightsUnknown
	}
	if !ValidRightsStatus(input.Status) {
		return "", fmt.Errorf("unsupported rights status %q", input.Status)
	}
	if input.EffectiveAt.IsZero() {
		input.EffectiveAt = time.Now().UTC()
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(input.EffectiveAt) {
		return "", fmt.Errorf("expires_at must be after effective_at")
	}
	input.AllowedPurposes = normalizedStringList(input.AllowedPurposes)
	input.AllowedDatasetTypes = normalizedStringList(input.AllowedDatasetTypes)
	input.AllowedRecipients = normalizedStringList(input.AllowedRecipients)
	for _, datasetType := range input.AllowedDatasetTypes {
		if !ValidDatasetType(datasetType) {
			return "", fmt.Errorf("unsupported dataset type %q", datasetType)
		}
	}
	if input.Status == RightsEligible {
		if strings.TrimSpace(input.ConsentOrContractID) == "" {
			return "", fmt.Errorf("eligible rights require a consent or contract identifier")
		}
		if !containsString(input.AllowedPurposes, "model_training") {
			return "", fmt.Errorf("eligible rights must allow model_training")
		}
		if len(input.AllowedDatasetTypes) == 0 || len(input.AllowedRecipients) == 0 {
			return "", fmt.Errorf("eligible rights require dataset types and recipients")
		}
	}
	input.EvidenceSHA256 = strings.ToLower(strings.TrimSpace(input.EvidenceSHA256))
	if input.EvidenceSHA256 != "" {
		decoded, err := hex.DecodeString(input.EvidenceSHA256)
		if err != nil || len(decoded) != 32 {
			return "", fmt.Errorf("evidence_sha256 must be a 64-character SHA-256 hex value")
		}
	}
	if strings.TrimSpace(input.RightsID) != "" {
		if _, err := uuid.Parse(strings.TrimSpace(input.RightsID)); err != nil {
			return "", fmt.Errorf("rights_id must be a valid UUID")
		}
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	metadata, err := json.Marshal(input.Metadata)
	if err != nil {
		return "", fmt.Errorf("marshal rights metadata: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin rights transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var rightsID string
	var version int64
	var previousStatus RightsStatus
	err = tx.QueryRowContext(ctx, `
		SELECT rights_id::text, version, status
		FROM training_rights
		WHERE scope_type=$1 AND scope_ref=$2
		FOR UPDATE`, input.ScopeType, input.ScopeRef).Scan(&rightsID, &version, &previousStatus)
	if err == sql.ErrNoRows {
		rightsID = strings.TrimSpace(input.RightsID)
		if rightsID == "" {
			rightsID = uuid.NewString()
		}
		version = 1
		_, err = tx.ExecContext(ctx, `
			INSERT INTO training_rights (
				rights_id, scope_type, scope_ref, status, version, consent_or_contract_id,
				allowed_purposes, allowed_dataset_types, allowed_recipients, region,
				effective_at, expires_at, revoked_at, evidence_uri, evidence_sha256, metadata
			) VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,
			          CASE WHEN $4='withdrawn' THEN NOW() ELSE NULL END,$13,$14,$15::jsonb)`,
			rightsID, input.ScopeType, input.ScopeRef, input.Status, version, input.ConsentOrContractID,
			pq.Array(input.AllowedPurposes), pq.Array(input.AllowedDatasetTypes), pq.Array(input.AllowedRecipients),
			input.Region, input.EffectiveAt, input.ExpiresAt, input.EvidenceURI, input.EvidenceSHA256, metadata)
	} else if err == nil {
		version++
		_, err = tx.ExecContext(ctx, `
			UPDATE training_rights SET
				status=$1, version=$2, consent_or_contract_id=$3,
				allowed_purposes=$4, allowed_dataset_types=$5, allowed_recipients=$6,
				region=$7, effective_at=$8, expires_at=$9,
				revoked_at=CASE WHEN $1='withdrawn' THEN NOW() ELSE NULL END,
				evidence_uri=$10, evidence_sha256=$11, metadata=$12::jsonb, updated_at=NOW()
			WHERE rights_id=$13::uuid`,
			input.Status, version, input.ConsentOrContractID,
			pq.Array(input.AllowedPurposes), pq.Array(input.AllowedDatasetTypes), pq.Array(input.AllowedRecipients),
			input.Region, input.EffectiveAt, input.ExpiresAt, input.EvidenceURI, input.EvidenceSHA256, metadata, rightsID)
	} else {
		return "", fmt.Errorf("lock current rights: %w", err)
	}
	if err != nil {
		return "", fmt.Errorf("write rights: %w", err)
	}
	eventType := "updated"
	if version == 1 {
		eventType = "created"
	}
	if input.Status == RightsWithdrawn {
		eventType = "withdrawn"
	}
	if input.Status == RightsExcluded {
		eventType = "excluded"
	}
	if input.Status == RightsLegalHold {
		eventType = "legal_hold"
	}
	if input.Status == RightsExpired {
		eventType = "expired"
	}
	if version > 1 && input.Status == RightsEligible && previousStatus != RightsEligible {
		eventType = "restored"
	}
	var snapshot []byte
	if err := tx.QueryRowContext(ctx, `
		SELECT row_to_json(current_row)::text
		FROM training_rights AS current_row
		WHERE rights_id=$1::uuid`, rightsID).Scan(&snapshot); err != nil {
		return "", fmt.Errorf("snapshot current rights: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO training_rights_events (rights_id, version, event_type, snapshot, actor_ref, reason)
		VALUES ($1::uuid,$2,$3,$4::jsonb,$5,$6)`, rightsID, version, eventType, snapshot, actor, reason); err != nil {
		return "", fmt.Errorf("append rights event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit rights transaction: %w", err)
	}
	return rightsID, nil
}

func ValidRightsStatus(status RightsStatus) bool {
	switch status {
	case RightsUnknown, RightsEligible, RightsExcluded, RightsWithdrawn, RightsExpired, RightsLegalHold:
		return true
	default:
		return false
	}
}

// WithdrawRights is a narrow append-only ledger transition that preserves the
// existing grant fields, increments its version, stamps revoked_at and records
// the resulting full row snapshot in training_rights_events.
func WithdrawRights(ctx context.Context, db *sql.DB, scopeType, scopeRef, actor, reason string) (string, int64, error) {
	if db == nil {
		return "", 0, fmt.Errorf("nil database")
	}
	scopeType = strings.ToLower(strings.TrimSpace(scopeType))
	scopeRef = strings.ToLower(strings.TrimSpace(scopeRef))
	if (scopeType != "user" && scopeType != "api_key") || !ValidSubjectRef(scopeRef) {
		return "", 0, fmt.Errorf("valid scope_type and scope_ref are required")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, fmt.Errorf("begin rights withdrawal: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var rightsID string
	var version int64
	if err := tx.QueryRowContext(ctx, `
		SELECT rights_id::text, version
		FROM training_rights
		WHERE scope_type=$1 AND scope_ref=$2
		FOR UPDATE`, scopeType, scopeRef).Scan(&rightsID, &version); err != nil {
		return "", 0, fmt.Errorf("lock rights for withdrawal: %w", err)
	}
	version++
	if _, err := tx.ExecContext(ctx, `
		UPDATE training_rights
		SET status='withdrawn', version=$1, revoked_at=NOW(), updated_at=NOW()
		WHERE rights_id=$2::uuid`, version, rightsID); err != nil {
		return "", 0, fmt.Errorf("withdraw rights: %w", err)
	}
	var snapshot []byte
	if err := tx.QueryRowContext(ctx, `
		SELECT row_to_json(current_row)::text
		FROM training_rights AS current_row
		WHERE rights_id=$1::uuid`, rightsID).Scan(&snapshot); err != nil {
		return "", 0, fmt.Errorf("snapshot withdrawn rights: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO training_rights_events (rights_id, version, event_type, snapshot, actor_ref, reason)
		VALUES ($1::uuid,$2,'withdrawn',$3::jsonb,$4,$5)`, rightsID, version, snapshot, actor, reason); err != nil {
		return "", 0, fmt.Errorf("append withdrawal event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", 0, fmt.Errorf("commit rights withdrawal: %w", err)
	}
	return rightsID, version, nil
}

func CreateDeletionRequest(ctx context.Context, db *sql.DB, scopeType, scopeRef, source, reason, idempotencyKey string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("nil database")
	}
	scopeType = strings.ToLower(strings.TrimSpace(scopeType))
	scopeRef = strings.ToLower(strings.TrimSpace(scopeRef))
	if (scopeType != "user" && scopeType != "api_key") || !ValidSubjectRef(scopeRef) {
		return "", fmt.Errorf("valid scope_type and scope_ref are required")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		idempotencyKey = uuid.NewString()
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin deletion request: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	deletionID := uuid.NewString()
	err = tx.QueryRowContext(ctx, `
		INSERT INTO training_deletion_requests (
			deletion_id, scope_type, scope_ref, source, idempotency_key, status, reason
		) VALUES ($1::uuid,$2,$3,$4,$5,'pending',$6)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING deletion_id::text`, deletionID, scopeType, scopeRef, source, idempotencyKey, reason).Scan(&deletionID)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("create deletion request: %w", err)
	}
	if err == sql.ErrNoRows {
		var existingScopeType, existingScopeRef string
		if err := tx.QueryRowContext(ctx, `
			SELECT deletion_id::text, scope_type, scope_ref
			FROM training_deletion_requests
			WHERE idempotency_key=$1
			FOR UPDATE`, idempotencyKey).Scan(&deletionID, &existingScopeType, &existingScopeRef); err != nil {
			return "", fmt.Errorf("load existing deletion request: %w", err)
		}
		if existingScopeType != scopeType || existingScopeRef != scopeRef {
			return "", fmt.Errorf("idempotency key already belongs to a different deletion scope")
		}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO training_deletion_targets (deletion_id, target_type, status)
		VALUES ($1::uuid,'raw','pending'),($1::uuid,'spool','pending'),
		       ($1::uuid,'prompt_audit','pending'),($1::uuid,'curated_release','pending'),
		       ($1::uuid,'buyer_release','pending'),($1::uuid,'buyer_notice','pending')
		ON CONFLICT DO NOTHING`, deletionID)
	if err != nil {
		return "", fmt.Errorf("create deletion targets: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit deletion request: %w", err)
	}
	return deletionID, nil
}

func normalizedStringList(values []string) []string {
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
