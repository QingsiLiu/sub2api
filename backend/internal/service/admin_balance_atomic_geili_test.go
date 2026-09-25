package service

import (
	"context"
	"errors"
	"math"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

type atomicAdminBalanceFixture struct {
	UserRepository
	committed User
	audit     []*RedeemCode
	pending   map[*dbent.Tx]*User
	auditErr  error
}

func (r *atomicAdminBalanceFixture) target(ctx context.Context) (*User, error) {
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return nil, errors.New("financial mutation missing tx")
	}
	if r.pending[tx] == nil {
		user := r.committed
		r.pending[tx] = &user
		tx.OnCommit(func(next dbent.Committer) dbent.Committer {
			return dbent.CommitFunc(func(ctx context.Context, tx *dbent.Tx) error {
				if err := next.Commit(ctx, tx); err != nil {
					return err
				}
				r.committed = *r.pending[tx]
				return nil
			})
		})
	}
	return r.pending[tx], nil
}
func (r *atomicAdminBalanceFixture) GetByID(ctx context.Context, _ int64) (*User, error) {
	u, e := r.target(ctx)
	if e != nil {
		return nil, e
	}
	copy := *u
	return &copy, nil
}
func (r *atomicAdminBalanceFixture) AdjustBalance(ctx context.Context, _ int64, delta float64) (BalanceChange, error) {
	u, e := r.target(ctx)
	if e != nil {
		return BalanceChange{}, e
	}
	change := BalanceChange{Old: u.Balance, New: u.Balance + delta}
	if change.New < 0 {
		return change, ErrBalanceNegative
	}
	u.Balance = change.New
	return change, nil
}
func (r *atomicAdminBalanceFixture) SetBalance(ctx context.Context, _ int64, value float64) (BalanceChange, error) {
	u, e := r.target(ctx)
	if e != nil {
		return BalanceChange{}, e
	}
	change := BalanceChange{Old: u.Balance, New: value}
	u.Balance = value
	return change, nil
}

type atomicAdminAuditFixture struct {
	RedeemCodeRepository
	balance *atomicAdminBalanceFixture
}

func (a *atomicAdminAuditFixture) Create(ctx context.Context, record *RedeemCode) error {
	r := a.balance
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return errors.New("audit missing tx")
	}
	if r.auditErr != nil {
		return r.auditErr
	}
	copy := *record
	tx.OnCommit(func(next dbent.Committer) dbent.Committer {
		return dbent.CommitFunc(func(ctx context.Context, tx *dbent.Tx) error {
			if err := next.Commit(ctx, tx); err != nil {
				return err
			}
			r.audit = append(r.audit, &copy)
			return nil
		})
	})
	return nil
}

type atomicAdminInvalidator struct {
	APIKeyAuthCacheInvalidator
	calls int
}

func (i *atomicAdminInvalidator) InvalidateAuthCacheByUserID(context.Context, int64) { i.calls++ }

func TestAdminBalanceAuditAndMutationCommitTogether(t *testing.T) {
	for _, failure := range []string{"audit", "commit", "none"} {
		t.Run(failure, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			r := &atomicAdminBalanceFixture{committed: User{ID: 7, Balance: 10}, pending: map[*dbent.Tx]*User{}}
			invalidator := &atomicAdminInvalidator{}
			svc := &adminServiceImpl{userRepo: r, redeemCodeRepo: &atomicAdminAuditFixture{balance: r}, entClient: client, authCacheInvalidator: invalidator}
			mock.ExpectBegin()
			if failure == "audit" {
				r.auditErr = errors.New("audit unavailable")
				mock.ExpectRollback()
			} else if failure == "commit" {
				mock.ExpectCommit().WillReturnError(errors.New("commit unavailable"))
			} else {
				mock.ExpectCommit()
			}
			got, err := svc.UpdateUserBalance(context.Background(), 7, 5, "add", "reconciliation")
			if failure != "none" {
				require.Error(t, err)
				require.Equal(t, 10.0, r.committed.Balance)
				require.Empty(t, r.audit)
				require.Zero(t, invalidator.calls)
			} else {
				require.NoError(t, err)
				require.Equal(t, 15.0, got.Balance)
				require.Equal(t, 15.0, r.committed.Balance)
				require.Len(t, r.audit, 1)
				require.Equal(t, 5.0, r.audit[0].Value)
				require.Equal(t, "reconciliation", r.audit[0].Notes)
				require.Equal(t, 1, invalidator.calls)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
func TestAdminBalanceRejectsInvalidAmountBeforeMutation(t *testing.T) {
	svc := &adminServiceImpl{}
	for _, v := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := svc.UpdateUserBalance(context.Background(), 7, v, "add", "")
		require.Error(t, err)
	}
}
