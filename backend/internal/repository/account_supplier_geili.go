package repository

import (
	"context"
	"database/sql"
	"errors"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbpredicate "github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"

	entsql "entgo.io/ent/dialect/sql"
)

type supplierAccountRepositoryGeili struct {
	client *dbent.Client
	db     *sql.DB
}

func NewSupplierAccountRepositoryGeili(client *dbent.Client, db *sql.DB) service.SupplierAccountRepositoryGeili {
	return &supplierAccountRepositoryGeili{client: client, db: db}
}

// applyAccountSupplierOwnerFilterGeili restricts account list queries to one supplier.
func applyAccountSupplierOwnerFilterGeili(ctx context.Context, q *dbent.AccountQuery) {
	ownerID, ok := service.AccountSupplierOwnerFilterGeili(ctx)
	if !ok {
		return
	}
	q.Where(dbpredicate.Account(func(s *entsql.Selector) {
		owners := entsql.Table("account_supplier_owners_geili")
		s.Where(entsql.In(s.C("id"),
			entsql.Select(owners.C("account_id")).From(owners).Where(entsql.EQ(owners.C("owner_user_id"), ownerID)),
		))
	}))
}

func (r *supplierAccountRepositoryGeili) CreateSupplierAccount(ctx context.Context, account *service.Account, ownerID int64) error {
	if account == nil || ownerID <= 0 {
		return service.ErrSupplierAccountInvalid
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txClient := tx.Client()
	// Lock the owner row so a concurrent role change cannot interleave with the create.
	var role string
	rows, err := txClient.QueryContext(ctx, `SELECT role FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, ownerID)
	if err != nil {
		return err
	}
	if rows.Next() {
		err = rows.Scan(&role)
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	if role != service.RoleSupplier {
		return service.ErrSupplierOwnerInvalid
	}
	if err := createAccountRecord(ctx, txClient, account); err != nil {
		return err
	}
	if _, err := txClient.ExecContext(ctx, `INSERT INTO account_supplier_owners_geili(account_id, owner_user_id) VALUES ($1, $2)`, account.ID, ownerID); err != nil {
		return err
	}
	if err := enqueueSchedulerOutbox(ctx, txClient, service.SchedulerOutboxEventAccountChanged, &account.ID, nil, buildSchedulerGroupPayload(nil)); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *supplierAccountRepositoryGeili) SupplierAccountIDs(ctx context.Context, ownerID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT account_id FROM account_supplier_owners_geili WHERE owner_user_id=$1 ORDER BY account_id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *supplierAccountRepositoryGeili) IsLiveSupplierAccount(ctx context.Context, ownerID, accountID int64) (bool, error) {
	var ok bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM account_supplier_owners_geili o JOIN accounts a ON a.id=o.account_id
		WHERE o.owner_user_id=$1 AND o.account_id=$2 AND a.deleted_at IS NULL)`, ownerID, accountID).Scan(&ok)
	return ok, err
}

func (r *supplierAccountRepositoryGeili) SetSupplierOwner(ctx context.Context, accountID, ownerID int64) error {
	if ownerID <= 0 {
		_, err := r.db.ExecContext(ctx, `DELETE FROM account_supplier_owners_geili WHERE account_id=$1`, accountID)
		return err
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO account_supplier_owners_geili(account_id, owner_user_id) VALUES ($1, $2)
		ON CONFLICT (account_id) DO UPDATE SET owner_user_id=EXCLUDED.owner_user_id, created_at=NOW()`, accountID, ownerID)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23503" {
		return service.ErrSupplierOwnerInvalid
	}
	return err
}

func (r *supplierAccountRepositoryGeili) SupplierOwners(ctx context.Context, accountIDs []int64) (map[int64]service.SupplierAccountOwnerGeili, error) {
	out := make(map[int64]service.SupplierAccountOwnerGeili, len(accountIDs))
	if len(accountIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT o.account_id, u.id, u.email FROM account_supplier_owners_geili o
		JOIN users u ON u.id=o.owner_user_id WHERE o.account_id = ANY($1)`, pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var accountID int64
		var owner service.SupplierAccountOwnerGeili
		if err := rows.Scan(&accountID, &owner.UserID, &owner.Email); err != nil {
			return nil, err
		}
		out[accountID] = owner
	}
	return out, rows.Err()
}

// hydrateAccountSupplierOwnersGeili is best-effort administrator presentation only.
func hydrateAccountSupplierOwnersGeili(ctx context.Context, exec sqlExecutor, accounts []*service.Account) {
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if account != nil {
			ids = append(ids, account.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	rows, err := exec.QueryContext(ctx, `SELECT o.account_id, u.id, u.email FROM account_supplier_owners_geili o
		JOIN users u ON u.id=o.owner_user_id WHERE o.account_id = ANY($1)`, pq.Array(ids))
	if err != nil {
		logger.LegacyPrintf("repository.account", "supplier owner display hydration failed: %v", err)
		return
	}
	defer func() { _ = rows.Close() }()
	owners := make(map[int64]*service.SupplierAccountOwnerGeili, len(ids))
	for rows.Next() {
		var accountID int64
		owner := &service.SupplierAccountOwnerGeili{}
		if err := rows.Scan(&accountID, &owner.UserID, &owner.Email); err != nil {
			return
		}
		owners[accountID] = owner
	}
	for _, account := range accounts {
		if account != nil {
			account.SupplierOwner = owners[account.ID]
		}
	}
}
