package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/ent/usersubscriptiongroup"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type userSubscriptionRepository struct {
	client *dbent.Client
}

// GetActiveByUserIDAndEntitledGroupID resolves a shared subscription through
// the Geili entitlement table while keeping the legacy repository contract.
func (r *userSubscriptionRepository) GetActiveByUserIDAndEntitledGroupID(ctx context.Context, userID, groupID int64) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	link, err := client.UserSubscriptionGroup.Query().
		Where(usersubscriptiongroup.GroupIDEQ(groupID), usersubscriptiongroup.HasSubscriptionWith(usersubscription.UserIDEQ(userID), usersubscription.StatusEQ(service.SubscriptionStatusActive), usersubscription.ExpiresAtGT(time.Now()), usersubscription.StartsAtLTE(time.Now()))).
		WithSubscription(func(q *dbent.UserSubscriptionQuery) {
			q.WithGroup().WithPlan().WithEntitlements()
		}).
		Order(usersubscriptiongroup.ByUserSubscriptionID()).
		First(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	sub := link.Edges.Subscription
	if sub == nil || sub.Status != service.SubscriptionStatusActive || !sub.ExpiresAt.After(time.Now()) {
		return nil, service.ErrSubscriptionNotFound
	}
	return r.projectContract(ctx, userSubscriptionEntityToService(sub))
}

func NewUserSubscriptionRepository(client *dbent.Client) service.UserSubscriptionRepository {
	return &userSubscriptionRepository{client: client}
}

func (r *userSubscriptionRepository) Create(ctx context.Context, sub *service.UserSubscription) error {
	if sub == nil {
		return service.ErrSubscriptionNilInput
	}

	client := clientFromContext(ctx, r.client)
	builder := client.UserSubscription.Create().
		SetUserID(sub.UserID).
		SetNillableGroupID(optionalPositiveInt64(sub.GroupID)).
		SetNillablePlanID(sub.PlanID).
		SetExpiresAt(sub.ExpiresAt).
		SetNillableDailyWindowStart(sub.DailyWindowStart).
		SetNillableWeeklyWindowStart(sub.WeeklyWindowStart).
		SetNillableMonthlyWindowStart(sub.MonthlyWindowStart).
		SetDailyUsageUsd(sub.DailyUsageUSD).
		SetWeeklyUsageUsd(sub.WeeklyUsageUSD).
		SetMonthlyUsageUsd(sub.MonthlyUsageUSD).
		SetNillableAssignedBy(sub.AssignedBy)

	if sub.StartsAt.IsZero() {
		builder.SetStartsAt(time.Now())
	} else {
		builder.SetStartsAt(sub.StartsAt)
	}
	if sub.Status != "" {
		builder.SetStatus(sub.Status)
	}
	if !sub.AssignedAt.IsZero() {
		builder.SetAssignedAt(sub.AssignedAt)
	}
	// Keep compatibility with historical behavior: always store notes as a string value.
	builder.SetNotes(sub.Notes)

	created, err := builder.Save(ctx)
	if err == nil {
		// geili hook: every subscription keeps its primary entitlement explicitly;
		// additional bundled groups are added by the migration/plan flow.
		if created.GroupID != nil {
			entitlementErr := client.UserSubscriptionGroup.Create().
				SetUserSubscriptionID(created.ID).
				SetGroupID(*created.GroupID).
				OnConflictColumns(usersubscriptiongroup.FieldUserSubscriptionID, usersubscriptiongroup.FieldGroupID).DoNothing().Exec(ctx)
			// The unified-subscription trigger may have inserted this exact primary
			// entitlement already. Ent's no-op upsert reports sql.ErrNoRows when the
			// conflict path inserts nothing; that is a successful idempotent outcome,
			// not a failed assignment.
			if entitlementErr != nil && !errors.Is(entitlementErr, sql.ErrNoRows) {
				return entitlementErr
			}
		}
		applyUserSubscriptionEntityToService(sub, created)
	}
	return translatePersistenceError(err, nil, service.ErrSubscriptionAlreadyExists)
}

