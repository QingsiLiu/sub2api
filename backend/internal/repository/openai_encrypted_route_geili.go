package repository

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const openAIEncryptedRoutePrefixGeili = "openai_encrypted_route_geili:v2:"
const encryptedRouteCacheTTLGeili = 24 * time.Hour

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

func (c *gatewayCache) GetOpenAIEncryptedRoutesGeili(ctx context.Context, userID int64, digests []string) (map[string]service.OpenAIEncryptedRouteGeili, error) {
	result := map[string]service.OpenAIEncryptedRouteGeili{}
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
	if c.rdb != nil {
		values, err := c.rdb.MGet(ctx, keys...).Result()
		if err == nil {
			for i, value := range values {
				raw, ok := value.(string)
				if !ok {
					continue
				}
				var r service.OpenAIEncryptedRouteGeili
				if json.Unmarshal([]byte(raw), &r) == nil && r.Digest == requested[i] && r.AccountID > 0 && r.GroupID > 0 {
					result[r.Digest] = r
				}
			}
		}
	}
	if len(result) == len(requested) {
		return result, nil
	}
	if c.encryptedRouteDB == nil {
		return result, nil
	}
	// Redis entries have a fixed 24-hour TTL; cache misses refresh durable access.
	// Thus active sessions touch SQL daily without a database write on every turn.
	args := []any{userID}
	slots := []string{}
	for _, d := range requested {
		if _, ok := result[d]; ok {
			continue
		}
		args = append(args, d)
		slots = append(slots, fmt.Sprintf("$%d", len(args)))
	}
	q := `UPDATE openai_encrypted_sources_geili SET last_accessed_at=NOW() WHERE user_id=$1 AND last_accessed_at>NOW()-INTERVAL '180 days' AND cipher_digest IN (` + strings.Join(slots, ",") + `) RETURNING cipher_digest,group_id,account_id,model,backend_fingerprint,conflict,generated_at`
	rows, err := c.encryptedRouteDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Only missed digests query durable metadata; cache writes monotonically retain conflicts.
	sqlRecords := make([]service.OpenAIEncryptedRouteGeili, 0)
	for rows.Next() {
		var r service.OpenAIEncryptedRouteGeili
		if err := rows.Scan(&r.Digest, &r.GroupID, &r.AccountID, &r.Model, &r.BackendFingerprint, &r.Conflict, &r.GeneratedAt); err != nil {
			return nil, err
		}
		result[r.Digest] = r
		sqlRecords = append(sqlRecords, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if c.rdb != nil {
		pipe := c.rdb.Pipeline()
		for _, r := range sqlRecords {
			raw, _ := json.Marshal(r)
			pipe.Eval(ctx, encryptedRouteCacheWriteGeili, []string{encryptedRouteKeyGeili(userID, r.Digest)}, string(raw), int(encryptedRouteCacheTTLGeili.Seconds()))
		}
		_, _ = pipe.Exec(ctx)
	}
	return result, nil
}

func (c *gatewayCache) SetOpenAIEncryptedRoutesGeili(ctx context.Context, userID int64, records []service.OpenAIEncryptedRouteGeili) error {
	if userID <= 0 {
		return fmt.Errorf("invalid encrypted source user")
	}
	if len(records) > 256 {
		return fmt.Errorf("too many encrypted sources")
	}
	for _, r := range records {
		if !validEncryptedDigestGeili(r.Digest) || r.AccountID <= 0 || r.GroupID <= 0 || !validEncryptedDigestGeili(r.BackendFingerprint) || r.Model == "" {
			return fmt.Errorf("invalid encrypted source metadata")
		}
	}
	if len(records) == 0 {
		return nil
	}
	if c.encryptedRouteDB == nil {
		return fmt.Errorf("encrypted provenance persistence unavailable")
	}
	records = append([]service.OpenAIEncryptedRouteGeili(nil), records...)
	sort.Slice(records, func(i, j int) bool { return records[i].Digest < records[j].Digest })
	tx, err := c.encryptedRouteDB.BeginTx(ctx, nil)
	if err != nil {
		return err
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
			return err
		}
		stored = append(stored, actual)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if c.rdb != nil {
		pipe := c.rdb.Pipeline()
		for _, r := range stored {
			raw, _ := json.Marshal(r)
			pipe.Eval(ctx, encryptedRouteCacheWriteGeili, []string{encryptedRouteKeyGeili(userID, r.Digest)}, string(raw), int(encryptedRouteCacheTTLGeili.Seconds()))
		}
		_, _ = pipe.Exec(ctx)
	}
	// Bounded periodic pruning; never touches usage or settlement tables.
	now := time.Now().Unix()
	prev := c.encryptedRouteLastPrune.Load()
	if now-prev >= 3600 && c.encryptedRouteLastPrune.CompareAndSwap(prev, now) {
		_, _ = c.encryptedRouteDB.ExecContext(ctx, `DELETE FROM openai_encrypted_sources_geili WHERE (user_id,cipher_digest) IN (SELECT user_id,cipher_digest FROM openai_encrypted_sources_geili WHERE last_accessed_at<NOW()-INTERVAL '180 days' LIMIT 1000)`)
	}
	return nil
}

// A late cache writer must never overwrite a conflict discovered by another instance.
const encryptedRouteCacheWriteGeili = `local old=redis.call("GET",KEYS[1]); local incoming=cjson.decode(ARGV[1]); if old then local ok,v=pcall(cjson.decode,old); if ok and v.conflict then incoming.conflict=true end end; return redis.call("SET",KEYS[1],cjson.encode(incoming),"EX",ARGV[2])`
