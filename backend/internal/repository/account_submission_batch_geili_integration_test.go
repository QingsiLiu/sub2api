//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func batchInviteFixture(t *testing.T, platform string) (*accountSubmissionRepository, *service.AccountSubmissionInvite, string) {
	t.Helper()
	repo, ok := NewAccountSubmissionRepository(integrationDB).(*accountSubmissionRepository)
	require.True(t, ok)
	hash := fmt.Sprintf("%064x", time.Now().UnixNano())
	i, err := repo.CreateInvite(context.Background(), hash, 1, service.AccountSubmissionConfig{Name: "batch-" + hash[45:], Platform: platform, Concurrency: 3, Priority: 50, RateMultiplier: 0})
	require.NoError(t, err)
	t.Cleanup(func() {
		// The source rows intentionally retain credentials until the capability is removed.
		rows, e := integrationDB.Query(`SELECT account_id FROM account_submission_invites_geili WHERE id=$1 OR parent_invite_id=$1`, i.ID)
		if e != nil {
			return
		}
		var ids []int64
		for rows.Next() {
			var id *int64
			_ = rows.Scan(&id)
			if id != nil {
				ids = append(ids, *id)
			}
		}
		_ = rows.Close()
		proxies, e := integrationDB.Query(`SELECT c.proxy_id FROM account_submission_connections_geili c JOIN account_submission_invites_geili i ON i.id=c.invite_id WHERE i.id=$1 OR i.parent_invite_id=$1`, i.ID)
		if e != nil {
			return
		}
		var proxyIDs []int64
		for proxies.Next() {
			var id *int64
			_ = proxies.Scan(&id)
			if id != nil {
				proxyIDs = append(proxyIDs, *id)
			}
		}
		_ = proxies.Close()
		_, _ = integrationDB.Exec(`DELETE FROM account_submission_connections_geili WHERE invite_id IN (SELECT id FROM account_submission_invites_geili WHERE id=$1 OR parent_invite_id=$1)`, i.ID)
		_, _ = integrationDB.Exec(`DELETE FROM account_submission_invites_geili WHERE id=$1`, i.ID)
		_, _ = integrationDB.Exec(`DELETE FROM scheduler_outbox WHERE account_id=ANY($1)`, pq.Array(ids))
		_, _ = integrationDB.Exec(`DELETE FROM usage_logs WHERE account_id=ANY($1)`, pq.Array(ids))
		_, _ = integrationDB.Exec(`DELETE FROM accounts WHERE id=ANY($1)`, pq.Array(ids))
		_, _ = integrationDB.Exec(`DELETE FROM proxies WHERE id=ANY($1) AND NOT EXISTS(SELECT 1 FROM accounts WHERE proxy_id=proxies.id)`, pq.Array(proxyIDs))
	})
	return repo, i, hash
}