func (r *userSubscriptionRepository) GetByID(ctx context.Context, id int64) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	m, err := client.UserSubscription.Query().
		Where(usersubscription.IDEQ(id)).
		WithUser().
		WithGroup().WithPlan().
		WithGroupEntitlements().
		WithEntitlements().
		WithAssignedByUser().
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return r.projectContract(ctx, userSubscriptionEntityToService(m))
}

func (r *userSubscriptionRepository) GetByIDForUpdate(ctx context.Context, id int64) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	m, err := client.UserSubscription.Query().
		Where(usersubscription.IDEQ(id)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return userSubscriptionEntityToService(m), nil
}

func (r *userSubscriptionRepository) GetByIDIncludeDeleted(ctx context.Context, id int64) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	queryCtx := mixins.SkipSoftDelete(ctx)
	m, err := client.UserSubscription.Query().
		Where(usersubscription.IDEQ(id)).
		WithUser().
		WithGroup().WithPlan().
		WithGroupEntitlements().
		WithEntitlements().WithEntitlementOperations().
		WithAssignedByUser().
		Only(queryCtx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return r.projectContract(ctx, userSubscriptionEntityToServicePreserveStatus(m))
}

func (r *userSubscriptionRepository) GetByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	m, err := client.UserSubscription.Query().
		Where(usersubscription.UserIDEQ(userID), usersubscription.GroupIDEQ(groupID)).
		WithGroup().WithPlan().
		WithEntitlements().WithEntitlementOperations().
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return r.projectContract(ctx, userSubscriptionEntityToService(m))
}

func (r *userSubscriptionRepository) GetActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	m, err := client.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(userID),
			usersubscription.GroupIDEQ(groupID),
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(time.Now()),
		).
		WithGroup().WithPlan().
		WithEntitlements().WithEntitlementOperations().
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return r.projectContract(ctx, userSubscriptionEntityToService(m))
}

func (r *userSubscriptionRepository) Update(ctx context.Context, sub *service.UserSubscription) error {
	if sub == nil {
		return service.ErrSubscriptionNilInput
	}

	client := clientFromContext(ctx, r.client)
	builder := client.UserSubscription.UpdateOneID(sub.ID).
		SetUserID(sub.UserID).
		SetNillableGroupID(optionalPositiveInt64(sub.GroupID)).
		SetNillablePlanID(sub.PlanID).
		SetStartsAt(sub.StartsAt).
		SetExpiresAt(sub.ExpiresAt).
		SetStatus(sub.Status).
		SetNillableDailyWindowStart(sub.DailyWindowStart).
		SetNillableWeeklyWindowStart(sub.WeeklyWindowStart).
		SetNillableMonthlyWindowStart(sub.MonthlyWindowStart).
		SetDailyUsageUsd(sub.DailyUsageUSD).
		SetWeeklyUsageUsd(sub.WeeklyUsageUSD).
		SetMonthlyUsageUsd(sub.MonthlyUsageUSD).
		SetNillableAssignedBy(sub.AssignedBy).
		SetAssignedAt(sub.AssignedAt).
		SetNotes(sub.Notes)

	updated, err := builder.Save(ctx)
	if err == nil {
		applyUserSubscriptionEntityToService(sub, updated)
		return nil
	}
	return translatePersistenceError(err, service.ErrSubscriptionNotFound, service.ErrSubscriptionAlreadyExists)
}

func (r *userSubscriptionRepository) Delete(ctx context.Context, id int64) error {
	err := r.withLotTx(ctx, id, func(tc context.Context, c *dbent.Client, lots []geilisub.Lot) error {
		if err := geilisub.CheckMutable(lots); err != nil {
			return err
		}
		_, err := c.UserSubscription.Delete().Where(usersubscription.IDEQ(id)).Exec(tc)
		if err != nil {
			return err
		}
		return bumpAdminContractRevision(tc, c, id, time.Now())
	})
	if errors.Is(err, service.ErrSubscriptionNotFound) || dbent.IsNotFound(err) {
		return nil
	}
	return err
}

