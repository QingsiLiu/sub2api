package repository

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const openAIEncryptedRoutePrefixGeili = "openai_encrypted_route_geili:v2:"
const encryptedRouteCacheTTLGeili = 24 * time.Hour
const encryptedRouteRedisBudgetGeili = 10 * time.Millisecond

func encryptedRouteKeyGeili(userID int64, digest string) string {
	return openAIEncryptedRoutePrefixGeili + strconv.FormatInt(userID, 10) + ":" + digest
}
func validEncryptedDigestGeili(v string) bool {
	if len(v) != 64 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil && v == strings.ToLower(v)
}

func validEncryptedSourceHintGeili(raw string, digest string) bool {
	var r service.OpenAIEncryptedRouteGeili
	return json.Unmarshal([]byte(raw), &r) == nil && r.Digest == digest && r.AccountID > 0 && r.GroupID > 0 && validEncryptedDigestGeili(r.BackendFingerprint) && strings.TrimSpace(r.Model) != ""
}

// Presence only: this bounded Redis read can select a verification budget but
// never returns an origin, checks authorization, or replaces authoritative SQL.
func (c *gatewayCache) HasOpenAIEncryptedSourceHintGeili(ctx context.Context, userID int64, digests []string) (bool, error) {
	if userID <= 0 {
		return false, fmt.Errorf("invalid encrypted source user")
	}
	requested, keys := []string{}, []string{}
	seen := map[string]bool{}
	for _, digest := range digests {
		if !validEncryptedDigestGeili(digest) {
			return false, fmt.Errorf("invalid encrypted digest")
		}
		if !seen[digest] {
			seen[digest] = true
			requested = append(requested, digest)
			keys = append(keys, encryptedRouteKeyGeili(userID, digest))
		}
	}
	if len(keys) > 256 {
		return false, fmt.Errorf("too many encrypted sources")
	}
	if len(keys) == 0 || c.encryptedRouteRDB == nil {
		return false, nil
	}
	redisCtx, cancel := context.WithTimeout(ctx, encryptedRouteRedisBudgetGeili)
	defer cancel()
	values, err := c.encryptedRouteRDB.MGet(redisCtx, keys...).Result()
	if err != nil {
		return false, encryptedRouteLookupFailureGeili("hint", false, err)
	}
	for i, value := range values {
		if raw, ok := value.(string); ok && validEncryptedSourceHintGeili(raw, requested[i]) {
			return true, nil
		}
	}
	return false, nil
}