func TestAccountSubmissionBatchPostgresIndependentProxies(t *testing.T) {
	ctx := context.Background()
	preset, err := testEntClient(t).Proxy.Create().SetName(fmt.Sprintf("batch-preset-%d", time.Now().UnixNano())).SetProtocol("http").SetHost("9.9.9.9").SetPort(8081).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM proxies WHERE id=$1`, preset.ID) })
	repo, invite, hash := batchInviteFixture(t, service.PlatformAnthropic)
	_, err = integrationDB.Exec(`UPDATE account_submission_invites_geili SET config=jsonb_set(config,'{proxy_id}',to_jsonb($2::bigint)) WHERE id=$1`, invite.ID, preset.ID)
	require.NoError(t, err)
	connections := []service.SubmissionConnectionInput{
		{Mode: "direct"},
		{Mode: "custom", Proxy: &service.SubmissionProxyDraft{Name: "Proxy A", Protocol: "http", Host: "8.8.8.8", Port: 8080, Username: "holder-a", Password: "synthetic-password-a"}},
		{Mode: "custom", Proxy: &service.SubmissionProxyDraft{Name: "Proxy B", Protocol: "socks5", Host: "1.1.1.1", Port: 1080, Username: "holder-b", Password: "synthetic-password-b"}},
		{Mode: "preset"},
	}
	keys := []string{"synthetic-direct", "synthetic-proxy-a", "synthetic-proxy-b", "synthetic-preset"}
	require.NoError(t, repo.SubmitInviteBatch(ctx, hash, keys, service.SubmissionOptions{PerKeyConnections: connections}))
	rows, err := integrationDB.Query(`SELECT a.credentials->>'api_key',a.proxy_id,COALESCE(p.host,''),COALESCE(p.password,''),i.account_ref::text FROM account_submission_invites_geili i JOIN accounts a ON a.id=i.account_id LEFT JOIN proxies p ON p.id=a.proxy_id WHERE i.id=$1 OR i.parent_invite_id=$1 ORDER BY i.id`, invite.ID)
	require.NoError(t, err)
	var proxyIDs []*int64
	var refs []string
	hosts := []string{"", "8.8.8.8", "1.1.1.1", "9.9.9.9"}
	passwords := []string{"", "synthetic-password-a", "synthetic-password-b", ""}
	for rows.Next() {
		var key, host, password, ref string
		var proxyID *int64
		n := len(refs)
		require.NoError(t, rows.Scan(&key, &proxyID, &host, &password, &ref))
		require.Equal(t, keys[n], key)
		require.Equal(t, hosts[n], host)
		require.Equal(t, passwords[n], password)
		refs = append(refs, ref)
		proxyIDs = append(proxyIDs, proxyID)
	}
	require.NoError(t, rows.Err())
	_ = rows.Close()
	require.Len(t, refs, 4)
	require.Nil(t, proxyIDs[0])
	require.NotEqual(t, *proxyIDs[1], *proxyIDs[2])
	require.Equal(t, preset.ID, *proxyIDs[3])
	for n, ref := range refs {
		state, err := repo.ReadSubmissionMemberConnection(ctx, hash, ref)
		require.NoError(t, err)
		require.Equal(t, connections[n].Mode, state.Mode)
		if n == 1 || n == 2 {
			require.True(t, state.HasPassword)
			require.Empty(t, state.Proxy.Password)
			require.Equal(t, hosts[n], state.Proxy.Host)
		}
	}
	// Replaying different connection drafts cannot alter the committed batch.
	require.NoError(t, repo.SubmitInviteBatch(ctx, hash, keys, service.SubmissionOptions{PerKeyConnections: []service.SubmissionConnectionInput{{Mode: "direct"}, {Mode: "direct"}, {Mode: "direct"}, {Mode: "direct"}}}))
	state, err := repo.ReadSubmissionMemberConnection(ctx, hash, refs[2])
	require.NoError(t, err)
	require.Equal(t, "1.1.1.1", state.Proxy.Host)
	// Editing A afterwards only changes A; B's proxy and password remain independent.
	_, err = repo.UpdateSubmissionMemberConnection(ctx, hash, refs[1], 0, "11111111-1111-1111-1111-111111111111", service.SubmissionConnectionInput{Mode: "direct"})
	require.NoError(t, err)
	state, err = repo.ReadSubmissionMemberConnection(ctx, hash, refs[2])
	require.NoError(t, err)
	require.Equal(t, "1.1.1.1", state.Proxy.Host)
	require.True(t, state.HasPassword)
	var presetHost string
	require.NoError(t, integrationDB.QueryRow(`SELECT host FROM proxies WHERE id=$1`, preset.ID).Scan(&presetHost))
	require.Equal(t, "9.9.9.9", presetHost)
}

func TestAccountSubmissionBatchPostgresPerKeyProxyRollback(t *testing.T) {
	ctx := context.Background()
	repo, invite, hash := batchInviteFixture(t, service.PlatformOpenAI)
	_, err := integrationDB.Exec(`ALTER TABLE accounts ADD CONSTRAINT batch_per_key_rollback_fixture_geili CHECK(name <> ` + pq.QuoteLiteral(invite.Config.Name+" #2") + `) NOT VALID`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`ALTER TABLE accounts DROP CONSTRAINT IF EXISTS batch_per_key_rollback_fixture_geili`)
	})
	prefix := fmt.Sprintf("per-key-rollback-%d", time.Now().UnixNano())
	connections := []service.SubmissionConnectionInput{
		{Mode: "custom", Proxy: &service.SubmissionProxyDraft{Name: prefix + "-A", Protocol: "http", Host: "8.8.8.8", Port: 8080}},
		{Mode: "custom", Proxy: &service.SubmissionProxyDraft{Name: prefix + "-B", Protocol: "http", Host: "1.1.1.1", Port: 8080}},
	}
	err = repo.SubmitInviteBatch(ctx, hash, []string{"synthetic-a", "synthetic-b"}, service.SubmissionOptions{PerKeyConnections: connections})
	require.ErrorIs(t, err, service.ErrSubmissionUnavailable)
	current, err := repo.InspectInvite(ctx, hash)
	require.NoError(t, err)
	require.Nil(t, current.AccountID)
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM proxies WHERE name LIKE $1`, "[external] "+prefix+"%").Scan(&n))
	require.Zero(t, n)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM accounts WHERE name LIKE $1`, invite.Config.Name+" #%").Scan(&n))
	require.Zero(t, n)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM account_submission_invites_geili WHERE parent_invite_id=$1`, invite.ID).Scan(&n))
	require.Zero(t, n)
}

