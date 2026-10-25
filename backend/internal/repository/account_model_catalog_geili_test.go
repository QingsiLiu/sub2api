package repository

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type modelCatalogSQLCounterGeili struct {
	sqlExecutor
	presentation, mandatory atomic.Int64
}

func (c *modelCatalogSQLCounterGeili) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(query, "FROM account_runtime_geili WHERE account_id=ANY($1)") {
		c.presentation.Add(1)
	}
	if query == accountRuntimeGetSQLGeili || query == accountRuntimeAdmissionSQLGeili {
		c.mandatory.Add(1)
	}
	return c.sqlExecutor.QueryContext(ctx, query, args...)
}

func modelCatalogRepositoryGeili(t *testing.T) (*accountRepository, sqlmock.Sqlmock, *modelCatalogSQLCounterGeili) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = client.Close() })
	counter := &modelCatalogSQLCounterGeili{sqlExecutor: db}
	return newAccountRepositoryWithSQL(client, counter, nil), mock, counter
}

func modelCatalogAccountRowsGeili() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "name", "platform", "type", "status", "schedulable", "credentials"}).
		AddRow(17, "catalog-fixture", service.PlatformAnthropic, service.AccountTypeAPIKey, service.StatusActive, true,
			`{"model_mapping":{"claude-opus-5":"claude-opus-5"}}`)
}