func (r *userSubscriptionRepository) Restore(ctx context.Context, subscriptionID int64, restoredStatus string) (*service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	queryCtx := mixins.SkipSoftDelete(ctx)
	_, err := client.UserSubscription.UpdateOneID(subscriptionID).
		SetStatus(restoredStatus).
		ClearDeletedAt().
		SetUpdatedAt(time.Now()).
		Save(queryCtx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, service.ErrSubscriptionRestoreConflict)
	}
	contract, err := geilisub.LoadContract(ctx, client, subscriptionID)
	if err != nil {
		return nil, err
	}
	if contract != nil {
		active := restoredStatus == service.SubscriptionStatusActive && contract.ExpiresAt.After(time.Now()) && contract.Mode == geilisub.ContractModeV2
		if active {
			if _, err = client.ExecContext(ctx, `UPDATE subscription_contracts SET is_current=FALSE WHERE user_id=$1 AND subscription_id<>$2`, contract.UserID, subscriptionID); err != nil {
				return nil, err
			}
		}
		if _, err = client.ExecContext(ctx, `UPDATE subscription_contracts SET revision=revision+1,is_current=$2,updated_at=$3 WHERE subscription_id=$1`, subscriptionID, active, time.Now()); err != nil {
			return nil, err
		}
	}
	return r.GetByID(ctx, subscriptionID)
}

func (r *userSubscriptionRepository) ListByUserID(ctx context.Context, userID int64) ([]service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	subs, err := client.UserSubscription.Query().
		Where(usersubscription.UserIDEQ(userID)).
		WithGroup().WithPlan().
		WithGroupEntitlements().
		WithEntitlements().WithEntitlementOperations().
		Order(dbent.Desc(usersubscription.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return r.projectContracts(ctx, userSubscriptionEntitiesToService(subs))
}

func (r *userSubscriptionRepository) ListActiveByUserID(ctx context.Context, userID int64) ([]service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	subs, err := client.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(userID),
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(time.Now()),
		).
		WithGroup().WithPlan().
		WithGroupEntitlements().
		// geili hook: active-list consumers need the authoritative quota lots.
		WithEntitlements().
		Order(dbent.Desc(usersubscription.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return r.projectContracts(ctx, userSubscriptionEntitiesToService(subs))
}

func (r *userSubscriptionRepository) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]service.UserSubscription, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	q := client.UserSubscription.Query().Where(usersubscription.GroupIDEQ(groupID))

	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	subs, err := q.
		WithUser().
		WithGroup().WithPlan().
		WithGroupEntitlements().
		WithEntitlements().WithEntitlementOperations().
		Order(dbent.Desc(usersubscription.FieldCreatedAt)).
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}

	result, err := r.projectContracts(ctx, userSubscriptionEntitiesToService(subs))
	if err != nil {
		return nil, nil, err
	}
	return result, paginationResultFromTotal(int64(total), params), nil
}