func TestAccountSubmissionBatchPostgresConcurrentMaximumAndProtection(t *testing.T) {
	ctx := context.Background()
	group, err := testEntClient(t).Group.Create().SetName(fmt.Sprintf("batch-group-%d", time.Now().UnixNano())).SetPlatform(service.PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM groups WHERE id=$1`, group.ID) })
	repo, invite, hash := batchInviteFixture(t, service.PlatformOpenAI)
	_, err = integrationDB.Exec(`UPDATE account_submission_invites_geili SET config=jsonb_set(config,'{group_ids}',$2::jsonb) WHERE id=$1`, invite.ID, fmt.Sprintf("[%d]", group.ID))
	require.NoError(t, err)
	keys := make([]string, service.MaxSubmissionBatchKeys)
	for n := range keys {
		keys[n] = fmt.Sprintf("synthetic-batch-%d", n)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for n := 0; n < 6; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- repo.SubmitInviteBatch(ctx, hash, keys, service.SubmissionOptions{}) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	rows, err := integrationDB.Query(`SELECT a.id,a.status,a.schedulable,a.concurrency,a.rate_multiplier,a.credentials->>'base_url',a.name FROM account_submission_invites_geili i JOIN accounts a ON a.id=i.account_id WHERE i.id=$1 OR i.parent_invite_id=$1 ORDER BY i.id`, invite.ID)
	require.NoError(t, err)
	var ids []int64
	for rows.Next() {
		var id int64
		var status, url, name string
		var enabled bool
		var concurrency int
		var rate float64
		require.NoError(t, rows.Scan(&id, &status, &enabled, &concurrency, &rate, &url, &name))
		ids = append(ids, id)
		require.Equal(t, "inactive", status)
		require.False(t, enabled)
		require.Equal(t, 3, concurrency)
		require.Zero(t, rate)
		require.Equal(t, "https://api.openai.com", url)
		require.Contains(t, name, fmt.Sprintf(" #%d", len(ids)))
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Len(t, ids, 100)
	var bindings int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM account_groups WHERE group_id=$1 AND account_id=ANY($2)`, group.ID, pq.Array(ids)).Scan(&bindings))
	require.Equal(t, 100, bindings)
	protected, err := repo.ProtectedAccountIDs(ctx, ids)
	require.NoError(t, err)
	require.Len(t, protected, 100)
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE account_id=ANY($1)`, pq.Array(ids)).Scan(&count))
	require.Equal(t, 100, count)
	_, err = integrationDB.Exec(`UPDATE accounts SET credentials=jsonb_set(credentials,'{api_key}','"replacement"') WHERE id=$1`, ids[99])
	require.Error(t, err)
	require.NoError(t, repo.SubmitInvite(ctx, hash, "replacement-must-not-save"))
	items, _, err := repo.ListInvites(ctx, 1, 100)
	require.NoError(t, err)
	for _, item := range items {
		if item.ID == invite.ID {
			require.Equal(t, 100, item.AccountCount)
		}
		if item.AccountID != nil && *item.AccountID != ids[0] {
			for _, id := range ids[1:] {
				require.NotEqual(t, id, *item.AccountID)
			}
		}
	}
}

func TestAccountSubmissionBatchPostgresUsageAndMemberCapability(t *testing.T) {
	ctx := context.Background()
	repo, invite, hash := batchInviteFixture(t, service.PlatformAnthropic)
	require.NoError(t, repo.SubmitInviteBatch(ctx, hash, []string{"synthetic-first", "synthetic-second", "synthetic-third"}, service.SubmissionOptions{}))
	from, to, err := service.SubmissionUsageDates("", "", time.Now())
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE account_submission_invites_geili SET submitted_at=$2 WHERE id=$1 OR parent_invite_id=$1`, invite.ID, from)
	require.NoError(t, err)
	result, err := repo.SubmissionUsage(ctx, hash, from, to)
	require.NoError(t, err)
	require.Len(t, result.Accounts, 3)
	client := testEntClient(t)
	u := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("batch-owner-%d@test.com", time.Now().UnixNano())})
	k := mustCreateApiKey(t, client, &service.APIKey{UserID: u.ID, Key: fmt.Sprintf("sk-batch-fixture-%d", time.Now().UnixNano()), Name: "private customer"})
	logs := newUsageLogRepositoryWithSQL(client, integrationDB)
	var ids []int64
	for n, account := range result.Accounts {
		var id int64
		require.NoError(t, integrationDB.QueryRow(`SELECT account_id FROM account_submission_invites_geili WHERE account_ref=$1`, account.AccountRef).Scan(&id))
		ids = append(ids, id)
		if n != 2 {
			_, err = logs.Create(ctx, &service.UsageLog{UserID: u.ID, APIKeyID: k.ID, AccountID: id, Model: "claude-synthetic", InputTokens: 10, OutputTokens: 20, CacheReadTokens: 30, TotalCost: float64(n+1) / 4, ActualCost: 999, CreatedAt: from.Add(time.Hour)})
			require.NoError(t, err)
		}
	}
	_, _, otherHash := batchInviteFixture(t, service.PlatformAnthropic)
	require.NoError(t, repo.SubmitInvite(ctx, otherHash, "synthetic-other"))
	_, err = repo.ReadSubmissionMemberConnection(ctx, otherHash, result.Accounts[1].AccountRef)
	require.ErrorIs(t, err, service.ErrSubmissionInactive)
	result, err = repo.SubmissionUsage(ctx, hash, from, to)
	require.NoError(t, err)
	require.Equal(t, int64(2), result.Summary.Requests)
	require.Equal(t, .75, result.Summary.ReferenceCost)
	require.Equal(t, .25, result.Accounts[0].Summary.ReferenceCost)
	require.Equal(t, .5, result.Accounts[1].Summary.ReferenceCost)
	require.Zero(t, result.Accounts[2].Summary.Requests)
	require.Len(t, result.Daily, 1)
	require.Len(t, result.Models, 1)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	for _, private := range []string{"synthetic-second", "private customer", "actual_cost", "account_id", "token_hash"} {
		require.NotContains(t, string(raw), private)
	}
	ref := result.Accounts[1].AccountRef
	state, err := repo.UpdateSubmissionMemberConnection(ctx, hash, ref, 0, "11111111-1111-1111-1111-111111111111", service.SubmissionConnectionInput{Mode: "direct"})
	require.NoError(t, err)
	require.Equal(t, int64(1), state.Version)
	first, err := repo.ReadSubmissionMemberConnection(ctx, hash, result.Accounts[0].AccountRef)
	require.NoError(t, err)
	require.Zero(t, first.Version)
	_, err = repo.UpdateSubmissionMemberConnection(ctx, hash, ref, 0, "22222222-2222-2222-2222-222222222222", service.SubmissionConnectionInput{Mode: "direct"})
	require.ErrorIs(t, err, service.ErrSubmissionConnectionConflict)
	require.NoError(t, repo.RevokeSubmissionView(ctx, invite.ID))
	_, err = repo.ReadSubmissionMemberConnection(ctx, hash, ref)
	require.ErrorIs(t, err, service.ErrSubmissionInactive)
	_, err = repo.UpdateSubmissionMemberConnection(ctx, hash, ref, 1, "22222222-2222-2222-2222-222222222222", service.SubmissionConnectionInput{Mode: "direct"})
	require.ErrorIs(t, err, service.ErrSubmissionInactive)
	newHash := fmt.Sprintf("%064x", time.Now().UnixNano())
	require.NoError(t, repo.ResetSubmissionView(ctx, invite.ID, newHash))
	_, err = repo.ReadSubmissionMemberConnection(ctx, hash, ref)
	require.ErrorIs(t, err, service.ErrSubmissionInactive)
	_, err = repo.ReadSubmissionMemberConnection(ctx, newHash, ref)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE accounts SET deleted_at=now() WHERE id=$1`, ids[0])
	require.NoError(t, err)
	result, err = repo.SubmissionUsage(ctx, newHash, from, to)
	require.NoError(t, err)
	require.Len(t, result.Accounts, 2)
	require.Equal(t, .5, result.Summary.ReferenceCost)
	require.NoError(t, repo.ResetSubmissionView(ctx, invite.ID, hash))
}

func TestAccountSubmissionBatchPostgresRollbackAllMembers(t *testing.T) {
	ctx := context.Background()
	repo, invite, hash := batchInviteFixture(t, service.PlatformAnthropic)
	_, err := integrationDB.Exec(`ALTER TABLE accounts ADD CONSTRAINT batch_rollback_fixture_geili CHECK(name <> ` + pq.QuoteLiteral(invite.Config.Name+" #3") + `) NOT VALID`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`ALTER TABLE accounts DROP CONSTRAINT IF EXISTS batch_rollback_fixture_geili`)
	})
	proxyName := fmt.Sprintf("batch-proxy-%d", time.Now().UnixNano())
	err = repo.SubmitInviteBatch(ctx, hash, []string{"synthetic-a", "synthetic-b", "synthetic-c"}, service.SubmissionOptions{Connection: &service.SubmissionConnectionInput{Mode: "custom", Proxy: &service.SubmissionProxyDraft{Name: proxyName, Protocol: "http", Host: "8.8.8.8", Port: 8080, Password: "synthetic-proxy-secret"}}})
	require.ErrorIs(t, err, service.ErrSubmissionUnavailable)
	require.NotContains(t, err.Error(), "synthetic")
	current, err := repo.InspectInvite(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, "pending", current.Status)
	for _, query := range []string{`SELECT count(*) FROM account_submission_invites_geili WHERE parent_invite_id=$1`, `SELECT count(*) FROM account_submission_connections_geili WHERE invite_id=$1`} {
		var n int
		require.NoError(t, integrationDB.QueryRow(query, invite.ID).Scan(&n))
		require.Zero(t, n)
	}
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM accounts WHERE name LIKE $1`, invite.Config.Name+" #%").Scan(&n))
	require.Zero(t, n)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM proxies WHERE name=$1`, "[external] "+proxyName).Scan(&n))
	require.Zero(t, n)
}

func TestAccountSubmissionFullFormBatchPostgresSharedTemplateAndIndependentKeys(t *testing.T) {
	for _, platform := range []string{service.PlatformAnthropic, service.PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			ctx := context.Background()
			repo, invite, hash := batchInviteFixture(t, platform)
			name := "advanced " + hash[len(hash)-8:]
			note := "shared notes"
			zero := 0.0
			load := 19
			expiry := time.Now().Add(time.Hour).Truncate(time.Second)
			baseURL := "https://api.anthropic.com"
			if platform == service.PlatformOpenAI {
				baseURL = "https://api.openai.com"
			}
			prepared := &service.Account{Name: name, Platform: platform, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-template", "base_url": baseURL, "model_mapping": map[string]any{"from": "to"}}, Extra: map[string]any{"external_submission_form_geili": true, "quota_daily_limit": 7}, Notes: &note, Concurrency: 37, Priority: 5, RateMultiplier: &zero, LoadFactor: &load, ExpiresAt: &expiry, AutoPauseOnExpired: false}
			connections := []service.SubmissionConnectionInput{{Mode: "custom", Proxy: &service.SubmissionProxyDraft{Name: "A", Protocol: "http", Host: "8.8.8.8", Port: 8080, Password: "synthetic-password-a"}}, {Mode: "custom", Proxy: &service.SubmissionProxyDraft{Name: "B", Protocol: "http", Host: "1.1.1.1", Port: 8080, Password: "synthetic-password-b"}}}
			opts := service.SubmissionOptions{PreparedAccount: prepared, Name: &name, PerKeyConnections: connections}
			require.NoError(t, repo.SubmitInviteBatch(ctx, hash, []string{"synthetic-first", "synthetic-second"}, opts))
			rows, err := integrationDB.Query(`SELECT a.name,a.credentials,a.extra,a.notes,a.concurrency,a.priority,a.rate_multiplier,a.load_factor,a.expires_at,a.auto_pause_on_expired,a.status,a.schedulable,p.host,p.password FROM accounts a JOIN account_submission_invites_geili i ON i.account_id=a.id JOIN proxies p ON p.id=a.proxy_id WHERE i.id=$1 OR i.parent_invite_id=$1 ORDER BY i.id`, invite.ID)
			require.NoError(t, err)
			count := 0
			for rows.Next() {
				var actualName, notes, status, host, password string
				var credsRaw, extraRaw []byte
				var concurrency, priority, factor int
				var multiplier float64
				var expires time.Time
				var autoPause, enabled bool
				require.NoError(t, rows.Scan(&actualName, &credsRaw, &extraRaw, &notes, &concurrency, &priority, &multiplier, &factor, &expires, &autoPause, &status, &enabled, &host, &password))
				var creds, extra map[string]any
				require.NoError(t, json.Unmarshal(credsRaw, &creds))
				require.NoError(t, json.Unmarshal(extraRaw, &extra))
				require.Equal(t, []string{"synthetic-first", "synthetic-second"}[count], creds["api_key"])
				require.Equal(t, baseURL, creds["base_url"])
				require.Equal(t, map[string]any{"from": "to"}, creds["model_mapping"])
				require.Equal(t, fmt.Sprintf("%s #%d", name, count+1), actualName)
				require.Equal(t, note, notes)
				require.Equal(t, 37, concurrency)
				require.Equal(t, 5, priority)
				require.Zero(t, multiplier)
				require.Equal(t, 19, factor)
				require.True(t, expires.Equal(expiry))
				require.False(t, autoPause)
				require.Equal(t, "inactive", status)
				require.False(t, enabled)
				require.Equal(t, float64(7), extra["quota_daily_limit"])
				require.Equal(t, true, extra["external_submission_form_geili"])
				require.Equal(t, connections[count].Proxy.Host, host)
				require.Equal(t, connections[count].Proxy.Password, password)
				count++
			}
			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
			require.Equal(t, 2, count)
			require.Equal(t, "synthetic-template", prepared.Credentials["api_key"], "shared template must not be mutated by per-member credentials")
			require.NoError(t, repo.SubmitInviteBatch(ctx, hash, []string{"replacement"}, service.SubmissionOptions{}))
			var n int
			require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM account_submission_invites_geili WHERE id=$1 OR parent_invite_id=$1`, invite.ID).Scan(&n))
			require.Equal(t, 2, n)
		})
	}
}