func expectModelCatalogAccountConversionGeili(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT .* FROM "accounts"`).WillReturnRows(modelCatalogAccountRowsGeili())
	mock.ExpectQuery(`SELECT .* FROM "account_groups"`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
}

func TestAccountModelCatalogGeiliOnlyAdminOptsIntoRuntimePresentation(t *testing.T) {
	for _, call := range []string{"catalog", "original availability", "admin GetByID", "scheduler list", "GetByIDs"} {
		t.Run(call, func(t *testing.T) {
			repo, mock, counter := modelCatalogRepositoryGeili(t)
			if call == "catalog" {
				mock.ExpectQuery(regexp.QuoteMeta(accountModelCatalogSelectGeili)).WithArgs(`{"anthropic"}`).WillReturnRows(sqlmock.NewRows([]string{"id", "platform", "type", "credentials"}).AddRow(17, service.PlatformAnthropic, service.AccountTypeAPIKey, `{"model_mapping":{"claude-opus-5":"claude-opus-5"}}`))
			} else {
				expectModelCatalogAccountConversionGeili(mock)
			}
			if call == "admin GetByID" {
				mock.ExpectQuery(`SELECT .* FROM account_runtime_geili WHERE account_id=ANY\(\$1\)`).WithArgs("{17}").WillReturnRows(runtimeRowsGeili(`{}`))
			}
			ctx := context.Background()
			if call == "admin GetByID" {
				ctx = service.WithAccountRuntimePresentationGeili(ctx)
			}
			var accounts []service.Account
			var err error
			switch call {
			case "catalog":
				accounts, err = repo.ListModelCatalogCandidatesGeili(ctx, nil, []string{service.PlatformAnthropic}, true)
			case "original availability":
				accounts, err = repo.ListModelAvailabilityCandidates(ctx, nil, []string{service.PlatformAnthropic}, true)
			case "admin GetByID":
				var account *service.Account
				account, err = repo.GetByID(ctx, 17)
				if account != nil {
					accounts = []service.Account{*account}
				}
			case "scheduler list":
				accounts, err = repo.ListSchedulableByPlatform(ctx, service.PlatformAnthropic)
			default:
				var pointers []*service.Account
				pointers, err = repo.GetByIDs(ctx, []int64{17})
				for _, account := range pointers {
					accounts = append(accounts, *account)
				}
			}
			require.NoError(t, err)
			require.Len(t, accounts, 1)
			require.Equal(t, "claude-opus-5", accounts[0].GetModelMapping()["claude-opus-5"])
			if call != "admin GetByID" {
				require.Nil(t, accounts[0].RuntimeState)
				require.Zero(t, counter.presentation.Load(), "even a swallowed best-effort query error must count as a read")
			} else {
				require.NotNil(t, accounts[0].RuntimeState)
				require.Equal(t, int64(2), accounts[0].RuntimeState.Version)
				require.Equal(t, int64(1), counter.presentation.Load())
			}
			require.False(t, isAccountModelCatalogContextGeili(ctx), "the caller's context is never changed")
		})
	}
}

func TestAccountModelCatalogGeiliPrivateContextDoesNotSkipMandatoryOrSuccessReads(t *testing.T) {
	repo, mock, counter := modelCatalogRepositoryGeili(t)
	ctx := context.WithValue(context.Background(), accountModelCatalogContextKeyGeili{}, true)
	mock.ExpectQuery(regexp.QuoteMeta(accountRuntimeGetSQLGeili)).WithArgs(int64(17)).WillReturnRows(runtimeRowsGeili(`{}`))
	mock.ExpectQuery(regexp.QuoteMeta(accountRuntimeAdmissionSQLGeili)).WithArgs(int64(17), "model").WillReturnRows(preparedAdmissionRowsGeili(false, true, "balance"))
	mock.ExpectQuery(regexp.QuoteMeta(accountRuntimeGetSQLGeili)).WithArgs(int64(17)).WillReturnRows(runtimeRowsGeili(`{}`))
	state, err := repo.GetAccountRuntimeGeili(ctx, 17)
	require.NoError(t, err)
	require.NotNil(t, state)
	gate, err := repo.CheckAccountRuntimeAdmissionGeili(ctx, 17, "model")
	require.NoError(t, err)
	require.False(t, gate.Allowed)
	require.True(t, gate.BalanceBlocked)
	require.NoError(t, repo.ResetAccountModelSuccessGeili(ctx, 17, "model"))
	require.Equal(t, int64(3), counter.mandatory.Load())
	require.Zero(t, counter.presentation.Load())
}

func TestAccountModelCatalogGeiliGroupedDirectoryPreservesQueryScope(t *testing.T) {
	repo, mock, counter := modelCatalogRepositoryGeili(t)
	mock.ExpectQuery(regexp.QuoteMeta(accountModelCatalogSelectGeili+` JOIN account_groups ag ON ag.account_id=a.id AND ag.group_id=$2 WHERE a.deleted_at IS NULL AND a.status='active' AND a.schedulable=TRUE AND a.platform=ANY($1) ORDER BY ag.priority,a.priority`)).WithArgs(`{"anthropic"}`, int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "platform", "type", "credentials"}).AddRow(17, service.PlatformAnthropic, service.AccountTypeAPIKey, `{"model_mapping":{"claude-opus-5":"claude-opus-5"}}`))
	group := int64(7)
	accounts, err := repo.ListModelCatalogCandidatesGeili(context.Background(), &group, []string{service.PlatformAnthropic}, false)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Nil(t, accounts[0].RuntimeState)
	require.Zero(t, counter.presentation.Load())
}

func TestAccountModelCatalogGeiliEmptyAndFailedReads(t *testing.T) {
	repo, mock, _ := modelCatalogRepositoryGeili(t)
	accounts, err := repo.ListModelCatalogCandidatesGeili(context.Background(), nil, nil, false)
	require.NoError(t, err)
	require.Empty(t, accounts)
	mock.ExpectQuery(regexp.QuoteMeta(accountModelCatalogSelectGeili)).WithArgs(`{"anthropic"}`).WillReturnRows(sqlmock.NewRows([]string{"id", "platform", "type", "credentials"}).AddRow(17, "anthropic", "apikey", `broken`))
	_, err = repo.ListModelCatalogCandidatesGeili(context.Background(), nil, []string{"anthropic"}, false)
	require.ErrorContains(t, err, "decode model catalog declarations")
	mock.ExpectQuery(regexp.QuoteMeta(accountModelCatalogSelectGeili)).WithArgs(`{"anthropic"}`).WillReturnRows(sqlmock.NewRows([]string{"id", "platform", "type", "credentials"}).AddRow(17, "anthropic", "apikey", nil).RowError(0, context.DeadlineExceeded))
	_, err = repo.ListModelCatalogCandidatesGeili(context.Background(), nil, []string{"anthropic"}, false)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
