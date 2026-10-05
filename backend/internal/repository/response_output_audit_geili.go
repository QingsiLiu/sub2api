package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type responseAuditRepository struct{ db *sql.DB }

func NewResponseAuditRepository(db *sql.DB) service.ResponseAuditRepository {
	return &responseAuditRepository{db: db}
}
func (r *responseAuditRepository) Save(ctx context.Context, a *service.ResponseAudit) error {
	p, err := service.MarshalResponseAudit(a)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO gateway_response_audits
 (audit_request_id,turn,user_id,api_key_id,account_id,model,endpoint,status,request_id,client_request_id,usage_request_id,started_at,finished_at,evidence)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb) ON CONFLICT(audit_request_id,turn) DO NOTHING`,
		a.AuditRequestID, a.Turn, a.UserID, a.APIKeyID, a.AccountID, a.Model, a.Endpoint, a.Status, a.RequestID, a.ClientRequestID, a.UsageRequestID, a.StartedAt, a.FinishedAt, string(p))
	return err
}

const responseAuditReceiptJoin = ` LEFT JOIN usage_settlement_receipts r ON r.api_key_id=a.api_key_id AND r.request_id=a.usage_request_id `
const responseAuditSelect = `a.evidence || jsonb_build_object('id',a.id,'settlement_state',
 CASE WHEN a.usage_request_id='' THEN 'unlinked' WHEN r.id IS NULL THEN 'not_found' ELSE r.state END,
 'receipt_id',r.id,'charged_amount',CASE WHEN r.state='settled' THEN r.charged_amount::text ELSE NULL END)`

func responseAuditWhere(f service.ResponseAuditFilter) (string, []any) {
	args := []any{f.From, f.To}
	where := []string{"a.finished_at >= $1", "a.finished_at < $2"}
	add := func(col string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf("a.%s=$%d", col, len(args)))
	}
	if f.Status != "" {
		add("status", f.Status)
	}
	if f.Model != "" {
		add("model", f.Model)
	}
	if f.Endpoint != "" {
		add("endpoint", f.Endpoint)
	}
	if f.UserID > 0 {
		add("user_id", f.UserID)
	}
	if f.APIKeyID > 0 {
		add("api_key_id", f.APIKeyID)
	}
	if f.AccountID > 0 {
		add("account_id", f.AccountID)
	}
	if f.RequestID != "" {
		add("request_id", f.RequestID)
	}
	return " WHERE " + strings.Join(where, " AND "), args
}
func (r *responseAuditRepository) List(ctx context.Context, f service.ResponseAuditFilter) ([]service.ResponseAudit, int64, error) {
	w, args := responseAuditWhere(f)
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM gateway_response_audits a"+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	q := fmt.Sprintf("SELECT %s FROM gateway_response_audits a %s %s ORDER BY a.finished_at DESC,a.id DESC LIMIT $%d OFFSET $%d", responseAuditSelect, responseAuditReceiptJoin, w, len(args)-1, len(args))
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []service.ResponseAudit{}
	for rows.Next() {
		var raw []byte
		var a service.ResponseAudit
		if err = rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		if err = json.Unmarshal(raw, &a); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}
func (r *responseAuditRepository) Get(ctx context.Context, id int64) (*service.ResponseAudit, error) {
	var p []byte
	if err := r.db.QueryRowContext(ctx, "SELECT "+responseAuditSelect+" FROM gateway_response_audits a"+responseAuditReceiptJoin+" WHERE a.id=$1", id).Scan(&p); err != nil {
		return nil, err
	}
	var a service.ResponseAudit
	err := json.Unmarshal(p, &a)
	return &a, err
}
func (r *responseAuditRepository) Stats(ctx context.Context, f service.ResponseAuditFilter) (*service.ResponseAuditStats, error) {
	w, args := responseAuditWhere(f)
	s := &service.ResponseAuditStats{Counts: map[string]int64{"success": 0, "partial_failure": 0, "empty": 0, "failed": 0, "unknown": 0}}
	q := `SELECT COUNT(*),COUNT(*) FILTER(WHERE NOT COALESCE((a.evidence->>'terminal_written')::boolean,false)),
 COUNT(*) FILTER(WHERE COALESCE((a.evidence->>'write_failed')::boolean,false)),
 COUNT(DISTINCT r.id) FILTER(WHERE a.status IN ('empty','failed') AND r.state='settled' AND r.charged_amount>0),
 COUNT(*) FILTER(WHERE a.status='success'),COUNT(*) FILTER(WHERE a.status='partial_failure'),
 COUNT(*) FILTER(WHERE a.status='empty'),COUNT(*) FILTER(WHERE a.status='failed'),COUNT(*) FILTER(WHERE a.status='unknown'),
 (SELECT MIN(started_at) FROM gateway_response_audits)
 FROM gateway_response_audits a` + responseAuditReceiptJoin + w
	var success, partial, empty, failed, unknown int64
	if err := r.db.QueryRowContext(ctx, q, args...).Scan(&s.Total, &s.MissingTerminal, &s.WriteFailed, &s.EmptyChargedReceipts, &success, &partial, &empty, &failed, &unknown, &s.ObservationStartedAt); err != nil {
		return nil, err
	}
	s.Counts["success"] = success
	s.Counts["partial_failure"] = partial
	s.Counts["empty"] = empty
	s.Counts["failed"] = failed
	s.Counts["unknown"] = unknown
	return s, nil
}
func (r *responseAuditRepository) Lookup(ctx context.Context, keys []service.ResponseAuditUsageKey) (map[service.ResponseAuditUsageKey]*service.ResponseAudit, error) {
	out := make(map[service.ResponseAuditUsageKey]*service.ResponseAudit)
	if len(keys) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(keys)*2)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		args = append(args, k.APIKeyID, k.RequestID)
		pairs = append(pairs, fmt.Sprintf("($%d::bigint,$%d::text)", len(args)-1, len(args)))
	}
	// Only a unique association may label a financial row. Reused client IDs
	// can correspond to several physical requests with different outcomes.
	q := `WITH keys(key_id,request_id) AS (VALUES ` + strings.Join(pairs, ",") + `),
 matches AS (
 SELECT k.key_id,k.request_id,a.id FROM keys k JOIN gateway_response_audits a ON a.api_key_id=k.key_id AND a.usage_request_id=k.request_id
 UNION
 SELECT k.key_id,k.request_id,a.id FROM keys k JOIN usage_settlement_receipts r ON r.api_key_id=k.key_id AND r.usage_request_id=k.request_id
 JOIN gateway_response_audits a ON a.api_key_id=r.api_key_id AND a.usage_request_id=r.request_id
 ), unique_matches AS (SELECT key_id,request_id,MIN(id) AS id FROM matches GROUP BY key_id,request_id HAVING COUNT(*)=1)
 SELECT m.key_id,m.request_id,a.evidence||jsonb_build_object('id',a.id) FROM unique_matches m JOIN gateway_response_audits a ON a.id=m.id`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k service.ResponseAuditUsageKey
		var p []byte
		if err = rows.Scan(&k.APIKeyID, &k.RequestID, &p); err != nil {
			return nil, err
		}
		var a service.ResponseAudit
		if err = json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		out[k] = &a
	}
	return out, rows.Err()
}
func (r *responseAuditRepository) Cleanup(ctx context.Context, before time.Time) error {
	for i := 0; i < 100; i++ {
		if ctx.Err() != nil {
			return nil
		}
		res, err := r.db.ExecContext(ctx, `DELETE FROM gateway_response_audits WHERE id IN (SELECT id FROM gateway_response_audits WHERE finished_at<$1 ORDER BY finished_at LIMIT 1000)`, before)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	}
	return nil
}