func (r *userSubscriptionRepository) List(ctx context.Context, params pagination.PaginationParams, userID, groupID *int64, status, platform, sortBy, sortOrder string) ([]service.UserSubscription, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	q := client.UserSubscription.Query()
	includeSoftDeleted := status == "" || status == service.SubscriptionStatusRevoked
	if userID != nil {
		q = q.Where(usersubscription.UserIDEQ(*userID))
	}
	if groupID != nil {
		q = q.Where(usersubscription.GroupIDEQ(*groupID))
	}
	if platform != "" {
		groupPredicates := []predicate.Group{group.PlatformEQ(platform)}
		if includeSoftDeleted {
			groupPredicates = append(groupPredicates, group.DeletedAtIsNil())
		}
		q = q.Where(usersubscription.HasGroupWith(groupPredicates...))
	}

	// Status filtering with real-time expiration check
	now := time.Now()
	switch status {
	case service.SubscriptionStatusActive:
		// Active: status is active AND not yet expired
		q = q.Where(
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(now),
		)
	case service.SubscriptionStatusExpired:
		// Expired: status is expired OR (status is active but already expired)
		q = q.Where(
			usersubscription.Or(
				usersubscription.StatusEQ(service.SubscriptionStatusExpired),
				usersubscription.And(
					usersubscription.StatusEQ(service.SubscriptionStatusActive),
					usersubscription.ExpiresAtLTE(now),
				),
			),
		)
	case service.SubscriptionStatusRevoked:
		// Revoked is a DTO/API display state backed by user_subscriptions.deleted_at.
		q = q.Where(usersubscription.DeletedAtNotNil())
	case "":
		// No filter. Use SkipSoftDelete below so admin "all status" includes revoked history.
	default:
		// Other persisted status.
		q = q.Where(usersubscription.StatusEQ(status))
	}

	queryCtx := ctx
	if includeSoftDeleted {
		queryCtx = mixins.SkipSoftDelete(ctx)
	}

	total, err := q.Clone().Count(queryCtx)
	if err != nil {
		return nil, nil, err
	}

	// geili hook: all-status/revoked admin lists need the same quota projection.
	q = q.WithPlan().WithEntitlements().WithEntitlementOperations()
	if !includeSoftDeleted {
		q = q.WithUser().WithGroup().WithAssignedByUser()
	}

	// Determine sort field
	var field string
	switch sortBy {
	case "expires_at":
		field = usersubscription.FieldExpiresAt
	case "status":
		field = usersubscription.FieldStatus
	default:
		field = usersubscription.FieldCreatedAt
	}

	// Determine sort order (default: desc)
	if sortOrder == "asc" && sortBy != "" {
		q = q.Order(dbent.Asc(field))
	} else {
		q = q.Order(dbent.Desc(field))
	}

	subs, err := q.
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(queryCtx)
	if err != nil {
		return nil, nil, err
	}

	result, err := r.projectContracts(ctx, userSubscriptionEntitiesToService(subs))
	if err != nil {
		return nil, nil, err
	}
	if includeSoftDeleted {
		if err := r.attachUserSubscriptionRelations(ctx, result); err != nil {
			return nil, nil, err
		}
	}

	return result, paginationResultFromTotal(int64(total), params), nil
}

func (r *userSubscriptionRepository) ExistsByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (bool, error) {
	client := clientFromContext(ctx, r.client)
	return client.UserSubscription.Query().
		Where(usersubscription.UserIDEQ(userID), usersubscription.GroupIDEQ(groupID)).
		Exist(ctx)
}

func (r *userSubscriptionRepository) ExistsActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (bool, error) {
	return r.ExistsByUserIDAndGroupID(ctx, userID, groupID)
}

func (r *userSubscriptionRepository) ExtendExpiry(ctx context.Context, subscriptionID int64, newExpiresAt time.Time) error {
	return r.withLotTx(ctx, subscriptionID, func(tc context.Context, c *dbent.Client, lots []geilisub.Lot) error {
		contract, err := geilisub.LoadContract(tc, c, subscriptionID)
		if err != nil {
			return err
		}
		if len(lots) > 0 {
			selected, err := selectContractLots(contract, lots, nil, time.Now())
			if err != nil {
				return err
			}
			for i := range selected {
				selected[i].ExpiresAt = newExpiresAt
				if selected[i].Status == "expired" && newExpiresAt.After(time.Now()) {
					selected[i].Status = "active"
				}
			}
			if err := geilisub.PersistLots(tc, c, selected); err != nil {
				return err
			}
		}
		if err := c.UserSubscription.UpdateOneID(subscriptionID).SetExpiresAt(newExpiresAt).Exec(tc); err != nil {
			return err
		}
		if err := geilisub.RefreshParent(tc, c, subscriptionID, time.Now()); err != nil {
			return err
		}
		return refreshAdminContractExpiry(tc, c, contract, newExpiresAt, time.Now())
	})
}

func (r *userSubscriptionRepository) UpdateStatus(ctx context.Context, subscriptionID int64, status string) error {
	return r.withLotTx(ctx, subscriptionID, func(tc context.Context, c *dbent.Client, lots []geilisub.Lot) error {
		if err := geilisub.CheckMutable(lots); err != nil {
			return err
		}
		contract, err := geilisub.LoadContract(tc, c, subscriptionID)
		if err != nil {
			return err
		}
		if contract != nil && status == service.SubscriptionStatusActive && !contract.ExpiresAt.After(time.Now()) {
			return geilisub.ErrStateConflict
		}
		if err := c.UserSubscription.UpdateOneID(subscriptionID).SetStatus(status).Exec(tc); err != nil {
			return err
		}
		return bumpAdminContractRevision(tc, c, subscriptionID, time.Now())
	})
}

