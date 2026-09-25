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
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type recoveryFixture struct {
	user, key, sub, lot int64
	admission, request  string
	from, cutoff        time.Time
}

func makeRecoveryFixture(t *testing.T) recoveryFixture {
	t.Helper()
	ctx := context.Background()
	client := testEntClient(t)
	id := uuid.NewString()
	u := mustCreateUser(t, client, &service.User{Email: "recovery-" + id + "@example.com", PasswordHash: "hash", Balance: 77})
	g := mustCreateGroup(t, client, &service.Group{Name: "recovery-" + id, Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription})
	k := mustCreateApiKey(t, client, &service.APIKey{UserID: u.ID, Key: "sk-recovery-" + id, Name: "recovery", GroupID: &g.ID})
	sub := mustCreateSubscription(t, client, &service.UserSubscription{UserID: u.ID, GroupID: g.ID})
	// A unique historical microsecond range isolates each test without truncation.
	from := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Microsecond)
	f := recoveryFixture{user: u.ID, key: k.ID, sub: sub.ID, admission: "admission-" + id, request: "local:" + id, from: from, cutoff: from.Add(2 * time.Microsecond)}
	err := integrationDB.QueryRowContext(ctx, `INSERT INTO user_subscription_entitlements(user_subscription_id,lot_index,status,starts_at,expires_at,daily_limit_usd,weekly_limit_usd,monthly_limit_usd,lifetime_usage_usd,source_type)
 VALUES($1,0,'expired',$2,$3,45,315,1350,10.84225728,'legacy') RETURNING id`, f.sub, from.Add(-24*time.Hour), from.Add(time.Hour)).Scan(&f.lot)
	require.NoError(t, err)
	lots, err := json.Marshal([]map[string]any{{"id": f.lot, "user_subscription_id": f.sub}})
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO subscription_requests(request_key,subscription_id,api_key_id,status,lots,admitted_at,settled_at,billing_request_id,cost_usd) VALUES($1,$2,$3,'settled',$4,$5,$6,$7,10.84225728)`, f.admission, f.sub, f.key, string(lots), from.Add(-time.Minute), from.Add(time.Microsecond), f.request)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO subscription_usage_allocations(request_key,entitlement_id,cost_usd,daily_window_start) VALUES($1,$2,10.84225728,$3)`, f.admission, f.lot, from.Add(-time.Hour))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO usage_billing_dedup(request_id,api_key_id,request_fingerprint,created_at) VALUES($1,$2,'original-fingerprint',$3)`, f.request, f.key, from.Add(time.Microsecond))
	require.NoError(t, err)
	t.Cleanup(func() { cleanupBillingFixture(t, u.ID, nil, []int64{g.ID}) })
	return f
}
func recoveryFinancialState(t *testing.T, f recoveryFixture) string {
	t.Helper()
	var raw string
	require.NoError(t, integrationDB.QueryRow(`SELECT jsonb_build_object('user',(SELECT to_jsonb(x) FROM users x WHERE id=$1),'key',(SELECT to_jsonb(x) FROM api_keys x WHERE id=$2),'subscription',(SELECT to_jsonb(x) FROM user_subscriptions x WHERE id=$3),'lots',(SELECT jsonb_agg(to_jsonb(x)) FROM user_subscription_entitlements x WHERE user_subscription_id=$3),'allocations',(SELECT jsonb_agg(to_jsonb(x)) FROM subscription_usage_allocations x WHERE request_key=$4),'admission',(SELECT to_jsonb(x) FROM subscription_requests x WHERE request_key=$4),'daily',(SELECT jsonb_agg(to_jsonb(x)) FROM subscription_daily_usage x WHERE subscription_id=$3),'dedup',(SELECT jsonb_agg(to_jsonb(x)) FROM usage_billing_dedup x WHERE api_key_id=$2))::text`, f.user, f.key, f.sub, f.admission).Scan(&raw))
	return raw
}
func TestUsageRecoveryIntegration_IdempotentNoFinancialMutation(t *testing.T) {
	f := makeRecoveryFixture(t)
	ctx := context.Background()
	repo := NewUsageRecoveryRepository(integrationDB)
	// An unexplained dedup operation stays explicitly unattributed and amount unknown.
	_, err := integrationDB.Exec(`INSERT INTO usage_billing_dedup(request_id,api_key_id,request_fingerprint,created_at) VALUES($1,$2,'opaque-only',$3)`, "local:unknown-"+f.admission, f.key, f.from)
	require.NoError(t, err)
	before := recoveryFinancialState(t, f)
	m, err := repo.Scan(ctx, f.from, f.cutoff)
	require.NoError(t, err)
	require.Equal(t, 1, m.Summary.Recoverable)
	require.Equal(t, 1, m.Summary.Unresolved)
	require.Equal(t, 1, m.Summary.UnknownAmountCount)
	require.Equal(t, "10.8422572800", m.Summary.KnownAmountUSD)
	hash, err := UsageRecoveryManifestDigest(m)
	require.NoError(t, err)
	// CLI reads the JSON in a fresh process; time zone representations must not
	// spuriously fail the evidence comparison after the approved roundtrip.
	rawManifest, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	var parsed UsageRecoveryManifest
	require.NoError(t, json.Unmarshal(rawManifest, &parsed))
	m = &parsed
	result, err := repo.Apply(ctx, m, hash, f.cutoff, 1)
	require.NoError(t, err)
	require.Equal(t, 1, result.Inserted)
	require.Equal(t, 1, result.Unresolved)
	repeat, err := repo.Apply(ctx, m, hash, f.cutoff, 1)
	require.NoError(t, err)
	require.Zero(t, repeat.Inserted)
	require.Equal(t, 1, repeat.AlreadyRecovered)
	require.Equal(t, before, recoveryFinancialState(t, f))
	var amount, source, completeness string
	var detail []byte
	var completed, account, group any
	require.NoError(t, integrationDB.QueryRow(`SELECT charged_amount::text,record_source,record_completeness,detail,completed_at,account_id,group_id FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, f.request, f.key).Scan(&amount, &source, &completeness, &detail, &completed, &account, &group))
	require.Equal(t, "10.8422572800", amount)
	require.Equal(t, "historical_recovery", source)
	require.Equal(t, "partial", completeness)
	require.Nil(t, completed)
	require.Nil(t, account)
	require.Nil(t, group)
	require.NotContains(t, string(detail), "input_tokens")
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, f.request, f.key).Scan(&n))
	require.Zero(t, n)
	// The financial view shows the exact amount but leaves fabricated usage absent.
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_financial_records WHERE request_id=$1 AND api_key_id=$2 AND actual_cost=10.84225728 AND model IS NULL AND input_tokens IS NULL`, f.request, f.key).Scan(&n))
	require.Equal(t, 1, n)
}
func TestUsageRecoveryIntegration_RejectsTamperAndChangedEvidence(t *testing.T) {
	f := makeRecoveryFixture(t)
	ctx := context.Background()
	repo := NewUsageRecoveryRepository(integrationDB)
	m, err := repo.Scan(ctx, f.from, f.cutoff)
	require.NoError(t, err)
	hash, err := UsageRecoveryManifestDigest(m)
	require.NoError(t, err)
	altered := *m
	altered.Candidates = append([]UsageRecoveryCandidate{}, m.Candidates...)
	altered.Candidates[0].RequestID = "local:tamper"
	_, err = repo.Apply(ctx, &altered, hash, f.cutoff, 10)
	require.ErrorContains(t, err, "SHA256")
	_, err = integrationDB.Exec(`UPDATE subscription_usage_allocations SET cost_usd=10.85 WHERE request_key=$1`, f.admission)
	require.NoError(t, err)
	_, err = repo.Apply(ctx, m, hash, f.cutoff, 10)
	require.ErrorContains(t, err, "evidence changed")
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_settlement_receipts WHERE request_id=$1`, f.request).Scan(&n))
	require.Zero(t, n)
}
func TestUsageRecoveryIntegration_DetailArrivesAfterScan(t *testing.T) {
	f := makeRecoveryFixture(t)
	ctx := context.Background()
	repo := NewUsageRecoveryRepository(integrationDB)
	m, err := repo.Scan(ctx, f.from, f.cutoff)
	require.NoError(t, err)
	hash, err := UsageRecoveryManifestDigest(m)
	require.NoError(t, err)
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: fmt.Sprintf("recovery-late-%d", time.Now().UnixNano()), Type: service.AccountTypeAPIKey})
	_, err = integrationDB.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,actual_cost,subscription_id,created_at) VALUES($1,$2,$3,$4,'verified-late',10.84225728,$5,$6)`, f.user, f.key, account.ID, f.request, f.sub, f.from)
	require.NoError(t, err)
	result, err := repo.Apply(ctx, m, hash, f.cutoff, 10)
	require.NoError(t, err)
	require.Equal(t, 1, result.DetailArrived)
	require.Zero(t, result.Inserted)
}
func TestUsageRecoveryIntegration_ExistingZeroFailureNeverCountsAsRecovered(t *testing.T) {
	f := makeRecoveryFixture(t)
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "recovery-zero-" + uuid.NewString(), Type: service.AccountTypeAPIKey})
	_, err := integrationDB.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,actual_cost,subscription_id,created_at) VALUES($1,$2,$3,$4,'failed-zero',0,$5,$6)`, f.user, f.key, account.ID, f.request, f.sub, f.from)
	require.NoError(t, err)
	m, err := NewUsageRecoveryRepository(integrationDB).Scan(context.Background(), f.from, f.cutoff)
	require.NoError(t, err)
	require.Zero(t, m.Summary.Recoverable)
	require.Equal(t, 1, m.Summary.Unresolved)
	require.Contains(t, m.Candidates[0].Reason, "conflicts")
}

