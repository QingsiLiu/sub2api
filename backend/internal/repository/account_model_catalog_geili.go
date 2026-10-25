package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type accountModelCatalogContextKeyGeili struct{}

// The directory needs model declarations, not usable upstream credentials or
// account/proxy/group display projections. oauth_type preserves Google One's
// default model mapping; other defaults depend only on platform and type.
const accountModelCatalogSelectGeili = `SELECT a.id,a.platform,a.type,
CASE WHEN a.credentials IS NULL OR a.credentials='null'::jsonb THEN NULL
WHEN jsonb_typeof(a.credentials)<>'object' THEN 'false'::jsonb
ELSE jsonb_build_object('model_mapping',a.credentials->'model_mapping','oauth_type',a.credentials->'oauth_type') END
FROM accounts a`

// ListModelCatalogCandidatesGeili preserves the configured candidate pool and
// ignores transient scheduling state, just like ListModelAvailabilityCandidates.
// It executes one narrow read and never hydrates account associations.
func (r *accountRepository) ListModelCatalogCandidatesGeili(ctx context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]service.Account, error) {
	if len(platforms) == 0 {
		return []service.Account{}, nil
	}
	if r.sql == nil {
		return nil, fmt.Errorf("model catalog SQL executor is unavailable")
	}
	query := accountModelCatalogSelectGeili
	args := []any{pq.Array(platforms)}
	if groupID != nil {
		query += ` JOIN account_groups ag ON ag.account_id=a.id AND ag.group_id=$2`
		args = append(args, *groupID)
	}
	query += ` WHERE a.deleted_at IS NULL AND a.status='active' AND a.schedulable=TRUE AND a.platform=ANY($1)`
	if groupID == nil && !includeGrouped {
		query += ` AND NOT EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id)`
	}
	if groupID != nil {
		query += ` ORDER BY ag.priority,a.priority`
	} else {
		query += ` ORDER BY a.priority`
	}
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := []service.Account{}
	for rows.Next() {
		var account service.Account
		var credentials []byte
		if err := rows.Scan(&account.ID, &account.Platform, &account.Type, &credentials); err != nil {
			return nil, err
		}
		if len(credentials) > 0 {
			if err := json.Unmarshal(credentials, &account.Credentials); err != nil {
				return nil, fmt.Errorf("decode model catalog declarations: %w", err)
			}
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func isAccountModelCatalogContextGeili(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	catalog, _ := ctx.Value(accountModelCatalogContextKeyGeili{}).(bool)
	return catalog
}