func (r *userSubscriptionRepository) UpdateNotes(ctx context.Context, subscriptionID int64, notes string) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.UpdateOneID(subscriptionID).
		SetNotes(notes).
		Save(ctx)
	return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
}

func (r *userSubscriptionRepository) ActivateWindows(ctx context.Context, id int64, dailyStart, periodicStart time.Time) error {
	client := clientFromContext(ctx, r.client)
	n, err := client.UserSubscription.Update().
		Where(
			usersubscription.IDEQ(id),
			usersubscription.DailyWindowStartIsNil(),
			usersubscription.WeeklyWindowStartIsNil(),
			usersubscription.MonthlyWindowStartIsNil(),
		).
		SetDailyWindowStart(dailyStart).
		SetWeeklyWindowStart(periodicStart).
		SetMonthlyWindowStart(periodicStart).
		Save(ctx)
	return r.translateConditionalWindowReset(ctx, client, id, n, err)
}

func (r *userSubscriptionRepository) ResetUsageWindows(ctx context.Context, id int64, resetDaily, resetWeekly, resetMonthly bool, dailyStart, periodicStart time.Time) error {
	return r.resetLotWindows(ctx, id, resetDaily, resetWeekly, resetMonthly, dailyStart, periodicStart)
}

func (r *userSubscriptionRepository) ResetDailyUsage(ctx context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	client := clientFromContext(ctx, r.client)
	query := client.UserSubscription.Update().Where(usersubscription.IDEQ(id))
	if expectedWindowStart == nil {
		query = query.Where(usersubscription.DailyWindowStartIsNil())
	} else {
		query = query.Where(usersubscription.DailyWindowStartEQ(*expectedWindowStart))
	}
	n, err := query.
		SetDailyUsageUsd(0).
		SetDailyWindowStart(newWindowStart).
		Save(ctx)
	return r.translateConditionalWindowReset(ctx, client, id, n, err)
}

func (r *userSubscriptionRepository) ResetWeeklyUsage(ctx context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	client := clientFromContext(ctx, r.client)
	query := client.UserSubscription.Update().Where(usersubscription.IDEQ(id))
	if expectedWindowStart == nil {
		query = query.Where(usersubscription.WeeklyWindowStartIsNil())
	} else {
		query = query.Where(usersubscription.WeeklyWindowStartEQ(*expectedWindowStart))
	}
	n, err := query.
		SetWeeklyUsageUsd(0).
		SetWeeklyWindowStart(newWindowStart).
		Save(ctx)
	return r.translateConditionalWindowReset(ctx, client, id, n, err)
}

func (r *userSubscriptionRepository) ResetMonthlyUsage(ctx context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	client := clientFromContext(ctx, r.client)
	query := client.UserSubscription.Update().Where(usersubscription.IDEQ(id))
	if expectedWindowStart == nil {
		query = query.Where(usersubscription.MonthlyWindowStartIsNil())
	} else {
		query = query.Where(usersubscription.MonthlyWindowStartEQ(*expectedWindowStart))
	}
	n, err := query.
		SetMonthlyUsageUsd(0).
		SetMonthlyWindowStart(newWindowStart).
		Save(ctx)
	return r.translateConditionalWindowReset(ctx, client, id, n, err)
}