func TestUsageRecoveryIntegration_ConcurrentRunsExactlyOneReceipt(t *testing.T) {
	f := makeRecoveryFixture(t)
	ctx := context.Background()
	repo := NewUsageRecoveryRepository(integrationDB)
	m, err := repo.Scan(ctx, f.from, f.cutoff)
	require.NoError(t, err)
	hash, err := UsageRecoveryManifestDigest(m)
	require.NoError(t, err)
	before := recoveryFinancialState(t, f)
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, e := repo.Apply(ctx, m, hash, f.cutoff, 1); errs <- e }()
	}
	close(start)
	wg.Wait()
	close(errs)
	successes := 0
	for e := range errs {
		if e == nil {
			successes++
		}
	}
	require.GreaterOrEqual(t, successes, 1)
	repeat, err := repo.Apply(ctx, m, hash, f.cutoff, 1)
	require.NoError(t, err)
	require.Equal(t, 1, repeat.AlreadyRecovered)
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, f.request, f.key).Scan(&n))
	require.Equal(t, 1, n)
	require.Equal(t, before, recoveryFinancialState(t, f))
}
func TestUsageRecoveryIntegration_LateMismatchedDetailFailsClosed(t *testing.T) {
	f := makeRecoveryFixture(t)
	ctx := context.Background()
	repo := NewUsageRecoveryRepository(integrationDB)
	m, err := repo.Scan(ctx, f.from, f.cutoff)
	require.NoError(t, err)
	hash, err := UsageRecoveryManifestDigest(m)
	require.NoError(t, err)
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "recovery-wrong-" + uuid.NewString(), Type: service.AccountTypeAPIKey})
	_, err = integrationDB.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,actual_cost,subscription_id,created_at) VALUES($1,$2,$3,$4,'late-wrong',0,$5,$6)`, f.user, f.key, account.ID, f.request, f.sub, f.from)
	require.NoError(t, err)
	_, err = repo.Apply(ctx, m, hash, f.cutoff, 1)
	require.ErrorContains(t, err, "evidence changed")
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_settlement_receipts WHERE request_id=$1`, f.request).Scan(&n))
	require.Zero(t, n)
}

