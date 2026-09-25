package repository

// This is an evidence-only repair path. It deliberately does not depend on the
// billing service, and never calls Apply or updates balances, quotas or ledgers.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const UsageRecoveryManifestVersion = 1

var recoveryUnknownFields = []string{"account_id", "group_id", "model", "requested_model", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_cost", "rate_multiplier", "duration_ms", "completed_at"}

type UsageRecoveryCandidate struct {
	RequestID          string          `json:"request_id"`
	APIKeyID           int64           `json:"api_key_id"`
	RequestFingerprint string          `json:"request_fingerprint"`
	Kind               string          `json:"kind"`
	Disposition        string          `json:"disposition"`
	Reason             string          `json:"reason,omitempty"`
	UserID             *int64          `json:"user_id"`
	SubscriptionID     *int64          `json:"subscription_id"`
	AdmissionKey       *string         `json:"admission_key"`
	UsageRequestID     *string         `json:"usage_request_id"`
	MappingSource      string          `json:"mapping_source"`
	AmountUSD          *string         `json:"amount_usd"`
	AmountSource       string          `json:"amount_source"`
	AccountingDate     *string         `json:"accounting_date"`
	AdmittedAt         *time.Time      `json:"admitted_at"`
	SettledAt          *time.Time      `json:"settled_at"`
	CompletedAt        *time.Time      `json:"completed_at"`
	UnknownFields      []string        `json:"unknown_fields"`
	Evidence           json.RawMessage `json:"evidence"`
}
type UsageRecoverySummary struct {
	Candidates         int    `json:"candidates"`
	Recoverable        int    `json:"recoverable"`
	Unresolved         int    `json:"unresolved"`
	KnownAmountUSD     string `json:"known_amount_usd"`
	UnknownAmountCount int    `json:"unknown_amount_count"`
}
type UsageRecoveryManifest struct {
	Version    int                      `json:"version"`
	From       time.Time                `json:"from"`
	Cutoff     time.Time                `json:"cutoff"`
	ScannedAt  time.Time                `json:"scanned_at"`
	Database   string                   `json:"database"`
	Snapshot   string                   `json:"snapshot"`
	Candidates []UsageRecoveryCandidate `json:"candidates"`
	Summary    UsageRecoverySummary     `json:"summary"`
}
type UsageRecoveryApplyResult struct {
	Inserted         int `json:"inserted"`
	AlreadyRecovered int `json:"already_recovered"`
	DetailArrived    int `json:"detail_arrived"`
	Unresolved       int `json:"unresolved"`
	CommittedBatches int `json:"committed_batches"`
}
type UsageRecoveryRepository struct{ db *sql.DB }

func NewUsageRecoveryRepository(db *sql.DB) *UsageRecoveryRepository {
	return &UsageRecoveryRepository{db: db}
}

// Digest covers every field, including unresolved candidates and the cutoff.
// Callers should retain this SHA outside the manifest before enabling writes.
func UsageRecoveryManifestDigest(m *UsageRecoveryManifest) (string, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func recoverySummary(candidates []UsageRecoveryCandidate) (UsageRecoverySummary, error) {
	s := UsageRecoverySummary{Candidates: len(candidates)}
	total := decimal.Zero
	for _, c := range candidates {
		if c.Disposition == "recoverable" {
			s.Recoverable++
		} else {
			s.Unresolved++
		}
		if c.AmountUSD == nil {
			s.UnknownAmountCount++
			continue
		}
		n, err := decimal.NewFromString(*c.AmountUSD)
		if err != nil || n.IsNegative() {
			return s, fmt.Errorf("invalid amount for %s", c.RequestID)
		}
		if c.Disposition == "recoverable" {
			total = total.Add(n)
		}
	}
	s.KnownAmountUSD = total.StringFixed(10)
	return s, nil
}
func validateRecoveryManifest(m *UsageRecoveryManifest, sha string, cutoff time.Time) error {
	if m == nil || m.Version != UsageRecoveryManifestVersion {
		return errors.New("unsupported recovery manifest")
	}
	if m.From.IsZero() || !m.Cutoff.After(m.From) || !m.Cutoff.Equal(cutoff) || m.Cutoff.After(m.ScannedAt) {
		return errors.New("manifest range/cutoff invalid or does not match explicit cutoff")
	}
	actual, err := UsageRecoveryManifestDigest(m)
	if err != nil {
		return err
	}
	if sha == "" || actual != strings.ToLower(sha) {
		return errors.New("manifest SHA256 does not match approval hash")
	}
	summary, err := recoverySummary(m.Candidates)
	if err != nil {
		return err
	}
	if summary != m.Summary {
		return errors.New("manifest summary mismatch")
	}
	seen := map[string]bool{}
	for _, c := range m.Candidates {
		key := fmt.Sprintf("%d:%s", c.APIKeyID, c.RequestID)
		if c.RequestID == "" || c.APIKeyID <= 0 || seen[key] {
			return fmt.Errorf("invalid/duplicate financial identity %s", key)
		}
		seen[key] = true
		if c.Disposition != "recoverable" && c.Disposition != "unresolved" {
			return errors.New("unsupported recovery disposition")
		}
		if c.Disposition == "recoverable" && (c.Kind != "subscription" || c.UserID == nil || c.SubscriptionID == nil || c.AdmissionKey == nil || c.UsageRequestID == nil || c.AmountUSD == nil || c.AccountingDate == nil || c.AdmittedAt == nil || c.SettledAt == nil) {
			return fmt.Errorf("incomplete recoverable evidence for %s", key)
		}
	}
	return nil
}

// Scan uses one read-only repeatable-read snapshot. Endpoints are settlement
// times [from,cutoff); the stored accounting date remains the admission day.
func (r *UsageRecoveryRepository) Scan(ctx context.Context, from, cutoff time.Time) (*UsageRecoveryManifest, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("nil recovery database")
	}
	if from.IsZero() || !cutoff.After(from) || cutoff.After(time.Now()) {
		return nil, errors.New("explicit historical from and cutoff are required")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL timezone='UTC'; SET LOCAL statement_timeout='120s'`); err != nil {
		return nil, err
	}
	m := &UsageRecoveryManifest{Version: UsageRecoveryManifestVersion, From: from.UTC(), Cutoff: cutoff.UTC(), Candidates: []UsageRecoveryCandidate{}}
	if err = tx.QueryRowContext(ctx, `SELECT current_database(),txid_current_snapshot()::text,clock_timestamp()`).Scan(&m.Database, &m.Snapshot, &m.ScannedAt); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, recoverySubscriptionSQL+` AND sr.settled_at >= $1 AND sr.settled_at < $2
 AND NOT (sr.billing_request_id ~ '^(local:|client:|generated:|web_search:|grok-video:|grok_audio:|grok_realtime:).+'
  AND EXISTS(SELECT 1 FROM usage_logs ul WHERE ul.api_key_id=sr.api_key_id AND ul.request_id=sr.billing_request_id
   AND ul.user_id=s.user_id AND ul.subscription_id=sr.subscription_id AND ul.actual_cost=sr.cost_usd))
 ORDER BY sr.id`, from, cutoff)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c, e := scanRecoverySubscription(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		if c.Disposition != "has_detail" {
			m.Candidates = append(m.Candidates, c)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	// Missing ordinary dedup rows have neither amounts nor historical owner IDs.
	// Keep them in the manifest, never attach them to a mutable current API key.
	rows, err = tx.QueryContext(ctx, recoveryUnknownDedupSQL, from, cutoff)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c UsageRecoveryCandidate
		var created time.Time
		var fingerprints int
		var sources string
		if err = rows.Scan(&c.RequestID, &c.APIKeyID, &c.RequestFingerprint, &created, &fingerprints, &sources); err != nil {
			rows.Close()
			return nil, err
		}
		c.Kind = "unattributed_billing_operation"
		c.Disposition = "unresolved"
		c.Reason = "dedup proves an operation, not historical owner, debit kind or amount"
		if fingerprints != 1 {
			c.Reason = "conflicting archived/current fingerprints"
		}
		c.AmountSource = "unavailable: one-way dedup fingerprint"
		c.MappingSource = "unproven"
		c.UnknownFields = append(append([]string{}, recoveryUnknownFields...), "user_id", "subscription_id", "actual_cost", "accounting_date", "settled_at")
		c.Evidence, _ = json.Marshal(map[string]any{"dedup_created_at": created.UTC(), "dedup_sources": sources, "fingerprint_versions": fingerprints})
		m.Candidates = append(m.Candidates, c)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	m.Summary, err = recoverySummary(m.Candidates)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return m, nil
}

// Evidence is deliberately based on the admission's saved lot identities and
// immutable settlement/allocation records, not today's API-key configuration.
const recoverySubscriptionSQL = `
SELECT sr.request_key,sr.subscription_id,sr.api_key_id,sr.billing_request_id,
 sr.cost_usd::text,sr.admitted_at,sr.settled_at,s.user_id,
 COALESCE(rc.usage_date,(sr.admitted_at AT TIME ZONE 'Asia/Shanghai')::date)::text,
 COALESCE(d.fingerprint,''),COALESCE(d.fingerprint_count,0),
 COALESCE(a.amount,0)::text,COALESCE(a.invalid_count,0),COALESCE(a.items,'[]'::jsonb),
 (sc.user_id IS NULL OR sc.user_id=s.user_id),
 (SELECT COUNT(*) FROM subscription_requests duplicate WHERE duplicate.status='settled'
   AND duplicate.billing_request_id=sr.billing_request_id AND duplicate.api_key_id=sr.api_key_id),
 sr.lots,COALESCE(rc.term_id,''),COALESCE(d.sources,''),
 EXISTS(SELECT 1 FROM usage_logs ul WHERE ul.api_key_id=sr.api_key_id AND ul.request_id=sr.billing_request_id),
 EXISTS(SELECT 1 FROM usage_logs ul WHERE ul.api_key_id=sr.api_key_id AND ul.request_id=sr.billing_request_id
  AND ul.user_id=s.user_id AND ul.subscription_id=sr.subscription_id AND ul.actual_cost=sr.cost_usd),
 (rc.request_key IS NULL OR (rt.subscription_id=sr.subscription_id
   AND rc.usage_date=(sr.admitted_at AT TIME ZONE 'Asia/Shanghai')::date))
FROM subscription_requests sr
JOIN user_subscriptions s ON s.id=sr.subscription_id
LEFT JOIN subscription_contracts sc ON sc.subscription_id=sr.subscription_id
LEFT JOIN subscription_request_contracts rc ON rc.request_key=sr.request_key
LEFT JOIN subscription_contract_terms rt ON rt.term_id=rc.term_id
LEFT JOIN LATERAL (
 SELECT MIN(request_fingerprint) fingerprint,COUNT(DISTINCT request_fingerprint) fingerprint_count,
 string_agg(DISTINCT source,',' ORDER BY source) sources
 FROM (SELECT request_fingerprint,'hot' source FROM usage_billing_dedup WHERE request_id=sr.billing_request_id AND api_key_id=sr.api_key_id
 UNION ALL SELECT request_fingerprint,'archive' source FROM usage_billing_dedup_archive WHERE request_id=sr.billing_request_id AND api_key_id=sr.api_key_id) x
) d ON TRUE
LEFT JOIN LATERAL (
 SELECT SUM(sa.cost_usd) amount,COUNT(*) FILTER(WHERE sa.cost_usd<0 OR e.id IS NULL
  OR e.user_subscription_id<>sr.subscription_id OR NOT EXISTS(
   SELECT 1 FROM jsonb_array_elements(sr.lots) lot WHERE (lot->>'id')::bigint=sa.entitlement_id
    AND (lot->>'user_subscription_id')::bigint=sr.subscription_id)) invalid_count,
 jsonb_agg(jsonb_build_object('id',sa.id,'entitlement_id',sa.entitlement_id,'amount',sa.cost_usd::text,
  'subscription_id',e.user_subscription_id,'daily_window_start',sa.daily_window_start,
  'weekly_window_start',sa.weekly_window_start,'monthly_window_start',sa.monthly_window_start)
  ORDER BY sa.id) items
 FROM subscription_usage_allocations sa LEFT JOIN user_subscription_entitlements e ON e.id=sa.entitlement_id
 WHERE sa.request_key=sr.request_key
) a ON TRUE
WHERE sr.status='settled' AND sr.billing_request_id<>'' AND sr.settled_at IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.request_id=sr.billing_request_id AND r.api_key_id=sr.api_key_id)
`
const recoveryUnknownDedupSQL = `
WITH d AS (
 SELECT request_id,api_key_id,request_fingerprint,created_at,'hot' source FROM usage_billing_dedup WHERE created_at >= $1 AND created_at < $2
 UNION ALL
 SELECT request_id,api_key_id,request_fingerprint,created_at,'archive' source FROM usage_billing_dedup_archive WHERE created_at >= $1 AND created_at < $2
)
SELECT d.request_id,d.api_key_id,MIN(d.request_fingerprint),MIN(d.created_at),COUNT(DISTINCT d.request_fingerprint),string_agg(DISTINCT d.source,',' ORDER BY d.source)
FROM d WHERE NOT EXISTS(SELECT 1 FROM subscription_requests sr WHERE sr.status='settled' AND sr.billing_request_id=d.request_id AND sr.api_key_id=d.api_key_id)
 AND NOT EXISTS(SELECT 1 FROM usage_logs ul WHERE ul.request_id=d.request_id AND ul.api_key_id=d.api_key_id)
 AND NOT EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.request_id=d.request_id AND r.api_key_id=d.api_key_id)
GROUP BY d.request_id,d.api_key_id ORDER BY MIN(d.created_at),d.api_key_id,d.request_id`

type recoveryScanner interface{ Scan(...any) error }

func scanRecoverySubscription(row recoveryScanner) (UsageRecoveryCandidate, error) {
	var c UsageRecoveryCandidate
	var key, amount, date, allocated, term, sources string
	var sub, user int64
	var admitted, settled time.Time
	var fingerprints, invalid, duplicates int
	var allocations, lots []byte
	var ownerOK, logExists, logMatches, dateOK bool
	err := row.Scan(&key, &sub, &c.APIKeyID, &c.RequestID, &amount, &admitted, &settled, &user, &date, &c.RequestFingerprint, &fingerprints, &allocated, &invalid, &allocations, &ownerOK, &duplicates, &lots, &term, &sources, &logExists, &logMatches, &dateOK)
	if err != nil {
		return c, err
	}
	admitted = admitted.UTC()
	settled = settled.UTC()
	c.Kind = "subscription"
	c.Disposition = "recoverable"
	c.UserID = &user
	c.SubscriptionID = &sub
	c.AdmissionKey = &key
	c.AccountingDate = &date
	c.AdmittedAt = &admitted
	c.SettledAt = &settled
	c.UnknownFields = append([]string{}, recoveryUnknownFields...)
	c.AmountSource = "subscription_requests.cost_usd cross-checked against subscription_usage_allocations"
	n, e := decimal.NewFromString(amount)
	sum, es := decimal.NewFromString(allocated)
	c.AmountUSD = &amount
	if e != nil || es != nil || n.IsNegative() || !n.Equal(sum) || invalid != 0 || duplicates != 1 || !ownerOK || !dateOK || fingerprints != 1 || strings.TrimSpace(c.RequestFingerprint) == "" {
		c.Disposition = "unresolved"
		c.Reason = "settlement/allocations/owner/dedup identity evidence does not agree"
		c.AmountUSD = nil
		c.AmountSource = "unverified: conflicting settlement evidence"
	}
	// These namespaces are emitted by both historical normal gateway writers as
	// the shared UsageLog.RequestID and UsageBillingCommand.RequestID. Other paths
	// must bring an explicit durable task mapping; do not assume string equality.
	if normalRecoveryRequestID(c.RequestID) {
		v := c.RequestID
		c.UsageRequestID = &v
		c.MappingSource = "normal_gateway_shared_request_id_v1"
	} else {
		c.Disposition = "unresolved"
		c.Reason = "financial-to-log mapping requires task-specific evidence"
		c.MappingSource = "unproven"
	}
	if logExists && c.UsageRequestID != nil {
		if logMatches {
			c.Disposition = "has_detail"
		} else {
			c.Disposition = "unresolved"
			c.Reason = "existing usage detail conflicts with settlement identity or amount"
		}
	}
	evidence := map[string]any{"request_key": key, "settlement_amount": amount, "allocation_amount": allocated, "allocations": json.RawMessage(allocations), "admission_lots": json.RawMessage(lots), "contract_term": term, "dedup_sources": sources, "fingerprint_versions": fingerprints, "allocation_identity_errors": invalid, "settled_identity_count": duplicates, "contract_owner_agrees": ownerOK, "admission_date_agrees": dateOK}
	c.Evidence, err = json.Marshal(evidence)
	return c, err
}
func normalRecoveryRequestID(id string) bool {
	for _, p := range []string{"local:", "client:", "generated:", "web_search:", "grok-video:", "grok_audio:", "grok_realtime:"} {
		if strings.HasPrefix(id, p) && len(id) > len(p) {
			return true
		}
	}
	return false
}

// Apply commits bounded batches. Re-running the same manifest skips precisely
// matching receipts, so an interrupted run resumes without touching money.
func (r *UsageRecoveryRepository) Apply(ctx context.Context, m *UsageRecoveryManifest, sha string, cutoff time.Time, batchSize int) (UsageRecoveryApplyResult, error) {
	var out UsageRecoveryApplyResult
	if r == nil || r.db == nil {
		return out, errors.New("nil recovery database")
	}
	if err := validateRecoveryManifest(m, sha, cutoff); err != nil {
		return out, err
	}
	if batchSize < 1 || batchSize > 500 {
		return out, errors.New("batch size must be between 1 and 500")
	}
	var database string
	if err := r.db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		return out, err
	}
	if database != m.Database {
		return out, errors.New("manifest targets another database")
	}
	for offset := 0; offset < len(m.Candidates); offset += batchSize {
		end := offset + batchSize
		if end > len(m.Candidates) {
			end = len(m.Candidates)
		}
		batch, err := r.applyRecoveryBatch(ctx, m, m.Candidates[offset:end], sha)
		if err != nil {
			return out, fmt.Errorf("batch starting at %d failed; prior batches remain committed: %w", offset, err)
		}
		out.Inserted += batch.Inserted
		out.AlreadyRecovered += batch.AlreadyRecovered
		out.DetailArrived += batch.DetailArrived
		out.Unresolved += batch.Unresolved
		out.CommittedBatches++
	}
	return out, nil
}
func (r *UsageRecoveryRepository) applyRecoveryBatch(ctx context.Context, m *UsageRecoveryManifest, candidates []UsageRecoveryCandidate, sha string) (UsageRecoveryApplyResult, error) {
	var out UsageRecoveryApplyResult
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL timezone='UTC'; SET LOCAL lock_timeout='3s'; SET LOCAL statement_timeout='30s'`); err != nil {
		return out, err
	}
	for _, c := range candidates {
		if c.Disposition != "recoverable" {
			out.Unresolved++
			continue
		}
		exists, e := matchingRecoveryReceipt(ctx, tx, c, sha)
		if e != nil {
			return out, e
		}
		if exists {
			out.AlreadyRecovered++
			continue
		}
		// SHARE fences concurrent identity migration/deletion and ordinary settlement
		// on just this parent while evidence is revalidated; no global locks.
		var user int64
		if err = tx.QueryRowContext(ctx, `SELECT user_id FROM user_subscriptions WHERE id=$1 FOR SHARE`, *c.SubscriptionID).Scan(&user); err != nil {
			return out, err
		}
		if user != *c.UserID {
			return out, errors.New("subscription owner changed after scan")
		}
		fresh, e := scanRecoverySubscription(tx.QueryRowContext(ctx, recoverySubscriptionSQL+` AND sr.request_key=$1`, *c.AdmissionKey))
		if e != nil {
			return out, fmt.Errorf("revalidate %s: %w", c.RequestID, e)
		}
		if fresh.Disposition == "has_detail" {
			if err = verifyArrivedRecoveryDetail(ctx, tx, c); err != nil {
				return out, err
			}
			out.DetailArrived++
			continue
		}
		freshJSON, _ := json.Marshal(fresh)
		approvedJSON, _ := json.Marshal(c)
		if !bytes.Equal(freshJSON, approvedJSON) {
			return out, fmt.Errorf("evidence changed after scan: %s", c.RequestID)
		}
		evidenceHash := sha256.Sum256(c.Evidence)
		detail, err := json.Marshal(map[string]any{"recovery_manifest_sha256": sha, "recovery_evidence_sha256": hex.EncodeToString(evidenceHash[:]), "recovery_amount_source": c.AmountSource, "recovery_mapping_source": c.MappingSource})
		if err != nil {
			return out, err
		}
		result, e := tx.ExecContext(ctx, `INSERT INTO usage_settlement_receipts
   (request_id,api_key_id,usage_request_id,request_fingerprint,user_id,subscription_id,billing_type,
    charged_amount,accounting_date,admitted_at,settled_at,completed_at,detail,state,record_source,record_completeness)
   VALUES($1,$2,$3,$4,$5,$6,1,$7::numeric,$8::date,$9,$10,NULL,$11::jsonb,'settled','historical_recovery','partial')
   ON CONFLICT(request_id,api_key_id) DO NOTHING`, c.RequestID, c.APIKeyID, *c.UsageRequestID, c.RequestFingerprint, *c.UserID, *c.SubscriptionID, *c.AmountUSD, *c.AccountingDate, *c.AdmittedAt, *c.SettledAt, string(detail))
		if e != nil {
			return out, e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return out, e
		}
		if n != 1 {
			return out, errors.New("concurrent receipt insertion; retry same manifest")
		}
		out.Inserted++
	}
	if err = tx.Commit(); err != nil {
		return UsageRecoveryApplyResult{}, err
	}
	return out, nil
}
func matchingRecoveryReceipt(ctx context.Context, tx *sql.Tx, c UsageRecoveryCandidate, sha string) (bool, error) {
	var matches bool
	err := tx.QueryRowContext(ctx, `SELECT state='settled' AND record_source='historical_recovery'
  AND record_completeness='partial' AND user_id=$3 AND subscription_id=$4 AND usage_request_id=$5
  AND charged_amount=$6::numeric AND accounting_date=$7::date AND admitted_at=$8 AND settled_at=$9
  AND request_fingerprint=$10 AND detail->>'recovery_manifest_sha256'=$11
 FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, c.RequestID, c.APIKeyID, *c.UserID, *c.SubscriptionID, *c.UsageRequestID, *c.AmountUSD, *c.AccountingDate, *c.AdmittedAt, *c.SettledAt, c.RequestFingerprint, sha).Scan(&matches)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !matches {
		return false, fmt.Errorf("existing receipt conflicts with approved evidence: %s", c.RequestID)
	}
	return true, nil
}
func verifyArrivedRecoveryDetail(ctx context.Context, tx *sql.Tx, c UsageRecoveryCandidate) error {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs WHERE request_id=$1 AND api_key_id=$2 AND user_id=$3 AND subscription_id=$4 AND actual_cost=$5::numeric`, *c.UsageRequestID, c.APIKeyID, *c.UserID, *c.SubscriptionID, *c.AmountUSD).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("late usage detail disagrees with approved evidence: %s", c.RequestID)
	}
	return nil
}