func TestAccountSubmissionFullFormBatchPostgresRollback(t *testing.T) {
	ctx := context.Background()
	repo, invite, hash := batchInviteFixture(t, service.PlatformOpenAI)
	name := invite.Config.Name
	prefix := fmt.Sprintf("advanced-rollback-%d", time.Now().UnixNano())
	_, err := integrationDB.Exec(`ALTER TABLE accounts ADD CONSTRAINT advanced_batch_rollback_fixture_geili CHECK(name <> ` + pq.QuoteLiteral(name+" #2") + `) NOT VALID`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`ALTER TABLE accounts DROP CONSTRAINT IF EXISTS advanced_batch_rollback_fixture_geili`)
	})
	prepared := &service.Account{Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-template", "base_url": "https://api.openai.com"}, Extra: map[string]any{"external_submission_form_geili": true}, Concurrency: 17}
	connections := []service.SubmissionConnectionInput{{Mode: "custom", Proxy: &service.SubmissionProxyDraft{Name: prefix + "-A", Protocol: "http", Host: "8.8.8.8", Port: 8080}}, {Mode: "custom", Proxy: &service.SubmissionProxyDraft{Name: prefix + "-B", Protocol: "http", Host: "1.1.1.1", Port: 8080}}}
	err = repo.SubmitInviteBatch(ctx, hash, []string{"synthetic-first", "synthetic-second"}, service.SubmissionOptions{Name: &name, PreparedAccount: prepared, PerKeyConnections: connections})
	require.ErrorIs(t, err, service.ErrSubmissionUnavailable)
	current, err := repo.InspectInvite(ctx, hash)
	require.NoError(t, err)
	require.Nil(t, current.AccountID)
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM proxies WHERE name LIKE $1`, "[external] "+prefix+"%").Scan(&n))
	require.Zero(t, n)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM account_submission_invites_geili WHERE parent_invite_id=$1`, invite.ID).Scan(&n))
	require.Zero(t, n)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM accounts WHERE name LIKE $1`, name+" #%").Scan(&n))
	require.Zero(t, n)
}
