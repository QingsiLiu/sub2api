package repository

import (
	"context"

	dbuser "github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// GetBillingBalanceGeili retains the normal transaction client and soft-delete
// interceptor. Every call reads SQL; balances are neither cached nor coalesced.
func (r *userRepository) GetBillingBalanceGeili(ctx context.Context, id int64) (float64, error) {
	m, err := clientFromContext(ctx, r.client).User.Query().
		Where(dbuser.IDEQ(id)).Select(dbuser.FieldBalance).Only(ctx)
	if err != nil {
		return 0, translatePersistenceError(err, service.ErrUserNotFound, nil)
	}
	return m.Balance, nil
}
