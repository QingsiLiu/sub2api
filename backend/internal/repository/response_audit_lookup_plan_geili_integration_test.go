//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Use private temporary tables, not production data or planner switches. A
// realistic background population catches losing the partial-index predicate.
func TestResponseAuditLookupGeiliUsesPartialIndexForUsagePages(t *testing.T) {
	db, err := sql.Open("postgres", integrationEncryptedDSNGeili)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = db.ExecContext(ctx, `
CREATE TEMP TABLE gateway_response_audits (LIKE public.gateway_response_audits INCLUDING ALL);
INSERT INTO gateway_response_audits
(audit_request_id,turn,user_id,api_key_id,endpoint,status,usage_request_id,started_at,finished_at,evidence)
SELECT md5(g::text)::uuid,0,1,g%1000+1,'/v1/responses','success','request:'||g,NOW(),NOW(),'{"status":"success"}'::jsonb
FROM generate_series(1,100000) g;
ANALYZE gateway_response_audits;`)
	require.NoError(t, err)
	keys := make([]service.ResponseAuditUsageKey, 0, 100)
	for i := int64(1); i <= 100; i++ {
		keys = append(keys, service.ResponseAuditUsageKey{APIKeyID: i + 1, RequestID: "request:" + strconv.FormatInt(i, 10)})
	}
	query, args := responseAuditLookupQueryGeili(keys)
	type planNode struct {
		NodeType string            `json:"Node Type"`
		Relation string            `json:"Relation Name"`
		Index    string            `json:"Index Name"`
		Rows     float64           `json:"Plan Rows"`
		Plans    []json.RawMessage `json:"Plans"`
	}
	explain := func(q string) (scans, indexScans int) {
		var raw []byte
		require.NoError(t, db.QueryRowContext(ctx, "EXPLAIN (FORMAT JSON) "+q, args...).Scan(&raw))
		var doc []struct{ Plan json.RawMessage }
		require.NoError(t, json.Unmarshal(raw, &doc))
		var walk func(json.RawMessage)
		walk = func(raw json.RawMessage) {
			var n planNode
			require.NoError(t, json.Unmarshal(raw, &n))
			if n.Relation == "gateway_response_audits" {
				if n.NodeType == "Seq Scan" {
					scans++
				}
				if strings.Contains(n.NodeType, "Index") && strings.Contains(n.Index, "usage_request_id") {
					indexScans++
				}
			}
			for _, child := range n.Plans {
				walk(child)
			}
		}
		walk(doc[0].Plan)
		return
	}
	oldQuery := strings.ReplaceAll(query, " AND a.usage_request_id<>''", "")
	oldScans, _ := explain(oldQuery)
	require.GreaterOrEqual(t, oldScans, 2, "fixture must reproduce both historical full scans")
	scans, indexes := explain(query)
	require.Zero(t, scans)
	require.GreaterOrEqual(t, indexes, 2, "both correlation branches must use the partial index")
	start := time.Now()
	got, err := (&responseAuditRepository{db: db}).Lookup(ctx, keys)
	require.NoError(t, err)
	require.Len(t, got, 100)
	t.Logf("100-key lookup over 100000 background rows: %s; old audit full scans=%d, new=%d, partial index scans=%d", time.Since(start), oldScans, scans, indexes)
}
