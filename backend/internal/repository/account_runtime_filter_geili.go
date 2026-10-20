package repository

import (
	"context"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbpredicate "github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Availability filters are explicit administrator queries. Normal account and
// scheduler reads never call this projection or load runtime metadata for it.
func applyAccountRuntimeAvailabilityFilterGeili(ctx context.Context, q *dbent.AccountQuery) error {
	filter := service.AccountAvailabilityFilterGeili(ctx)
	if filter == "balance_stopped" {
		q.Where(dbpredicate.Account(func(s *entsql.Selector) {
			s.Where(entsql.ExprP(`EXISTS (SELECT 1 FROM account_runtime_geili r WHERE r.account_id=` + s.C("id") + ` AND r.balance_blocked)`))
		}))
		return nil
	}
	if filter != "temporarily_stopped" {
		return nil
	}
	// Reuse the quota domain policy, including rolling/fixed windows, local reset
	// timezones and permissive legacy JSON values. Project only these three fields;
	// never fetch credentials, proxies, account groups or runtime display state.
	quotaAccounts, err := q.Clone().Where(
		dbaccount.StatusEQ(service.StatusActive),
		dbaccount.SchedulableEQ(true),
		dbaccount.TypeIn(service.AccountTypeAPIKey, service.AccountTypeBedrock),
	).Select(dbaccount.FieldID, dbaccount.FieldType, dbaccount.FieldExtra).All(ctx)
	if err != nil {
		return err
	}
	quotaIDs := accountQuotaExceededIDsGeili(quotaAccounts)
	now := time.Now()
	q.Where(dbpredicate.Account(func(s *entsql.Selector) {
		conditions := []*entsql.Predicate{
			entsql.GT(s.C(dbaccount.FieldRateLimitResetAt), now),
			entsql.GT(s.C(dbaccount.FieldOverloadUntil), now),
			entsql.GT(s.C(dbaccount.FieldTempUnschedulableUntil), now),
			entsql.And(entsql.EQ(s.C(dbaccount.FieldAutoPauseOnExpired), true), entsql.LTE(s.C(dbaccount.FieldExpiresAt), now)),
		}
		if len(quotaIDs) > 0 {
			conditions = append(conditions, entsql.P(func(b *entsql.Builder) {
				b.Ident(s.C(dbaccount.FieldID)).WriteString(" = ANY(").Arg(pq.Array(quotaIDs)).WriteByte(')')
			}))
		}
		s.Where(entsql.And(
			entsql.ExprP(`NOT EXISTS (SELECT 1 FROM account_runtime_geili r WHERE r.account_id=`+s.C("id")+` AND r.stop_reason='manual')`),
			entsql.Or(
				entsql.EQ(s.C(dbaccount.FieldStatus), service.StatusError),
				entsql.And(entsql.EQ(s.C(dbaccount.FieldStatus), service.StatusActive), entsql.EQ(s.C(dbaccount.FieldSchedulable), true), entsql.Or(conditions...)),
			),
		))
	}))
	return nil
}

func accountQuotaExceededIDsGeili(accounts []*dbent.Account) []int64 {
	var ids []int64
	for _, row := range accounts {
		account := &service.Account{Type: row.Type, Extra: row.Extra}
		if account.IsAPIKeyOrBedrock() && account.IsQuotaExceeded() {
			ids = append(ids, row.ID)
		}
	}
	return ids
}
