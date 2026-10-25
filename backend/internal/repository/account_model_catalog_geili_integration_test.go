//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountModelCatalogGeiliNarrowProjectionAndCandidateScope(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	// Transaction-local fixtures cannot touch the shared migrated test tables.
	_, err = tx.ExecContext(ctx, `CREATE TEMP TABLE accounts (id bigint,platform text,type text,credentials jsonb,status text DEFAULT 'active',schedulable boolean DEFAULT true,deleted_at timestamptz,priority int DEFAULT 0) ON COMMIT DROP;
CREATE TEMP TABLE account_groups (account_id bigint,group_id bigint,priority int DEFAULT 0) ON COMMIT DROP;
INSERT INTO accounts(id,platform,type,credentials) VALUES
(1,'anthropic','apikey','{"api_key":"must-not-return","model_mapping":{"custom":"upstream","invalid":123}}'),
(2,'anthropic','oauth','{}'),
(3,'gemini','oauth','{"oauth_type":"google_one","refresh_token":"must-not-return"}'),
(4,'antigravity','oauth',NULL),
(5,'grok','oauth','null'),
(6,'anthropic','apikey','{"model_mapping":["invalid"]}'),
(7,'anthropic','apikey','{}'),(8,'anthropic','apikey','{}'),(9,'anthropic','apikey','{}'),(10,'anthropic','apikey','{}');
UPDATE accounts SET credentials=credentials || jsonb_build_object('access_token',repeat('x',1048576)) WHERE id=1;
UPDATE accounts SET deleted_at=now() WHERE id=7;
UPDATE accounts SET status='error' WHERE id=8;
UPDATE accounts SET schedulable=false WHERE id=9;
INSERT INTO account_groups(account_id,group_id,priority) VALUES (1,42,10),(2,42,0),(3,42,0),(7,42,0),(8,42,0),(9,42,0),(10,99,0)`)
	require.NoError(t, err)
	repo := &accountRepository{sql: tx}
	group := int64(42)
	grouped, err := repo.ListModelCatalogCandidatesGeili(ctx, &group, []string{"anthropic"}, false)
	require.NoError(t, err)
	require.Equal(t, []int64{2, 1}, catalogAccountIDsGeili(grouped), "group and account priorities remain ordered; deleted/disabled/unschedulable/other platforms and groups stay excluded")
	for _, a := range grouped {
		require.Nil(t, a.Extra)
		require.Nil(t, a.Proxy)
		require.Nil(t, a.Groups)
		require.Nil(t, a.GroupIDs)
		require.Nil(t, a.RuntimeState)
		data, err := json.Marshal(a.Credentials)
		require.NoError(t, err)
		require.Less(t, len(data), 150, "large upstream credentials never cross the directory read")
		require.NotContains(t, string(data), "must-not-return")
		require.NotContains(t, string(data), "access_token")
	}
	ungrouped, err := repo.ListModelCatalogCandidatesGeili(ctx, nil, []string{"anthropic"}, false)
	require.NoError(t, err)
	require.Equal(t, []int64{6}, catalogAccountIDsGeili(ungrouped))
	all, err := repo.ListModelCatalogCandidatesGeili(ctx, nil, []string{"anthropic", "gemini", "antigravity", "grok"}, true)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{1, 2, 3, 4, 5, 6, 10}, catalogAccountIDsGeili(all))
	// Compare actual SQL JSON projection with the full credentials decoder and
	// every platform fallback used by GetModelMapping, including malformed maps.
	for _, narrow := range all {
		var credentials []byte
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT credentials FROM accounts WHERE id=$1`, narrow.ID).Scan(&credentials))
		full := service.Account{Platform: narrow.Platform, Type: narrow.Type}
		if credentials != nil {
			require.NoError(t, json.Unmarshal(credentials, &full.Credentials))
		}
		require.Equal(t, full.GetModelMapping(), narrow.GetModelMapping(), "account %d defaults and mapping declarations must match", narrow.ID)
	}
	// Preserve the full decoder's fail-closed behavior for a corrupt outer
	// credentials value without selecting that potentially sensitive scalar.
	_, err = tx.ExecContext(ctx, `INSERT INTO accounts(id,platform,type,credentials) VALUES (20,'malformed','apikey','"must-not-return"')`)
	require.NoError(t, err)
	_, err = repo.ListModelCatalogCandidatesGeili(ctx, nil, []string{"malformed"}, true)
	require.ErrorContains(t, err, "decode model catalog declarations")
	require.NotContains(t, err.Error(), "must-not-return")
}

func catalogAccountIDsGeili(accounts []service.Account) []int64 {
	ids := make([]int64, len(accounts))
	for i := range accounts {
		ids[i] = accounts[i].ID
	}
	return ids
}
