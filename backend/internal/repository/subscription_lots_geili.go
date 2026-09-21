package repository

import (
	"context"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *userSubscriptionRepository) withLotTx(ctx context.Context, id int64, fn func(context.Context, *dbent.Client, []geilisub.Lot) error) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		c := tx.Client()
		if _, err := geilisub.LockParent(ctx, c, id); err != nil {
			return err
		}
		lots, err := geilisub.ReadLots(ctx, c, id)
		if err != nil {
			return err
		}
		return fn(ctx, c, lots)
	}
	if r.client.InTransaction() {
		if _, err := geilisub.LockParent(ctx, r.client, id); err != nil {
			return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
		}
		lots, err := geilisub.ReadLots(ctx, r.client, id)
		if err != nil {
			return err
		}
		return fn(ctx, r.client, lots)
	}

	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	tc := dbent.NewTxContext(ctx, tx)
	if _, err := geilisub.LockParent(tc, tx.Client(), id); err != nil {
		return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	lots, err := geilisub.ReadLots(tc, tx.Client(), id)
	if err != nil {
		return err
	}
	if err := fn(tc, tx.Client(), lots); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *userSubscriptionRepository) AdjustEntitlements(ctx context.Context, id int64, ids []int64, days int, actor int64) error {
	return r.withLotTx(ctx, id, func(tc context.Context, c *dbent.Client, lots []geilisub.Lot) error {
		now := time.Now()
		if len(lots) == 0 {
			var err error
			lots, err = geilisub.EnsureLegacyLot(tc, c, id)
			if err != nil {
				return err
			}
		}
		contract, err := geilisub.LoadContract(tc, c, id)
		if err != nil {
			return err
		}
		chosen, err := selectContractLots(contract, lots, ids, now)
		if err != nil {
			return err
		}
		for _, e := range chosen {
			before := e.ExpiresAt
			if e.ExpiresAt.Before(now) {
				if days < 0 {
					return geilisub.ErrStateConflict
				}
				e.ExpiresAt = now
			}
			e.ExpiresAt = e.ExpiresAt.AddDate(0, 0, days)
			if e.ExpiresAt.After(geilisub.MaxExpiry) {
				e.ExpiresAt = geilisub.MaxExpiry
			}
			if !e.ExpiresAt.After(now) {
				return geilisub.ErrStateConflict
			}
			e.Status = "active"
			if err := geilisub.PersistLots(tc, c, []geilisub.Lot{e}); err != nil {
				return err
			}
			if err := geilisub.RecordOperation(tc, c, id, e.ID, "adjust", "admin", "", actor, map[string]any{"before_expires_at": before, "after_expires_at": e.ExpiresAt}); err != nil {
				return err
			}
		}
		if err := geilisub.RefreshParent(tc, c, id, now); err != nil {
			return err
		}
		parent, err := c.UserSubscription.Get(tc, id)
		if err != nil {
			return err
		}
		return refreshAdminContractExpiry(tc, c, contract, parent.ExpiresAt, now)
	})
}
func (r *userSubscriptionRepository) incrementLots(ctx context.Context, id int64, cost float64) error {
	return r.withLotTx(ctx, id, func(tc context.Context, c *dbent.Client, lots []geilisub.Lot) error {
		contract, err := geilisub.LoadContract(tc, c, id)
		if err != nil {
			return err
		}
		if contract != nil {
			return geilisub.ErrAdmissionRequired
		}

		if len(lots) == 0 {
			parent, err := c.UserSubscription.Get(tc, id)
			if err != nil {
				return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
			}
			if parent.PlanID == nil && parent.GroupID != nil {
				if _, err := c.Group.Get(tc, *parent.GroupID); err != nil {
					return service.ErrSubscriptionNotFound
				}
			}
			return c.UserSubscription.UpdateOneID(id).AddDailyUsageUsd(cost).AddWeeklyUsageUsd(cost).AddMonthlyUsageUsd(cost).Exec(tc)
		}

		lots, err = geilisub.Allocate(lots, cost, time.Now())
		if err != nil {
			return err
		}
		if err := geilisub.PersistLots(tc, c, lots); err != nil {
			return err
		}
		return geilisub.RefreshParent(tc, c, id, time.Now())
	})
}
func (r *userSubscriptionRepository) resetLotWindows(ctx context.Context, id int64, daily, weekly, monthly bool, day, period time.Time) error {
	return r.withLotTx(ctx, id, func(tc context.Context, c *dbent.Client, lots []geilisub.Lot) error {
		if err := geilisub.CheckMutable(lots); err != nil {
			return err
		}
		contract, err := geilisub.LoadContract(tc, c, id)
		if err != nil {
			return err
		}
		if contract != nil {
			weekly, monthly = false, false
			if daily {
				// The actual reset happens after the parent lock; a wait may have
				// crossed Beijing midnight since the administrator clicked reset.
				now := time.Now()
				if err := resetAdminDailyLedger(tc, c, contract, lots, now); err != nil {
					return err
				}
				day = geilisub.DayStart(now)
			}
		}
		for i := range lots {
			e := &lots[i]
			if !e.Active(time.Now()) {
				continue
			}
			if daily {
				e.DailyUsageUSD = 0
				t := timezone.StartOfDay(day)
				if contract != nil {
					t = geilisub.DayStart(day)
				}
				e.DailyWindowStart = &t
			}
			if weekly {
				e.WeeklyUsageUSD = 0
				t := period
				e.WeeklyWindowStart = &t
			}
			if monthly {
				e.MonthlyUsageUSD = 0
				t := period
				e.MonthlyWindowStart = &t
			}
		}
		if err := geilisub.PersistLots(tc, c, lots); err != nil {
			return err
		}
		u := c.UserSubscription.UpdateOneID(id)
		if daily {
			u.SetDailyUsageUsd(0).SetDailyWindowStart(day)
		}
		if weekly {
			u.SetWeeklyUsageUsd(0).SetWeeklyWindowStart(period)
		}
		if monthly {
			u.SetMonthlyUsageUsd(0).SetMonthlyWindowStart(period)
		}
		if err := u.Exec(tc); err != nil {
			return err
		}
		return geilisub.RefreshParent(tc, c, id, time.Now())
	})
}
