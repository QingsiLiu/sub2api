package trainingdata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

func loadActiveRights(ctx context.Context, db *sql.DB, now time.Time) ([]RightsGrant, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT rights_id::text, scope_type, scope_ref, version, consent_or_contract_id, status,
		       allowed_purposes, allowed_dataset_types, allowed_recipients, region,
		       effective_at, expires_at, revoked_at, evidence_uri
		FROM training_rights
		WHERE effective_at <= $1`, now)
	if err != nil {
		return nil, fmt.Errorf("load training rights: %w", err)
	}
	defer rows.Close()
	var grants []RightsGrant
	for rows.Next() {
		var grant RightsGrant
		var expiresAt, revokedAt sql.NullTime
		if err := rows.Scan(
			&grant.RightsID, &grant.ScopeType, &grant.ScopeRef, &grant.Version,
			&grant.ConsentOrContractID, &grant.Status,
			pq.Array(&grant.AllowedPurposes), pq.Array(&grant.AllowedDatasetTypes),
			pq.Array(&grant.AllowedRecipients), &grant.Region, &grant.EffectiveAt,
			&expiresAt, &revokedAt, &grant.EvidenceURI,
		); err != nil {
			return nil, fmt.Errorf("scan training right: %w", err)
		}
		if expiresAt.Valid {
			value := expiresAt.Time
			grant.ExpiresAt = &value
		}
		if revokedAt.Valid {
			value := revokedAt.Time
			grant.RevokedAt = &value
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate training rights: %w", err)
	}
	return grants, nil
}

// LoadRightsByID returns the current effective rights rows keyed by rights_id.
// Withdrawn, excluded, expired and legal-hold rows remain present so offline
// curation/delivery can explicitly reject them instead of treating them as
// missing historical metadata.
func LoadRightsByID(ctx context.Context, db *sql.DB, now time.Time) (map[string]RightsGrant, error) {
	if db == nil {
		return nil, errors.New("nil training data database")
	}
	grants, err := loadActiveRights(ctx, db, now)
	if err != nil {
		return nil, err
	}
	result := make(map[string]RightsGrant, len(grants))
	for _, grant := range grants {
		if existing, ok := result[grant.RightsID]; ok && existing.Version >= grant.Version {
			continue
		}
		result[grant.RightsID] = grant
	}
	return result, nil
}

func upsertCaptureIndex(ctx context.Context, db *sql.DB, index CaptureIndex) error {
	if db == nil {
		return errors.New("nil training data database")
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO training_captures (
			capture_id, request_id, client_request_id, user_subject_ref, api_key_subject_ref,
			rights_id, rights_version, rights_status, started_at, finished_at, route, method,
			protocol, client_model, upstream_models, stream, http_status,
			duration_ms, attempt_count, capture_complete, capture_status,
			incomplete_reasons, request_bytes, upstream_request_bytes,
			upstream_response_bytes, client_response_bytes, raw_object_prefix,
			raw_manifest_key, redaction_version
		) VALUES (
			$1::uuid,$2,$3,$4,$5,$6::uuid,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,
			$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29
		)
		ON CONFLICT (capture_id) DO UPDATE SET
			finished_at=EXCLUDED.finished_at,
			upstream_models=EXCLUDED.upstream_models,
			http_status=EXCLUDED.http_status,
			duration_ms=EXCLUDED.duration_ms,
			attempt_count=EXCLUDED.attempt_count,
			capture_complete=EXCLUDED.capture_complete,
			capture_status=EXCLUDED.capture_status,
			incomplete_reasons=EXCLUDED.incomplete_reasons,
			request_bytes=EXCLUDED.request_bytes,
			upstream_request_bytes=EXCLUDED.upstream_request_bytes,
			upstream_response_bytes=EXCLUDED.upstream_response_bytes,
			client_response_bytes=EXCLUDED.client_response_bytes,
			raw_object_prefix=EXCLUDED.raw_object_prefix,
			raw_manifest_key=EXCLUDED.raw_manifest_key`,
		index.CaptureID, index.RequestID, index.ClientRequestID, index.UserSubjectRef, index.APIKeySubjectRef,
		nullableUUID(index.RightsID), nullableInt64(index.RightsVersion), index.RightsStatus,
		index.StartedAt, index.FinishedAt, index.Route,
		index.Method, index.Protocol, index.ClientModel, pq.Array(index.UpstreamModels), index.Stream,
		index.HTTPStatus, index.DurationMS, index.AttemptCount, index.CaptureComplete,
		index.CaptureStatus, pq.Array(index.IncompleteReasons), index.RequestBytes,
		index.UpstreamRequestBytes, index.UpstreamResponseBytes, index.ClientResponseBytes,
		index.RawObjectPrefix, index.RawManifestKey, index.RedactionVersion,
	)
	if err != nil {
		return fmt.Errorf("upsert training capture index: %w", err)
	}
	return nil
}

func nullableUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func createDeliveryAudit(ctx context.Context, db *sql.DB, releaseID, action, actor string, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode delivery audit metadata: %w", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO training_delivery_audits (release_id, action, actor_ref, metadata)
		VALUES ($1,$2,$3,$4::jsonb)`, releaseID, action, actor, encoded)
	if err != nil {
		return fmt.Errorf("insert delivery audit: %w", err)
	}
	return nil
}