func (r *userSubscriptionRepository) translateConditionalWindowReset(ctx context.Context, client *dbent.Client, id int64, affected int, err error) error {
	if err != nil {
		return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	if affected > 0 {
		return nil
	}

	// A stale reset is an expected no-op: another request already advanced the
	// window. Preserve not-found semantics for callers that target a missing row.
	exists, err := client.UserSubscription.Query().Where(usersubscription.IDEQ(id)).Exist(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	if !exists {
		return service.ErrSubscriptionNotFound
	}
	return nil
}

// IncrementUsage 原子性地累加订阅用量。
// 限额检查已在请求前由 BillingCacheService.CheckBillingEligibility 完成，
// 此处仅负责记录实际消费，确保消费数据的完整性。
func (r *userSubscriptionRepository) IncrementUsage(ctx context.Context, id int64, costUSD float64) error {
	return r.incrementLots(ctx, id, costUSD)
}

func (r *userSubscriptionRepository) BatchUpdateExpiredStatus(ctx context.Context) (int64, error) {
	client := clientFromContext(ctx, r.client)
	n, err := client.UserSubscription.Update().
		Where(
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtLTE(time.Now()),
		).
		SetStatus(service.SubscriptionStatusExpired).
		Save(ctx)
	return int64(n), err
}

// Extra repository helpers (currently used only by integration tests).

func (r *userSubscriptionRepository) ListExpired(ctx context.Context) ([]service.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	subs, err := client.UserSubscription.Query().
		Where(
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtLTE(time.Now()),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return r.projectContracts(ctx, userSubscriptionEntitiesToService(subs))
}

func (r *userSubscriptionRepository) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	client := clientFromContext(ctx, r.client)
	count, err := client.UserSubscription.Query().Where(usersubscription.GroupIDEQ(groupID)).Count(ctx)
	return int64(count), err
}

func (r *userSubscriptionRepository) CountActiveByGroupID(ctx context.Context, groupID int64) (int64, error) {
	client := clientFromContext(ctx, r.client)
	count, err := client.UserSubscription.Query().
		Where(
			usersubscription.GroupIDEQ(groupID),
			usersubscription.StatusEQ(service.SubscriptionStatusActive),
			usersubscription.ExpiresAtGT(time.Now()),
		).
		Count(ctx)
	return int64(count), err
}

func (r *userSubscriptionRepository) DeleteByGroupID(ctx context.Context, groupID int64) (int64, error) {
	client := clientFromContext(ctx, r.client)
	n, err := client.UserSubscription.Delete().Where(usersubscription.GroupIDEQ(groupID)).Exec(ctx)
	return int64(n), err
}

func (r *userSubscriptionRepository) attachUserSubscriptionRelations(ctx context.Context, subs []service.UserSubscription) error {
	if len(subs) == 0 {
		return nil
	}

	userIDs := make([]int64, 0, len(subs))
	groupIDs := make([]int64, 0, len(subs))
	assignedByIDs := make([]int64, 0, len(subs))
	for i := range subs {
		userIDs = append(userIDs, subs[i].UserID)
		groupIDs = append(groupIDs, subs[i].GroupID)
		if subs[i].AssignedBy != nil {
			assignedByIDs = append(assignedByIDs, *subs[i].AssignedBy)
		}
	}

	client := clientFromContext(ctx, r.client)
	users, err := client.User.Query().Where(user.IDIn(uniqueInt64s(userIDs)...)).All(ctx)
	if err != nil {
		return err
	}
	userByID := make(map[int64]*service.User, len(users))
	for _, u := range users {
		userByID[u.ID] = userEntityToService(u)
	}

	groups, err := client.Group.Query().Where(group.IDIn(uniqueInt64s(groupIDs)...)).All(ctx)
	if err != nil {
		return err
	}
	groupByID := make(map[int64]*service.Group, len(groups))
	for _, g := range groups {
		groupByID[g.ID] = groupEntityToService(g)
	}

	assignedByID := map[int64]*service.User{}
	if len(assignedByIDs) > 0 {
		assignedUsers, err := client.User.Query().Where(user.IDIn(uniqueInt64s(assignedByIDs)...)).All(ctx)
		if err != nil {
			return err
		}
		assignedByID = make(map[int64]*service.User, len(assignedUsers))
		for _, u := range assignedUsers {
			assignedByID[u.ID] = userEntityToService(u)
		}
	}

	for i := range subs {
		subs[i].User = userByID[subs[i].UserID]
		subs[i].Group = groupByID[subs[i].GroupID]
		if subs[i].AssignedBy != nil {
			subs[i].AssignedByUser = assignedByID[*subs[i].AssignedBy]
		}
	}
	return nil
}

func uniqueInt64s(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func userSubscriptionEntityToService(m *dbent.UserSubscription) *service.UserSubscription {
	return userSubscriptionEntityToServiceWithStatusMapping(m, true)
}

func userSubscriptionEntityToServicePreserveStatus(m *dbent.UserSubscription) *service.UserSubscription {
	return userSubscriptionEntityToServiceWithStatusMapping(m, false)
}

func userSubscriptionEntityToServiceWithStatusMapping(m *dbent.UserSubscription, mapDeletedToRevoked bool) *service.UserSubscription {
	if m == nil {
		return nil
	}
	status := m.Status
	if mapDeletedToRevoked && m.DeletedAt != nil {
		status = service.SubscriptionStatusRevoked
	}
	out := &service.UserSubscription{
		ID:                 m.ID,
		UserID:             m.UserID,
		GroupID:            derefInt64(m.GroupID),
		PlanID:             m.PlanID,
		StartsAt:           m.StartsAt,
		ExpiresAt:          m.ExpiresAt,
		Status:             status,
		DailyWindowStart:   m.DailyWindowStart,
		WeeklyWindowStart:  m.WeeklyWindowStart,
		MonthlyWindowStart: m.MonthlyWindowStart,
		DailyUsageUSD:      m.DailyUsageUsd,
		WeeklyUsageUSD:     m.WeeklyUsageUsd,
		MonthlyUsageUSD:    m.MonthlyUsageUsd,
		AssignedBy:         m.AssignedBy,
		AssignedAt:         m.AssignedAt,
		Notes:              derefString(m.Notes),
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
		DeletedAt:          m.DeletedAt,
	}
	if m.Edges.Plan != nil {
		p := m.Edges.Plan
		out.Plan = &service.SubscriptionQuotaPlan{ID: p.ID, Name: p.Name, DailyLimitUSD: p.DailyLimitUsd, WeeklyLimitUSD: p.WeeklyLimitUsd, MonthlyLimitUSD: p.MonthlyLimitUsd}
	}
	if m.Edges.User != nil {
		out.User = userEntityToService(m.Edges.User)
	}
	if m.Edges.Group != nil {
		out.Group = groupEntityToService(m.Edges.Group)
	}
	if m.Edges.AssignedByUser != nil {
		out.AssignedByUser = userEntityToService(m.Edges.AssignedByUser)
	}
	for _, entitlement := range m.Edges.GroupEntitlements {
		out.EntitledGroupIDs = append(out.EntitledGroupIDs, entitlement.GroupID)
	}
	for _, entitlement := range m.Edges.Entitlements {
		out.Entitlements = append(out.Entitlements, subscriptionEntitlementEntityToService(entitlement))
	}

	for _, operation := range m.Edges.EntitlementOperations {
		op := service.SubscriptionEntitlementOperation{ID: operation.ID, EntitlementID: operation.EntitlementID, Operation: operation.Operation, SourceType: operation.SourceType, SourceReference: operation.SourceReference, CreatedAt: operation.CreatedAt}
		raw, err := json.Marshal(operation.Detail)
		if err == nil {
			_ = json.Unmarshal(raw, &op)
		}
		out.EntitlementOperations = append(out.EntitlementOperations, op)
	}
	if len(out.EntitledGroupIDs) == 0 && out.GroupID > 0 {
		out.EntitledGroupIDs = []int64{out.GroupID}
	}
	return out
}

func subscriptionEntitlementEntityToService(e *dbent.UserSubscriptionEntitlement) service.SubscriptionEntitlement {
	if e == nil {
		return service.SubscriptionEntitlement{}
	}
	return geilisub.FromEntity(e)
}

func userSubscriptionEntitiesToService(models []*dbent.UserSubscription) []service.UserSubscription {
	out := make([]service.UserSubscription, 0, len(models))
	for i := range models {
		if s := userSubscriptionEntityToService(models[i]); s != nil {
			out = append(out, *s)
		}
	}
	return out
}

func applyUserSubscriptionEntityToService(dst *service.UserSubscription, src *dbent.UserSubscription) {
	if dst == nil || src == nil {
		return
	}
	dst.ID = src.ID
	dst.CreatedAt = src.CreatedAt
	dst.UpdatedAt = src.UpdatedAt
}

func optionalPositiveInt64(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}
func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
