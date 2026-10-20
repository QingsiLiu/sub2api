package repository

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func runtimeTestRepositoryGeili(t *testing.T) (*accountRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = client.Close() })
	return newAccountRepositoryWithSQL(client, db, nil), mock
}

func runtimeRowsGeili(models string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"account_id", "version", "balance_blocked", "balance_reason", "balance_blocked_at", "stop_reason", "protection_enabled", "model_states", "last_probe", "next_check_at", "updated_at"}).AddRow(int64(17), int64(2), false, "", nil, "", false, models, nil, nil, time.Now())
}

type runtimeModelsArgumentGeili struct {
	model   string
	blocked bool
	count   int
}

func (a runtimeModelsArgumentGeili) Match(value driver.Value) bool {
	raw, ok := value.(string)
	if !ok {
		return false
	}
	var models map[string]service.AccountModelHealthGeili
	if json.Unmarshal([]byte(raw), &models) != nil {
		return false
	}
	health, ok := models[a.model]
	return ok && health.Blocked == a.blocked && health.FailureCount == a.count && (!a.blocked || health.NextCheckAt != nil)
}

func expectRuntimeWriteStartGeili(mock sqlmock.Sqlmock, models string) {
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM accounts .* FOR UPDATE`).WithArgs(int64(17)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(17))
	mock.ExpectExec(`INSERT INTO account_runtime_geili`).WithArgs(int64(17)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT .* FROM account_runtime_geili WHERE account_id=\$1`).WithArgs(int64(17)).WillReturnRows(runtimeRowsGeili(models))
}

func expectRuntimeOutboxGeili(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).WithArgs(service.SchedulerOutboxEventAccountChanged, int64(17), nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
}

func TestAccountRuntimeGeiliFailureThresholdAndSuccessReset(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			repo, mock := runtimeTestRepositoryGeili(t)
			now := time.Now().UTC()
			models, _ := json.Marshal(map[string]service.AccountModelHealthGeili{"claude-opus-5": {FailureCount: count, FailureWindowStartedAt: &now}})
			expectRuntimeWriteStartGeili(mock, string(models))
			mock.ExpectExec(`UPDATE account_runtime_geili SET`).WithArgs(int64(17), int64(3), false, "", nil, "", false, runtimeModelsArgumentGeili{"claude-opus-5", count == 2, count + 1}, "null", nil).WillReturnResult(sqlmock.NewResult(0, 1))
			expectRuntimeOutboxGeili(mock)
			mock.ExpectCommit()
			require.NoError(t, repo.RecordModelFailureGeili(context.Background(), 17, "claude-opus-5", "invalid_tool_arguments", time.Time{}))
		})
	}
}

func TestAccountRuntimeGeiliDebtLabelDoesNotStopScheduling(t *testing.T) {
	repo, mock := runtimeTestRepositoryGeili(t)
	expectRuntimeWriteStartGeili(mock, `{}`)
	mock.ExpectExec(`UPDATE account_runtime_geili SET`).WithArgs(int64(17), int64(3), true, "insufficient_balance", sqlmock.AnyArg(), "", false, "{}", "null", nil).WillReturnResult(sqlmock.NewResult(0, 1))
	expectRuntimeOutboxGeili(mock)
	mock.ExpectCommit()
	require.NoError(t, repo.MarkAccountBalanceBlockedGeili(context.Background(), 17, "insufficient_balance"))
}

func TestAccountRuntimeGeiliProbeBookkeepingPreservesCASVersion(t *testing.T) {
	repo, mock := runtimeTestRepositoryGeili(t)
	expectRuntimeWriteStartGeili(mock, `{}`)
	mock.ExpectExec(`INSERT INTO account_probe_logs_geili`).WithArgs(int64(17), "claude-opus-5", int64(2), "fingerprint", "manual", true, true, false, sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`UPDATE account_runtime_geili SET`).WithArgs(int64(17), int64(2), false, "", nil, "", false, sqlmock.AnyArg(), sqlmock.AnyArg(), nil).WillReturnResult(sqlmock.NewResult(0, 1))
	expectRuntimeOutboxGeili(mock)
	mock.ExpectCommit()
	require.NoError(t, repo.RecordAccountProbeGeili(context.Background(), service.AccountProbeResultGeili{Snapshot: service.AccountProbeSnapshotGeili{AccountID: 17, RuntimeVersion: 2, Fingerprint: "fingerprint"}, Model: "claude-opus-5", Source: "manual", Success: true, Complete: true}))
}

