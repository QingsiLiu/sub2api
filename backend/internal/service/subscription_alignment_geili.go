package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/subscriptionoperation"
	"github.com/Wei-Shaw/sub2api/ent/user"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
)

type LegacyAlignmentRequest struct {
	UserID           int64   `json:"user_id"`
	EntitlementIDs   []int64 `json:"entitlement_ids"`
	ExpectedSnapshot string  `json:"expected_snapshot"`
	IdempotencyKey   string  `json:"idempotency_key"`
	Reason           string  `json:"reason"`
	Apply            bool    `json:"apply"`
}
type LegacyAlignmentResult struct {
	SubscriptionID int64                 `json:"subscription_id"`
	Snapshot       string                `json:"snapshot"`
	ExpiresAt      time.Time             `json:"expires_at"`
	Before         []geilisub.LegacyLine `json:"changes"`
	Applied        bool                  `json:"applied"`
}

// AlignLegacyEntitlements never accepts an arbitrary future expiry. It extends
// selected live paid siblings to their existing maximum, once per operation key.
// Callers must persist the preview as a backup before sending apply=true.
func (s *SubscriptionService) AlignLegacyEntitlements(ctx context.Context, sid, actor int64, req LegacyAlignmentRequest) (*LegacyAlignmentResult, error) {
	if s.entClient == nil || req.UserID <= 0 || len(req.EntitlementIDs) < 2 || len(req.EntitlementIDs) > 100 || len(req.IdempotencyKey) < 8 || len(req.IdempotencyKey) > 128 || strings.TrimSpace(req.Reason) == "" {
		return nil, geilisub.ErrSelection
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c := tx.Client()
	if _, err = c.User.Query().Unique(false).Where(user.IDEQ(req.UserID), geilisub.LockRows).Only(ctx); err != nil {
		return nil, err
	}
	parent, err := geilisub.LockParent(ctx, c, sid)
	if err != nil {
		return nil, err
	}
	if parent.UserID != req.UserID || parent.DeletedAt != nil {
		return nil, geilisub.ErrStateConflict
	}
	records, err := c.SubscriptionOperation.Query().Where(subscriptionoperation.SubscriptionIDEQ(sid), subscriptionoperation.OperationEQ("align_free"), subscriptionoperation.SourceReferenceEQ(req.IdempotencyKey)).All(ctx)
	if err != nil {
		return nil, err
	}
	ids := append([]int64(nil), req.EntitlementIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for i, id := range ids {
		if id <= 0 || i > 0 && ids[i-1] == id {
			return nil, geilisub.ErrSelection
		}
	}
	if len(records) > 0 {
		raw, _ := json.Marshal(records[0].Detail)
		var saved struct {
			Result LegacyAlignmentResult `json:"result"`
			IDs    []int64               `json:"ids"`
			Reason string                `json:"reason"`
		}
		if json.Unmarshal(raw, &saved) != nil || len(saved.IDs) != len(ids) || saved.Reason != req.Reason {
			return nil, geilisub.ErrStateConflict
		}
		for i, id := range ids {
			if id != saved.IDs[i] {
				return nil, geilisub.ErrStateConflict
			}
		}
		saved.Result.Applied = true
		return &saved.Result, nil
	}
	current, err := geilisub.LoadContract(ctx, c, sid)
	if err != nil {
		return nil, err
	}
	now := time.Now().Truncate(time.Microsecond)
	if current == nil || current.Mode != geilisub.ContractModeLegacy || !current.Active(now) {
		return nil, geilisub.ErrStateConflict
	}
	lots, err := geilisub.ReadLots(ctx, c, sid)
	if err != nil {
		return nil, err
	}
	var chosen []geilisub.Lot
	for _, id := range ids {
		for _, lot := range lots {
			if lot.ID == id {
				chosen = append(chosen, lot)
			}
		}
	}
	if len(chosen) != len(ids) {
		return nil, geilisub.ErrSelection
	}
	result := &LegacyAlignmentResult{SubscriptionID: sid}
	for i, l := range chosen {
		if !l.Active(now) || l.SourceType == "campaign" || l.PlanID == nil || l.DailyLimitUSD == nil {
			return nil, geilisub.ErrSelection
		}
		if i > 0 && (*l.PlanID != *chosen[0].PlanID || *l.DailyLimitUSD != *chosen[0].DailyLimitUSD) {
			return nil, geilisub.ErrContractTier
		}
		if l.ExpiresAt.After(result.ExpiresAt) {
			result.ExpiresAt = l.ExpiresAt
		}
	}
	plans, err := c.SubscriptionPlan.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	recognized, _, err := resolveLegacyPlans(ctx, c, chosen, plans)
	if err != nil {
		return nil, err
	}
	if len(recognized) != len(chosen) {
		return nil, geilisub.ErrContractTier
	}
	for _, l := range chosen {
		before := geilisub.LegacyRight{PlanID: l.PlanID, SourceType: l.SourceType, Status: l.Status, StartsAt: l.StartsAt, ExpiresAt: l.ExpiresAt, DailyUSD: l.DailyLimitUSD}
		after := before
		after.ExpiresAt = result.ExpiresAt
		result.Before = append(result.Before, geilisub.LegacyLine{EntitlementID: l.ID, Before: &before, After: after})
	}
	raw, _ := json.Marshal(struct {
		Contract *geilisub.Contract
		Lines    []geilisub.LegacyLine
	}{current, result.Before})
	hash := sha256.Sum256(raw)
	result.Snapshot = hex.EncodeToString(hash[:])
	if !req.Apply {
		return result, nil
	}
	if req.ExpectedSnapshot != result.Snapshot {
		return nil, geilisub.ErrStateConflict
	}
	// Paid/in-flight order decisions must not be silently invalidated by support.
	if err = checkSubscriptionPending(ctx, c, req.UserID); err != nil {
		return nil, err
	}
	for _, l := range chosen {
		if !l.ExpiresAt.Equal(result.ExpiresAt) {
			if err = c.UserSubscriptionEntitlement.UpdateOneID(l.ID).SetExpiresAt(result.ExpiresAt).Exec(ctx); err != nil {
				return nil, err
			}
		}
	}
	if _, err = geilisub.MarkLegacyContract(ctx, c, sid, now); err != nil {
		return nil, err
	}
	if err = geilisub.RefreshParent(ctx, c, sid, now); err != nil {
		return nil, err
	}
	result.Applied = true
	for _, l := range chosen {
		if err = geilisub.RecordOperation(ctx, c, sid, l.ID, "align_free", "admin", req.IdempotencyKey, actor, map[string]any{"result": result, "ids": ids, "reason": req.Reason}); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if err = s.invalidateSubscriptionCaches(req.UserID, 0, sid); err != nil {
		return result, err
	}
	return result, nil
}
