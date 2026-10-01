package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionoperation"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/ent/usersubscriptionentitlement"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	opAdminGrant     = "admin_grant"
	opAdminTerminate = "admin_terminate"
)

var (
	ErrAdminGrantUser     = infraerrors.NotFound("ADMIN_GRANT_USER_NOT_FOUND", "user not found")
	ErrAdminGrantSnapshot = infraerrors.Conflict("ADMIN_GRANT_SNAPSHOT_MISMATCH", "the user's subscription changed after the preview; preview again")
	ErrAdminGrantLot      = infraerrors.NotFound("ADMIN_GRANT_ENTITLEMENT_NOT_FOUND", "entitlement not found in this subscription")
)

// AdminGrantRequest issues an administrator daily entitlement that stacks on
// every live lot in the user's single routing pool. IdempotencyKey comes from
// the Idempotency-Key header, never from the JSON body.
type AdminGrantRequest struct {
	UserID           int64   `json:"user_id"`
	DailyLimitUSD    float64 `json:"daily_limit_usd"`
	Days             int     `json:"days"`
	Reason           string  `json:"reason"`
	ExpectedSnapshot string  `json:"expected_snapshot"`
	IdempotencyKey   string  `json:"-"`
	Apply            bool    `json:"apply"`
}

type AdminGrantLot struct {
	ID            int64     `json:"id"`
	SourceType    string    `json:"source_type"`
	Status        string    `json:"status"`
	StartsAt      time.Time `json:"starts_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	DailyLimitUSD *float64  `json:"daily_limit_usd"`
	PlanID        *int64    `json:"plan_id,omitempty"`
	New           bool      `json:"new"`
}

type AdminGrantResult struct {
	UserID               int64                   `json:"user_id"`
	SubscriptionID       int64                   `json:"subscription_id"`
	EntitlementID        int64                   `json:"entitlement_id"`
	CreatedPool          bool                    `json:"created_pool"`
	ConvertsFromV2       bool                    `json:"converts_from_v2"`
	ContractMode         string                  `json:"contract_mode"`
	CurrentDailyLimitUSD *float64                `json:"current_daily_limit_usd"`
	AfterDailyLimitUSD   *float64                `json:"after_daily_limit_usd"`
	DailyUsageUSD        float64                 `json:"daily_usage_usd"`
	DailyLimitUSD        float64                 `json:"daily_limit_usd"`
	Days                 int                     `json:"days"`
	StartsAt             time.Time               `json:"starts_at"`
	ExpiresAt            time.Time               `json:"expires_at"`
	PoolExpiresAt        time.Time               `json:"pool_expires_at"`
	Timeline             []geilisub.LimitSegment `json:"timeline"`
	Lots                 []AdminGrantLot         `json:"lots"`
	Warnings             []string                `json:"warnings"`
	Snapshot             string                  `json:"snapshot"`
	Applied              bool                    `json:"applied"`
}

// adminGrantTerms identifies one grant for durable idempotent replay.
type adminGrantTerms struct {
	UserID        int64   `json:"user_id"`
	DailyLimitUSD float64 `json:"daily_limit_usd"`
	Days          int     `json:"days"`
	Reason        string  `json:"reason"`
}

// GrantAdminEntitlement previews (apply=false, rolled back) or applies one
// administrator grant. The pool switches to compatibility mode so the router
// sums every live lot: a 45/day gift plus a 360/day grant admits 405/day until
// the gift expires, then 360/day. Self-service V2 changes pause meanwhile.
func (s *SubscriptionService) GrantAdminEntitlement(ctx context.Context, actor int64, req AdminGrantRequest) (*AdminGrantResult, error) {
	if s.entClient == nil {
		return nil, geilisub.ErrStateConflict
	}
	now := time.Now().Truncate(time.Microsecond)
	daily, reason, err := geilisub.ValidateGrant(req.DailyLimitUSD, req.Days, req.Reason, now)
	if err != nil {
		return nil, err
	}
	if req.UserID <= 0 {
		return nil, ErrAdminGrantUser
	}
	if req.Apply && !geilisub.ValidGrantKey(req.IdempotencyKey) {
		return nil, geilisub.ErrAdminGrantKey
	}
	terms := adminGrantTerms{UserID: req.UserID, DailyLimitUSD: daily, Days: req.Days, Reason: reason}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	c := tx.Client()
	owner, err := c.User.Query().Unique(false).Where(user.IDEQ(req.UserID), geilisub.LockRows).Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, ErrAdminGrantUser
	}
	if err != nil {
		return nil, err
	}
	if req.Apply {
		if saved, err := replayAdminGrant(ctx, c, req.IdempotencyKey, terms); saved != nil || err != nil {
			return saved, err
		}
	}
	pools, err := c.UserSubscription.Query().Where(usersubscription.UserIDEQ(req.UserID), usersubscription.DeletedAtIsNil(), usersubscription.ExpiresAtGT(now), usersubscription.StatusIn(SubscriptionStatusActive, SubscriptionStatusSuspended)).Order(usersubscription.ByID()).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(pools) > 1 {
		return nil, geilisub.ErrAdminGrantMultiplePools
	}
	result := &AdminGrantResult{UserID: req.UserID, DailyLimitUSD: daily, Days: req.Days, StartsAt: now, ExpiresAt: now.AddDate(0, 0, req.Days), ContractMode: geilisub.ContractModeLegacy}
	var parent *dbent.UserSubscription
	var current *geilisub.Contract
	var lots []geilisub.Lot
	if len(pools) == 1 {
		if parent, err = geilisub.LockParent(ctx, c, pools[0].ID); err != nil {
			return nil, err
		}
		if parent.DeletedAt != nil || !parent.ExpiresAt.After(now) || parent.StartsAt.After(now) {
			return nil, geilisub.ErrStateConflict
		}
		if parent.Status == SubscriptionStatusSuspended {
			return nil, geilisub.ErrAdminGrantPoolSuspended
		}
		if current, err = geilisub.LoadContract(ctx, c, parent.ID); err != nil {
			return nil, err
		}
		if lots, err = geilisub.ReadLots(ctx, c, parent.ID); err != nil {
			return nil, err
		}
		for _, l := range lots {
			if l.Status == "refund_pending" {
				return nil, geilisub.ErrAdminGrantRefundPending
			}
		}
		result.SubscriptionID = parent.ID
	} else {
		result.CreatedPool = true
	}
	// The snapshot is taken from persisted state before EnsureContract may seed
	// a baseline, so a preview and its apply always hash the same rows. Usage
	// counters are excluded: traffic must not invalidate a reviewed preview.
	result.Snapshot = adminGrantSnapshot(parent, current, lots, terms)
	used := 0.0
	if parent != nil {
		// A legacy pool without a contract gets its parent-derived lot here,
		// before the grant lot exists; otherwise that right would be skipped.
		if current, err = geilisub.EnsureContract(ctx, c, parent.ID, now); err != nil {
			return nil, err
		}
		if lots, err = geilisub.ReadLots(ctx, c, parent.ID); err != nil {
			return nil, err
		}
		if used, err = geilisub.ReadDailyUsage(ctx, c, parent.ID, current.TermID, now); err != nil {
			return nil, err
		}
		before := geilisub.ContractSummary(current, lots, used, now)
		result.CurrentDailyLimitUSD = before.DailyLimitUSD
		result.ConvertsFromV2 = current.Mode == geilisub.ContractModeV2 && current.Active(now)
	} else {
		zero := 0.0
		result.CurrentDailyLimitUSD = &zero
	}
	day := geilisub.DayStart(now)
	grant := geilisub.Lot{SourceType: geilisub.SourceAdminGrant, PurchaseMode: geilisub.SourceAdminGrant, Status: "active", StartsAt: now, ExpiresAt: result.ExpiresAt, DailyLimitUSD: &daily, DailyWindowStart: &day}
	projected := append(append([]geilisub.Lot(nil), lots...), grant)
	after := geilisub.Contract{Mode: geilisub.ContractModeLegacy, Status: "active", StartsAt: now, ExpiresAt: result.ExpiresAt}
	if current != nil {
		after = *current
		after.Mode = geilisub.ContractModeLegacy
	}
	afterSummary := geilisub.ContractSummary(&after, projected, used, now)
	result.AfterDailyLimitUSD = afterSummary.DailyLimitUSD
	result.DailyUsageUSD = afterSummary.DailyUsageUSD
	result.PoolExpiresAt = result.ExpiresAt
	if afterSummary.ExpiresAt != nil && afterSummary.ExpiresAt.After(result.PoolExpiresAt) {
		result.PoolExpiresAt = *afterSummary.ExpiresAt
	}
	result.Timeline = geilisub.GrantTimeline(projected, now)
	result.Lots = adminGrantLots(projected, now)
	result.Warnings = adminGrantWarnings(owner, result, daily)
	if !req.Apply {
		return result, nil
	}
	if req.ExpectedSnapshot != result.Snapshot {
		return nil, ErrAdminGrantSnapshot
	}
	// Paid/in-flight order decisions must not race an administrative change.
	if err = checkSubscriptionPending(ctx, c, req.UserID); err != nil {
		return nil, err
	}
	if parent == nil {
		create := c.UserSubscription.Create().SetUserID(req.UserID).SetStartsAt(now).SetExpiresAt(result.ExpiresAt).SetStatus(SubscriptionStatusActive).SetAssignedAt(now).SetNotes("admin_grant:" + req.IdempotencyKey)
		if actor > 0 {
			create.SetAssignedBy(actor)
		}
		if parent, err = create.Save(ctx); err != nil {
			return nil, err
		}
		result.SubscriptionID = parent.ID
	}
	index := 0
	for _, l := range lots {
		index = max(index, l.LotIndex+1)
	}
	lot, err := c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(parent.ID).SetLotIndex(index).SetSourceType(geilisub.SourceAdminGrant).SetSourceReference(req.IdempotencyKey).SetPurchaseMode(geilisub.SourceAdminGrant).SetStatus("active").SetStartsAt(now).SetExpiresAt(result.ExpiresAt).SetDailyLimitUsd(daily).SetDailyWindowStart(day).Save(ctx)
	if err != nil {
		return nil, err
	}
	result.EntitlementID = lot.ID
	for i := range result.Lots {
		if result.Lots[i].New {
			result.Lots[i].ID = lot.ID
		}
	}
	contract, err := geilisub.MarkLegacyContract(ctx, c, parent.ID, now)
	if err != nil {
		return nil, err
	}
	if err = geilisub.RefreshParent(ctx, c, parent.ID, now); err != nil {
		return nil, err
	}
	// The enforced quota must equal what the operator approved.
	if lots, err = geilisub.ReadLots(ctx, c, parent.ID); err != nil {
		return nil, err
	}
	if used, err = geilisub.ReadDailyUsage(ctx, c, parent.ID, contract.TermID, now); err != nil {
		return nil, err
	}
	if actual := geilisub.ContractSummary(contract, lots, used, now); !sameDailyLimit(actual.DailyLimitUSD, result.AfterDailyLimitUSD) {
		return nil, geilisub.ErrStateConflict
	}
	result.Applied = true
	if err = geilisub.RecordOperation(ctx, c, parent.ID, lot.ID, opAdminGrant, "admin", req.IdempotencyKey, actor, map[string]any{"reason": reason, "request": terms, "result": result}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if err = s.invalidateSubscriptionCaches(req.UserID, 0, parent.ID); err != nil {
		return result, err
	}
	return result, nil
}

func replayAdminGrant(ctx context.Context, c *dbent.Client, key string, terms adminGrantTerms) (*AdminGrantResult, error) {
	record, err := c.SubscriptionOperation.Query().Where(subscriptionoperation.OperationEQ(opAdminGrant), subscriptionoperation.SourceReferenceEQ(key)).Order(subscriptionoperation.ByID()).First(ctx)
	if dbent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(record.Detail)
	var saved struct {
		Request adminGrantTerms  `json:"request"`
		Result  AdminGrantResult `json:"result"`
	}
	if json.Unmarshal(raw, &saved) != nil || saved.Request != terms {
		return nil, geilisub.ErrAdminGrantKeyReused
	}
	saved.Result.Applied = true
	return &saved.Result, nil
}

func adminGrantSnapshot(parent *dbent.UserSubscription, current *geilisub.Contract, lots []geilisub.Lot, terms adminGrantTerms) string {
	type pool struct {
		ID        int64
		Status    string
		GroupID   *int64
		PlanID    *int64
		StartsAt  time.Time
		ExpiresAt time.Time
	}
	type right struct {
		ID        int64
		Source    string
		Status    string
		PlanID    *int64
		StartsAt  time.Time
		ExpiresAt time.Time
		Daily     *float64
		Weekly    *float64
		Monthly   *float64
	}
	var p *pool
	if parent != nil {
		p = &pool{parent.ID, parent.Status, parent.GroupID, parent.PlanID, parent.StartsAt.UTC(), parent.ExpiresAt.UTC()}
	}
	var contract *geilisub.Contract
	if current != nil {
		copied := *current
		copied.StartsAt, copied.ExpiresAt = copied.StartsAt.UTC(), copied.ExpiresAt.UTC()
		contract = &copied
	}
	rights := make([]right, 0, len(lots))
	for _, l := range lots {
		rights = append(rights, right{l.ID, l.SourceType, l.Status, l.PlanID, l.StartsAt.UTC(), l.ExpiresAt.UTC(), l.DailyLimitUSD, l.WeeklyLimitUSD, l.MonthlyLimitUSD})
	}
	sort.Slice(rights, func(i, j int) bool { return rights[i].ID < rights[j].ID })
	raw, _ := json.Marshal(struct {
		Pool     *pool
		Contract *geilisub.Contract
		Lots     []right
		Terms    adminGrantTerms
	}{p, contract, rights, terms})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

// adminGrantLots lists the rights that still matter: live or future lots.
func adminGrantLots(lots []geilisub.Lot, now time.Time) []AdminGrantLot {
	out := []AdminGrantLot{}
	for _, l := range lots {
		if l.Status == "refunded" || l.Status == "revoked" || !l.ExpiresAt.After(now) {
			continue
		}
		out = append(out, AdminGrantLot{ID: l.ID, SourceType: l.SourceType, Status: l.Status, StartsAt: l.StartsAt, ExpiresAt: l.ExpiresAt, DailyLimitUSD: l.DailyLimitUSD, PlanID: l.PlanID, New: l.ID == 0})
	}
	return out
}

func adminGrantWarnings(owner *dbent.User, r *AdminGrantResult, daily float64) []string {
	out := []string{"self_service_paused"}
	if r.CreatedPool {
		out = append(out, "creates_pool")
	}
	if r.ConvertsFromV2 {
		out = append(out, "converts_from_v2")
		// Conversion must preserve every paid right; flag any difference.
		if r.CurrentDailyLimitUSD != nil && r.AfterDailyLimitUSD != nil && !sameDailyLimit(r.AfterDailyLimitUSD, roundedSum(*r.CurrentDailyLimitUSD, daily)) {
			out = append(out, "conversion_changes_quota")
		}
	}
	if r.CurrentDailyLimitUSD == nil {
		out = append(out, "already_unlimited")
	}
	if owner.Status != StatusActive {
		out = append(out, "user_not_active")
	}
	return out
}

func roundedSum(a, b float64) *float64 {
	v := float64(int64((a+b)*100+0.5)) / 100
	return &v
}

func sameDailyLimit(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	d := *a - *b
	return d < 0.000001 && d > -0.000001
}

// AdminGrantTermination reports the pool's quota right after an early end.
type AdminGrantTermination struct {
	SubscriptionID int64      `json:"subscription_id"`
	EntitlementID  int64      `json:"entitlement_id"`
	TerminatedAt   time.Time  `json:"terminated_at"`
	DailyLimitUSD  *float64   `json:"daily_limit_usd"`
	PoolExpiresAt  *time.Time `json:"pool_expires_at"`
	Applied        bool       `json:"applied"`
}

// TerminateAdminGrant ends one live administrator grant now. It expires rather
// than revokes the lot, so the usage it carried today retires with it and the
// remaining rights are not charged for it. Paid and campaign lots are refused.
func (s *SubscriptionService) TerminateAdminGrant(ctx context.Context, actor, sid, eid int64, reason, key string) (*AdminGrantTermination, error) {
	if s.entClient == nil {
		return nil, geilisub.ErrStateConflict
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > geilisub.AdminGrantMaxReason {
		return nil, geilisub.ErrAdminGrantReason
	}
	if !geilisub.ValidGrantKey(key) {
		return nil, geilisub.ErrAdminGrantKey
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	c := tx.Client()
	pool, err := c.UserSubscription.Get(ctx, sid)
	if dbent.IsNotFound(err) {
		return nil, ErrSubscriptionNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err = c.User.Query().Unique(false).Where(user.IDEQ(pool.UserID), geilisub.LockRows).Only(ctx); err != nil {
		return nil, err
	}
	parent, err := geilisub.LockParent(ctx, c, sid)
	if err != nil {
		return nil, err
	}
	record, err := c.SubscriptionOperation.Query().Where(subscriptionoperation.SubscriptionIDEQ(sid), subscriptionoperation.OperationEQ(opAdminTerminate), subscriptionoperation.SourceReferenceEQ(key)).First(ctx)
	if err == nil {
		raw, _ := json.Marshal(record.Detail)
		var saved struct {
			Reason string                `json:"reason"`
			Result AdminGrantTermination `json:"result"`
		}
		if json.Unmarshal(raw, &saved) != nil || saved.Result.EntitlementID != eid || saved.Reason != reason {
			return nil, geilisub.ErrAdminGrantKeyReused
		}
		saved.Result.Applied = true
		return &saved.Result, nil
	}
	if !dbent.IsNotFound(err) {
		return nil, err
	}
	if parent.DeletedAt != nil {
		return nil, geilisub.ErrStateConflict
	}
	lot, err := c.UserSubscriptionEntitlement.Query().Where(usersubscriptionentitlement.IDEQ(eid), usersubscriptionentitlement.UserSubscriptionIDEQ(sid)).Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, ErrAdminGrantLot
	}
	if err != nil {
		return nil, err
	}
	now := time.Now().Truncate(time.Microsecond)
	if lot.SourceType != geilisub.SourceAdminGrant || !geilisub.FromEntity(lot).Active(now) {
		return nil, geilisub.ErrAdminGrantNotTerminable
	}
	if err = c.UserSubscriptionEntitlement.UpdateOneID(eid).SetExpiresAt(now).Exec(ctx); err != nil {
		return nil, err
	}
	contract, err := geilisub.MarkLegacyContract(ctx, c, sid, now)
	if err != nil {
		return nil, err
	}
	if err = geilisub.RefreshParent(ctx, c, sid, now); err != nil {
		return nil, err
	}
	lots, err := geilisub.ReadLots(ctx, c, sid)
	if err != nil {
		return nil, err
	}
	used, err := geilisub.ReadDailyUsage(ctx, c, sid, contract.TermID, now)
	if err != nil {
		return nil, err
	}
	summary := geilisub.ContractSummary(contract, lots, used, now)
	result := &AdminGrantTermination{SubscriptionID: sid, EntitlementID: eid, TerminatedAt: now, DailyLimitUSD: summary.DailyLimitUSD, PoolExpiresAt: summary.ExpiresAt, Applied: true}
	if err = geilisub.RecordOperation(ctx, c, sid, eid, opAdminTerminate, "admin", key, actor, map[string]any{"reason": reason, "result": result, "before_expires_at": lot.ExpiresAt, "after_expires_at": now}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if err = s.invalidateSubscriptionCaches(pool.UserID, 0, sid); err != nil {
		return result, err
	}
	return result, nil
}

// AdminEntitlementOperation is one audited right change shown to operators.
type AdminEntitlementOperation struct {
	ID              int64          `json:"id"`
	EntitlementID   int64          `json:"entitlement_id"`
	Operation       string         `json:"operation"`
	SourceType      string         `json:"source_type"`
	SourceReference string         `json:"source_reference"`
	ActorID         int64          `json:"actor_id"`
	ActorEmail      string         `json:"actor_email,omitempty"`
	Reason          string         `json:"reason,omitempty"`
	Detail          map[string]any `json:"detail,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

// AdminEntitlementView is the read-only per-lot view of one subscription pool.
type AdminEntitlementView struct {
	SubscriptionID int64                       `json:"subscription_id"`
	UserID         int64                       `json:"user_id"`
	Contract       *geilisub.Contract          `json:"contract"`
	Summary        geilisub.Summary            `json:"summary"`
	Timeline       []geilisub.LimitSegment     `json:"timeline"`
	Lots           []geilisub.Lot              `json:"lots"`
	Operations     []AdminEntitlementOperation `json:"operations"`
}

const adminEntitlementOperationLimit = 200

// AdminEntitlements reads lots, quota projection and the newest audit records
// without seeding a contract, so opening the dialog never changes state.
func (s *SubscriptionService) AdminEntitlements(ctx context.Context, sid int64) (*AdminEntitlementView, error) {
	if s.entClient == nil {
		return nil, geilisub.ErrStateConflict
	}
	c := s.entClient
	pool, err := c.UserSubscription.Get(ctx, sid)
	if dbent.IsNotFound(err) {
		return nil, ErrSubscriptionNotFound
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := &AdminEntitlementView{SubscriptionID: sid, UserID: pool.UserID, Operations: []AdminEntitlementOperation{}}
	if out.Lots, err = geilisub.ReadLots(ctx, c, sid); err != nil {
		return nil, err
	}
	if out.Contract, err = geilisub.LoadContract(ctx, c, sid); err != nil {
		return nil, err
	}
	used := 0.0
	if out.Contract != nil {
		if used, err = geilisub.ReadDailyUsage(ctx, c, sid, out.Contract.TermID, now); err != nil {
			return nil, err
		}
	}
	out.Summary = geilisub.ContractSummary(out.Contract, out.Lots, used, now)
	out.Timeline = geilisub.GrantTimeline(out.Lots, now)
	records, err := c.SubscriptionOperation.Query().Where(subscriptionoperation.SubscriptionIDEQ(sid)).Order(subscriptionoperation.ByID(entsql.OrderDesc())).Limit(adminEntitlementOperationLimit).All(ctx)
	if err != nil {
		return nil, err
	}
	actors := map[int64]string{}
	for _, r := range records {
		if r.ActorID > 0 {
			actors[r.ActorID] = ""
		}
	}
	if len(actors) > 0 {
		ids := make([]int64, 0, len(actors))
		for id := range actors {
			ids = append(ids, id)
		}
		users, err := c.User.Query().Where(user.IDIn(ids...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			actors[u.ID] = u.Email
		}
	}
	for _, r := range records {
		reason, _ := r.Detail["reason"].(string)
		out.Operations = append(out.Operations, AdminEntitlementOperation{ID: r.ID, EntitlementID: r.EntitlementID, Operation: r.Operation, SourceType: r.SourceType, SourceReference: r.SourceReference, ActorID: r.ActorID, ActorEmail: actors[r.ActorID], Reason: reason, Detail: r.Detail, CreatedAt: r.CreatedAt})
	}
	return out, nil
}