func TestAccountRuntimeGeiliLeaseEnforcesGlobalBudgetAndAccountExclusion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		held  bool
	}{{"global limit", 4, false}, {"same account other model", 1, true}} {
		t.Run(tc.name, func(t *testing.T) {
			repo, mock := runtimeTestRepositoryGeili(t)
			mock.ExpectBegin()
			mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(`DELETE FROM account_probe_leases_geili WHERE expires_at<=NOW\(\)`).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(`SELECT COUNT\(\*\),COALESCE\(BOOL_OR`).WithArgs(int64(17)).WillReturnRows(sqlmock.NewRows([]string{"count", "held"}).AddRow(tc.count, tc.held))
			mock.ExpectRollback()
			lease, err := repo.AcquireProbeLeaseGeili(context.Background(), 17, "other-model", 35*time.Second)
			require.NoError(t, err)
			require.Nil(t, lease)
		})
	}
}

func TestAccountRuntimeGeiliStaleLeaseReleaseRequiresToken(t *testing.T) {
	repo, mock := runtimeTestRepositoryGeili(t)
	mock.ExpectExec(`DELETE FROM account_probe_leases_geili WHERE account_id=\$1 AND token=\$2`).WithArgs(int64(17), "old-token").WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, repo.ReleaseProbeLeaseGeili(context.Background(), &service.AccountProbeLeaseGeili{AccountID: 17, Token: "old-token"}))
}

func TestAccountRuntimeGeiliAdmissionReadsDurableGate(t *testing.T) {
	repo, mock := runtimeTestRepositoryGeili(t)
	mock.ExpectQuery(`(?s)SELECT .*r.model_states->''.*r.model_states->\$2.*FROM accounts a LEFT JOIN account_runtime_geili`).WithArgs(int64(17), "claude-opus-5").WillReturnRows(sqlmock.NewRows([]string{"allowed", "balance_blocked", "reason", "credential_account_id"}).AddRow(false, true, "balance", int64(17)))
	gate, err := repo.CheckAccountRuntimeAdmissionGeili(context.Background(), 17, "claude-opus-5")
	require.NoError(t, err)
	require.False(t, gate.Allowed)
	require.True(t, gate.BalanceBlocked)
}

func TestAccountRuntimeGeiliHealthySuccessCreatesNoBlankState(t *testing.T) {
	repo, mock := runtimeTestRepositoryGeili(t)
	mock.ExpectQuery(`SELECT .* FROM account_runtime_geili WHERE account_id=\$1`).WithArgs(int64(17)).WillReturnRows(sqlmock.NewRows([]string{"account_id", "version", "balance_blocked", "balance_reason", "balance_blocked_at", "stop_reason", "protection_enabled", "model_states", "last_probe", "next_check_at", "updated_at"}))
	require.NoError(t, repo.ResetAccountModelSuccessGeili(context.Background(), 17, "claude-opus-5"))
}

func TestAccountRuntimeGeiliSuccessResetsCounterAndPreservesBlock(t *testing.T) {
	repo, mock := runtimeTestRepositoryGeili(t)
	now := time.Now().UTC()
	models, _ := json.Marshal(map[string]service.AccountModelHealthGeili{"claude-opus-5": {FailureCount: 3, Blocked: true, NextCheckAt: &now}})
	mock.ExpectQuery(`SELECT .* FROM account_runtime_geili WHERE account_id=\$1`).WithArgs(int64(17)).WillReturnRows(runtimeRowsGeili(string(models)))
	expectRuntimeWriteStartGeili(mock, string(models))
	mock.ExpectExec(`UPDATE account_runtime_geili SET`).WithArgs(int64(17), int64(3), false, "", nil, "", false, runtimeModelsArgumentGeili{"claude-opus-5", true, 0}, "null", nil).WillReturnResult(sqlmock.NewResult(0, 1))
	expectRuntimeOutboxGeili(mock)
	mock.ExpectCommit()
	require.NoError(t, repo.ResetAccountModelSuccessGeili(context.Background(), 17, "claude-opus-5"))
}