func (c *gatewayCache) GetOpenAIEncryptedRoutesGeili(ctx context.Context, userID int64, digests []string) (result map[string]service.OpenAIEncryptedRouteGeili, err error) {
	result = map[string]service.OpenAIEncryptedRouteGeili{}
	if userID <= 0 {
		return result, fmt.Errorf("invalid encrypted source user")
	}
	unique := map[string]bool{}
	keys := []string{}
	requested := []string{}
	for _, d := range digests {
		if !validEncryptedDigestGeili(d) {
			return result, fmt.Errorf("invalid encrypted digest")
		}
		if !unique[d] {
			unique[d] = true
			requested = append(requested, d)
			keys = append(keys, encryptedRouteKeyGeili(userID, d))
		}
	}
	if len(keys) == 0 {
		return result, nil
	}
	if len(keys) > 256 {
		return result, fmt.Errorf("too many encrypted sources")
	}
	if c.encryptedRouteRDB != nil {
		redisCtx, cancel := context.WithTimeout(ctx, encryptedRouteRedisBudgetGeili)
		values, err := c.encryptedRouteRDB.MGet(redisCtx, keys...).Result()
		cancel()
		if err == nil {
			for i, value := range values {
				raw, ok := value.(string)
				if !ok {
					continue
				}
				if validEncryptedSourceHintGeili(raw, requested[i]) {
					result[requested[i]] = service.OpenAIEncryptedRouteGeili{Digest: requested[i]}
				}
			}
		}
	}
	if c.encryptedRouteDB == nil {
		return nil, encryptedRouteLookupFailureGeili("query", len(result) > 0, fmt.Errorf("encrypted provenance persistence unavailable"))
	}
	// Preserve the Redis presence hint even when PostgreSQL admission is denied:
	// protected tracked sessions must fail closed, never use cached authority.
	permit, err := c.encryptedRouteGuard.acquireWithRetry(time.Now(), service.ConsumeOpenAIEncryptedMetadataRetryGeili(ctx))
	if err != nil {
		return nil, encryptedRouteLookupFailureGeili("query", len(result) > 0, err)
	}
	defer func() {
		if failure, ok := err.(*encryptedRouteLookupErrorGeili); ok {
			failure.admitted = true
		}
		finishErr := err
		if err != nil && ctx.Err() == context.Canceled {
			finishErr = context.Canceled
		}
		permit.finish(finishErr)
	}()
	// Redis is only an access-refresh hint. Every authority comes from current
	// durable metadata, including conflicts committed before a failed cache write.
	// One SQL round trip touches misses daily and reads hits without extending
	// their access timestamp or fixed Redis TTL. Deleted/expired rows disappear.
	cached := result
	result = map[string]service.OpenAIEncryptedRouteGeili{}
	misses, hits := []string{}, []string{}
	for _, d := range requested {
		if _, ok := cached[d]; ok {
			hits = append(hits, d)
		} else {
			misses = append(misses, d)
		}
	}
	args := []any{userID, pq.Array(misses), pq.Array(hits)}
	q := `WITH touched AS (UPDATE openai_encrypted_sources_geili SET last_accessed_at=NOW() WHERE user_id=$1 AND last_accessed_at>NOW()-INTERVAL '180 days' AND cipher_digest=ANY($2::CHAR(64)[]) RETURNING cipher_digest,group_id,account_id,model,backend_fingerprint,conflict,generated_at) SELECT cipher_digest,group_id,account_id,model,backend_fingerprint,conflict,generated_at FROM touched UNION ALL SELECT cipher_digest,group_id,account_id,model,backend_fingerprint,conflict,generated_at FROM openai_encrypted_sources_geili WHERE user_id=$1 AND last_accessed_at>NOW()-INTERVAL '180 days' AND cipher_digest=ANY($3::CHAR(64)[])`
	if len(hits) == 0 {
		q = `UPDATE openai_encrypted_sources_geili SET last_accessed_at=NOW() WHERE user_id=$1 AND last_accessed_at>NOW()-INTERVAL '180 days' AND cipher_digest=ANY($2::CHAR(64)[]) RETURNING cipher_digest,group_id,account_id,model,backend_fingerprint,conflict,generated_at`
		args = []any{userID, pq.Array(misses)}
	} else if len(misses) == 0 {
		q = `SELECT cipher_digest,group_id,account_id,model,backend_fingerprint,conflict,generated_at FROM openai_encrypted_sources_geili WHERE user_id=$1 AND last_accessed_at>NOW()-INTERVAL '180 days' AND cipher_digest=ANY($2::CHAR(64)[])`
		args = []any{userID, pq.Array(hits)}
	}
	rows, err := c.encryptedRouteDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, encryptedRouteLookupFailureGeili("query", len(cached) > 0, err)
	}
	defer func() { _ = rows.Close() }()
	// Every result uses durable metadata; only misses refill the fixed-TTL cache.
	sqlRecords := make([]service.OpenAIEncryptedRouteGeili, 0)
	for rows.Next() {
		var r service.OpenAIEncryptedRouteGeili
		if err := rows.Scan(&r.Digest, &r.GroupID, &r.AccountID, &r.Model, &r.BackendFingerprint, &r.Conflict, &r.GeneratedAt); err != nil {
			return nil, encryptedRouteLookupFailureGeili("rows", len(cached) > 0, err)
		}
		result[r.Digest] = r
		if _, hit := cached[r.Digest]; !hit {
			sqlRecords = append(sqlRecords, r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, encryptedRouteLookupFailureGeili("rows", len(cached) > 0, err)
	}
	if err := rows.Close(); err != nil {
		return nil, encryptedRouteLookupFailureGeili("close", len(cached) > 0, err)
	}
	if c.encryptedRouteRDB != nil && len(sqlRecords) > 0 {
		redisCtx, cancel := context.WithTimeout(ctx, encryptedRouteRedisBudgetGeili)
		pipe := c.encryptedRouteRDB.Pipeline()
		for _, r := range sqlRecords {
			raw, _ := json.Marshal(r)
			pipe.Eval(redisCtx, encryptedRouteCacheWriteGeili, []string{encryptedRouteKeyGeili(userID, r.Digest)}, string(raw), int(encryptedRouteCacheTTLGeili.Seconds()))
		}
		_, _ = pipe.Exec(redisCtx)
		cancel()
	}
	return result, nil
}

// A cached presence hint can require safe verification failure handling, but
// carries no route or authorization. All successful authorities still use SQL.
type encryptedRouteLookupErrorGeili struct {
	phase     string
	knownHint bool
	cause     error
	admitted  bool
	retried   atomic.Bool
}

func (e *encryptedRouteLookupErrorGeili) ClaimOpenAIEncryptedMetadataRetryGeili() bool {
	return e.admitted && e.retried.CompareAndSwap(false, true)
}

func (e *encryptedRouteLookupErrorGeili) Error() string {
	return "encrypted source verification failed at " + e.phase
}
func (e *encryptedRouteLookupErrorGeili) Unwrap() error { return e.cause }
func (e *encryptedRouteLookupErrorGeili) KnownOpenAIEncryptedSourceHintGeili() bool {
	return e.knownHint
}
func (e *encryptedRouteLookupErrorGeili) EncryptedRouteLookupPhaseGeili() string { return e.phase }
func encryptedRouteLookupFailureGeili(phase string, known bool, cause error) error {
	return &encryptedRouteLookupErrorGeili{phase: phase, knownHint: known, cause: cause}
}

// Controlled diagnostics never expose SQL text, credentials or ciphertext.
type encryptedRouteStorageErrorGeili struct {
	phase string
	cause error
}

func (e *encryptedRouteStorageErrorGeili) Error() string {
	return "encrypted source storage failed at " + e.phase
}
func (e *encryptedRouteStorageErrorGeili) Unwrap() error                           { return e.cause }
func (e *encryptedRouteStorageErrorGeili) EncryptedRouteStoragePhaseGeili() string { return e.phase }
func encryptedRouteStorageFailureGeili(phase string, cause error) error {
	return &encryptedRouteStorageErrorGeili{phase: phase, cause: cause}
}

func (c *gatewayCache) SetOpenAIEncryptedRoutesGeili(ctx context.Context, userID int64, records []service.OpenAIEncryptedRouteGeili) (err error) {
	if userID <= 0 {
		return encryptedRouteStorageFailureGeili("validate_user", fmt.Errorf("invalid encrypted source user"))
	}
	if len(records) > 256 {
		return encryptedRouteStorageFailureGeili("validate_batch", fmt.Errorf("too many encrypted sources"))
	}
	for _, r := range records {
		phase := ""
		switch {
		case !validEncryptedDigestGeili(r.Digest):
			phase = "validate_digest"
		case r.GroupID <= 0:
			phase = "validate_group"
		case r.AccountID <= 0:
			phase = "validate_account"
		case !validEncryptedDigestGeili(r.BackendFingerprint):
			phase = "validate_fingerprint"
		case r.Model == "":
			phase = "validate_model"
		}
		if phase != "" {
			return encryptedRouteStorageFailureGeili(phase, fmt.Errorf("invalid encrypted source metadata"))
		}
	}

	if len(records) == 0 {
		return nil
	}
	if c.encryptedRouteDB == nil {
		return encryptedRouteStorageFailureGeili("store_unavailable", fmt.Errorf("encrypted provenance persistence unavailable"))
	}
	permit, err := c.encryptedRouteGuard.acquire(time.Now())
	if err != nil {
		return encryptedRouteStorageFailureGeili("begin", err)
	}
	// Hold admission until rollback/commit has released the SQL transaction.
	// Redis refill and opportunistic pruning do not occupy this permit.
	defer func() {
		finishErr := err
		if err != nil && ctx.Err() == context.Canceled {
			finishErr = context.Canceled
		}
		permit.finish(finishErr)
	}()
	records = append([]service.OpenAIEncryptedRouteGeili(nil), records...)
	sort.Slice(records, func(i, j int) bool { return records[i].Digest < records[j].Digest })
	tx, err := c.encryptedRouteDB.BeginTx(ctx, nil)
	if err != nil {
		return encryptedRouteStorageFailureGeili("begin", err)
	}
	defer func() { _ = tx.Rollback() }()
	stored := make([]service.OpenAIEncryptedRouteGeili, 0, len(records))
	for _, r := range records {
		if r.GeneratedAt.IsZero() {
			r.GeneratedAt = time.Now().UTC()
		}
		q := `INSERT INTO openai_encrypted_sources_geili(user_id,cipher_digest,group_id,account_id,model,backend_fingerprint,conflict,generated_at,last_accessed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NOW()) ON CONFLICT(user_id,cipher_digest) DO UPDATE SET last_accessed_at=NOW(),conflict=openai_encrypted_sources_geili.conflict OR EXCLUDED.conflict OR openai_encrypted_sources_geili.account_id<>EXCLUDED.account_id OR openai_encrypted_sources_geili.model<>EXCLUDED.model OR openai_encrypted_sources_geili.backend_fingerprint<>EXCLUDED.backend_fingerprint RETURNING cipher_digest,group_id,account_id,model,backend_fingerprint,conflict,generated_at`
		var actual service.OpenAIEncryptedRouteGeili
		err = tx.QueryRowContext(ctx, q, userID, r.Digest, r.GroupID, r.AccountID, r.Model, r.BackendFingerprint, r.Conflict, r.GeneratedAt).Scan(&actual.Digest, &actual.GroupID, &actual.AccountID, &actual.Model, &actual.BackendFingerprint, &actual.Conflict, &actual.GeneratedAt)
		if err != nil {
			return encryptedRouteStorageFailureGeili("insert", err)
		}
		stored = append(stored, actual)
	}
	if err = tx.Commit(); err != nil {
		return encryptedRouteStorageFailureGeili("commit", err)
	}
	permit.finish(nil)
	permit = encryptedRoutePermitGeili{}
	if c.encryptedRouteRDB != nil {
		redisCtx, cancel := context.WithTimeout(ctx, encryptedRouteRedisBudgetGeili)
		pipe := c.encryptedRouteRDB.Pipeline()
		for _, r := range stored {
			raw, _ := json.Marshal(r)
			pipe.Eval(redisCtx, encryptedRouteCacheWriteGeili, []string{encryptedRouteKeyGeili(userID, r.Digest)}, string(raw), int(encryptedRouteCacheTTLGeili.Seconds()))
		}
		_, _ = pipe.Exec(redisCtx)
		cancel()
	}
	// Bounded periodic pruning; never touches usage or settlement tables.
	now := time.Now().Unix()
	prev := c.encryptedRouteLastPrune.Load()
	if now-prev >= 3600 && c.encryptedRouteLastPrune.CompareAndSwap(prev, now) {
		if prunePermit, acquireErr := c.encryptedRouteGuard.acquire(time.Now()); acquireErr == nil {
			_, pruneErr := c.encryptedRouteDB.ExecContext(ctx, `DELETE FROM openai_encrypted_sources_geili WHERE (user_id,cipher_digest) IN (SELECT user_id,cipher_digest FROM openai_encrypted_sources_geili WHERE last_accessed_at<NOW()-INTERVAL '180 days' LIMIT 1000)`)
			prunePermit.finish(pruneErr)
		}
	}
	return nil
}

// A late cache writer must never overwrite a conflict discovered by another instance.
const encryptedRouteCacheWriteGeili = `local old=redis.call("GET",KEYS[1]); local incoming=cjson.decode(ARGV[1]); if old then local ok,v=pcall(cjson.decode,old); if ok and v.conflict then incoming.conflict=true end end; return redis.call("SET",KEYS[1],cjson.encode(incoming),"EX",ARGV[2])`