func TestUsageRecoveryIntegration_BeijingAdmissionDateSurvivesMidnight(t *testing.T) {
	f := makeRecoveryFixture(t)
	ctx := context.Background()
	day := time.Now().Add(-72 * time.Hour).UTC().Truncate(24 * time.Hour)
	// Admission at 23:59:59 Shanghai; settlement two seconds into the next day.
	admitted := day.Add(15*time.Hour + 59*time.Minute + 59*time.Second)
	settled := admitted.Add(3 * time.Second)
	_, err := integrationDB.Exec(`UPDATE subscription_requests SET admitted_at=$2,settled_at=$3,cost_usd=24.19470704 WHERE request_key=$1`, f.admission, admitted, settled)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE subscription_usage_allocations SET cost_usd=24.19470704 WHERE request_key=$1`, f.admission)
	require.NoError(t, err)
	term := "recovery-term-" + f.admission
	_, err = integrationDB.Exec(`INSERT INTO subscription_contract_terms(term_id,subscription_id,starts_at,expires_at) VALUES($1,$2,$3,$4)`, term, f.sub, day.Add(-24*time.Hour), day.Add(24*time.Hour))
	require.NoError(t, err)
	_, err = integrationDB.Exec(`INSERT INTO subscription_request_contracts(request_key,term_id,usage_date) VALUES($1,$2,$3)`, f.admission, term, day.Format("2006-01-02"))
	require.NoError(t, err)
	repo := NewUsageRecoveryRepository(integrationDB)
	m, err := repo.Scan(ctx, settled.Add(-time.Microsecond), settled.Add(time.Microsecond))
	require.NoError(t, err)
	// This wider date may contain earlier test fixtures; find the exact identity.
	var candidate *UsageRecoveryCandidate
	for i := range m.Candidates {
		if m.Candidates[i].RequestID == f.request {
			candidate = &m.Candidates[i]
			break
		}
	}
	require.NotNil(t, candidate)
	require.Equal(t, "recoverable", candidate.Disposition)
	require.Equal(t, day.Format("2006-01-02"), *candidate.AccountingDate)
	require.Equal(t, "24.1947070400", *candidate.AmountUSD)
	hash, err := UsageRecoveryManifestDigest(m)
	require.NoError(t, err)
	_, err = repo.Apply(ctx, m, hash, m.Cutoff, 100)
	require.NoError(t, err)
	var date string
	require.NoError(t, integrationDB.QueryRow(`SELECT accounting_date::text FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, f.request, f.key).Scan(&date))
	require.Equal(t, day.Format("2006-01-02"), date)
}

func TestUsageRecoveryIntegration_LateLogMergesWithRecoveredReceipt(t *testing.T) {
	f := makeRecoveryFixture(t)
	ctx := context.Background()
	repo := NewUsageRecoveryRepository(integrationDB)
	m, err := repo.Scan(ctx, f.from, f.cutoff)
	require.NoError(t, err)
	hash, err := UsageRecoveryManifestDigest(m)
	require.NoError(t, err)
	result, err := repo.Apply(ctx, m, hash, f.cutoff, 100)
	require.NoError(t, err)
	require.Equal(t, 1, result.Inserted)
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "recovery-merge-" + uuid.NewString(), Type: service.AccountTypeAPIKey})
	_, err = integrationDB.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,input_tokens,actual_cost,subscription_id,created_at) VALUES($1,$2,$3,$4,'late-observed',42,10.84225728,$5,$6)`, f.user, f.key, account.ID, f.request, f.sub, f.from)
	require.NoError(t, err)
	var n int
	var amount string
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*),SUM(actual_cost)::text FROM usage_financial_records WHERE request_id=$1 AND api_key_id=$2`, f.request, f.key).Scan(&n, &amount))
	require.Equal(t, 1, n)
	require.Equal(t, "10.8422572800", amount)
}
