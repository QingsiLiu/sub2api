package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	BenefitCampaignStatusDraft  = "draft"
	BenefitCampaignStatusActive = "active"
	BenefitCampaignStatusPaused = "paused"
	BenefitCampaignStatusClosed = "closed"
	BenefitCampaignResetBeijing = "beijing_day"
)

var (
	ErrBenefitCampaignNotFound   = infraerrors.NotFound("BENEFIT_CAMPAIGN_NOT_FOUND", "benefit campaign not found")
	ErrBenefitCampaignClosed     = infraerrors.Conflict("BENEFIT_CAMPAIGN_CLOSED", "benefit campaign is not available for claiming")
	ErrBenefitCampaignIneligible = infraerrors.Forbidden("BENEFIT_CAMPAIGN_INELIGIBLE", "this account is not eligible for the benefit campaign")
	ErrBenefitCampaignClaimed    = infraerrors.Conflict("BENEFIT_CAMPAIGN_CLAIMED", "benefit campaign has already been claimed")
	ErrBenefitCampaignSnapshot   = infraerrors.Conflict("BENEFIT_CAMPAIGN_SNAPSHOT_REQUIRED", "benefit campaign eligibility has not been frozen")
)

// BenefitCampaign is the server-owned activity definition. Times are always
// returned with their timezone so the activity page never infers a local zone.
type BenefitCampaign struct {
	ID                    int64
	Slug                  string
	Title                 string
	Status                string
	StartsAt              time.Time
	ClaimEndsAt           time.Time
	EligibilityStartsAt   time.Time
	EligibilityEndsAt     time.Time
	DurationDays          int
	DailyLimitUSD         float64
	ResetMode             string
	MaxClaims             *int64
	EligibilitySnapshotAt *time.Time
}

type BenefitCampaignClaim struct {
	ID             int64     `json:"id"`
	CampaignSlug   string    `json:"campaign_slug"`
	SubscriptionID int64     `json:"subscription_id"`
	EntitlementID  int64     `json:"entitlement_id"`
	ClaimedAt      time.Time `json:"claimed_at"`
	StartsAt       time.Time `json:"starts_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	DailyLimitUSD  float64   `json:"daily_limit_usd"`
}

type BenefitCampaignView struct {
	Campaign    *BenefitCampaignClaimView `json:"campaign,omitempty"`
	Eligible    bool                      `json:"eligible"`
	Claimed     bool                      `json:"claimed"`
	Claim       *BenefitCampaignClaim     `json:"claim,omitempty"`
	NeedsAPIKey bool                      `json:"needs_api_key"`
}

type CreateBenefitCampaignInput struct {
	Slug                string
	Title               string
	Status              string
	StartsAt            time.Time
	ClaimEndsAt         time.Time
	EligibilityStartsAt time.Time
	EligibilityEndsAt   time.Time
	DurationDays        int
	DailyLimitUSD       float64
	ResetMode           string
	MaxClaims           *int64
}

type BenefitCampaignClaimView struct {
	ID                  int64      `json:"id"`
	Slug                string     `json:"slug"`
	Title               string     `json:"title"`
	Status              string     `json:"status"`
	StartsAt            time.Time  `json:"starts_at"`
	ClaimEndsAt         time.Time  `json:"claim_ends_at"`
	EligibilityStartsAt time.Time  `json:"eligibility_starts_at"`
	EligibilityEndsAt   time.Time  `json:"eligibility_ends_at"`
	DurationDays        int        `json:"duration_days"`
	DailyLimitUSD       float64    `json:"daily_limit_usd"`
	ResetMode           string     `json:"reset_mode"`
	SnapshotAt          *time.Time `json:"eligibility_snapshot_at,omitempty"`
}

type BenefitCampaignService struct {
	entClient           *dbent.Client
	subscriptionService *SubscriptionService
}

type campaignSQL interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func scanCampaignOne(ctx context.Context, q campaignSQL, query string, args []any, dest ...any) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	return rows.Scan(dest...)
}

func NewBenefitCampaignService(entClient *dbent.Client, subscriptionService *SubscriptionService) *BenefitCampaignService {
	return &BenefitCampaignService{entClient: entClient, subscriptionService: subscriptionService}
}

func (s *BenefitCampaignService) Create(ctx context.Context, input CreateBenefitCampaignInput, actorID int64) (*BenefitCampaign, error) {
	if s == nil || s.entClient == nil {
		return nil, infraerrors.ServiceUnavailable("BENEFIT_CAMPAIGN_UNAVAILABLE", "benefit campaigns are unavailable")
	}
	input.Slug = strings.TrimSpace(input.Slug)
	input.Title = strings.TrimSpace(input.Title)
	if input.Slug == "" || input.Title == "" || input.DurationDays <= 0 || input.DurationDays > 365 || input.DailyLimitUSD <= 0 || math.IsNaN(input.DailyLimitUSD) || math.IsInf(input.DailyLimitUSD, 0) || (input.MaxClaims != nil && *input.MaxClaims <= 0) || !input.ClaimEndsAt.After(input.StartsAt) || !input.EligibilityEndsAt.After(input.EligibilityStartsAt) {
		return nil, infraerrors.BadRequest("BENEFIT_CAMPAIGN_INVALID", "invalid benefit campaign definition")
	}
	if input.Status == "" {
		input.Status = BenefitCampaignStatusDraft
	}
	switch input.Status {
	case BenefitCampaignStatusDraft, BenefitCampaignStatusActive, BenefitCampaignStatusPaused, BenefitCampaignStatusClosed:
	default:
		return nil, infraerrors.BadRequest("BENEFIT_CAMPAIGN_STATUS_INVALID", "invalid benefit campaign status")
	}
	if input.ResetMode == "" {
		input.ResetMode = BenefitCampaignResetBeijing
	}
	if input.ResetMode != BenefitCampaignResetBeijing {
		return nil, infraerrors.BadRequest("BENEFIT_CAMPAIGN_RESET_INVALID", "only Beijing calendar-day reset is supported")
	}
	var out BenefitCampaign
	var max sql.NullInt64
	var snapshot sql.NullTime
	err := scanCampaignOne(ctx, s.entClient, `INSERT INTO benefit_campaigns(slug,title,status,starts_at,claim_ends_at,eligibility_starts_at,eligibility_ends_at,duration_days,daily_limit_usd,reset_mode,max_claims,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12) RETURNING id,slug,title,status,starts_at,claim_ends_at,eligibility_starts_at,eligibility_ends_at,duration_days,daily_limit_usd,reset_mode,max_claims,eligibility_snapshot_at`, []any{input.Slug, input.Title, input.Status, input.StartsAt, input.ClaimEndsAt, input.EligibilityStartsAt, input.EligibilityEndsAt, input.DurationDays, input.DailyLimitUSD, input.ResetMode, input.MaxClaims, actorID}, &out.ID, &out.Slug, &out.Title, &out.Status, &out.StartsAt, &out.ClaimEndsAt, &out.EligibilityStartsAt, &out.EligibilityEndsAt, &out.DurationDays, &out.DailyLimitUSD, &out.ResetMode, &max, &snapshot)
	if err != nil {
		return nil, err
	}
	if max.Valid {
		out.MaxClaims = &max.Int64
	}
	if snapshot.Valid {
		out.EligibilitySnapshotAt = &snapshot.Time
	}
	return &out, nil
}

type BenefitCampaignStats struct {
	EligibleCount   int64   `json:"eligible_count"`
	ClaimedCount    int64   `json:"claimed_count"`
	NominalDailyUSD float64 `json:"nominal_daily_usd"`
	ActualUsageUSD  float64 `json:"actual_usage_usd"`
}

func (s *BenefitCampaignService) Update(ctx context.Context, id int64, input CreateBenefitCampaignInput, actorID int64) (*BenefitCampaign, error) {
	if s == nil || s.entClient == nil {
		return nil, infraerrors.ServiceUnavailable("BENEFIT_CAMPAIGN_UNAVAILABLE", "benefit campaigns are unavailable")
	}
	input.Slug = strings.TrimSpace(input.Slug)
	input.Title = strings.TrimSpace(input.Title)
	if input.Slug == "" || input.Title == "" || input.DurationDays <= 0 || input.DurationDays > 365 || input.DailyLimitUSD <= 0 || math.IsNaN(input.DailyLimitUSD) || math.IsInf(input.DailyLimitUSD, 0) || (input.MaxClaims != nil && *input.MaxClaims <= 0) || !input.ClaimEndsAt.After(input.StartsAt) || !input.EligibilityEndsAt.After(input.EligibilityStartsAt) {
		return nil, infraerrors.BadRequest("BENEFIT_CAMPAIGN_INVALID", "invalid benefit campaign definition")
	}
	if input.Status == "" {
		input.Status = BenefitCampaignStatusDraft
	}
	switch input.Status {
	case BenefitCampaignStatusDraft, BenefitCampaignStatusActive, BenefitCampaignStatusPaused, BenefitCampaignStatusClosed:
	default:
		return nil, infraerrors.BadRequest("BENEFIT_CAMPAIGN_STATUS_INVALID", "invalid benefit campaign status")
	}
	if input.ResetMode == "" {
		input.ResetMode = BenefitCampaignResetBeijing
	}
	if input.ResetMode != BenefitCampaignResetBeijing {
		return nil, infraerrors.BadRequest("BENEFIT_CAMPAIGN_RESET_INVALID", "only Beijing calendar-day reset is supported")
	}
	var out BenefitCampaign
	var max sql.NullInt64
	var snapshot sql.NullTime
	err := scanCampaignOne(ctx, s.entClient, `UPDATE benefit_campaigns SET slug=$2,title=$3,status=$4,starts_at=$5,claim_ends_at=$6,eligibility_starts_at=$7,eligibility_ends_at=$8,duration_days=$9,daily_limit_usd=$10,reset_mode=$11,max_claims=$12,updated_by=$13,updated_at=NOW() WHERE id=$1 RETURNING id,slug,title,status,starts_at,claim_ends_at,eligibility_starts_at,eligibility_ends_at,duration_days,daily_limit_usd,reset_mode,max_claims,eligibility_snapshot_at`, []any{id, input.Slug, input.Title, input.Status, input.StartsAt, input.ClaimEndsAt, input.EligibilityStartsAt, input.EligibilityEndsAt, input.DurationDays, input.DailyLimitUSD, input.ResetMode, input.MaxClaims, actorID}, &out.ID, &out.Slug, &out.Title, &out.Status, &out.StartsAt, &out.ClaimEndsAt, &out.EligibilityStartsAt, &out.EligibilityEndsAt, &out.DurationDays, &out.DailyLimitUSD, &out.ResetMode, &max, &snapshot)
	if err == sql.ErrNoRows {
		return nil, ErrBenefitCampaignNotFound
	}
	if err != nil {
		return nil, err
	}
	if max.Valid {
		out.MaxClaims = &max.Int64
	}
	if snapshot.Valid {
		out.EligibilitySnapshotAt = &snapshot.Time
	}
	return &out, nil
}

func (s *BenefitCampaignService) Stats(ctx context.Context, id int64) (*BenefitCampaignStats, error) {
	if s == nil || s.entClient == nil {
		return nil, infraerrors.ServiceUnavailable("BENEFIT_CAMPAIGN_UNAVAILABLE", "benefit campaigns are unavailable")
	}
	var out BenefitCampaignStats
	if err := scanCampaignOne(ctx, s.entClient, `SELECT COUNT(*) FROM benefit_campaign_eligibility WHERE campaign_id=$1`, []any{id}, &out.EligibleCount); err != nil {
		return nil, err
	}
	if err := scanCampaignOne(ctx, s.entClient, `SELECT COUNT(*),COALESCE(SUM(e.daily_limit_usd),0) FROM benefit_campaign_claims c JOIN user_subscription_entitlements e ON e.id=c.entitlement_id WHERE c.campaign_id=$1`, []any{id}, &out.ClaimedCount, &out.NominalDailyUSD); err != nil {
		return nil, err
	}
	if err := scanCampaignOne(ctx, s.entClient, `SELECT COALESCE(SUM(a.cost_usd),0) FROM subscription_usage_allocations a JOIN user_subscription_entitlements e ON e.id=a.entitlement_id JOIN benefit_campaign_claims c ON c.entitlement_id=e.id WHERE c.campaign_id=$1`, []any{id}, &out.ActualUsageUSD); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *BenefitCampaignService) List(ctx context.Context) ([]BenefitCampaign, error) {
	if s == nil || s.entClient == nil {
		return nil, infraerrors.ServiceUnavailable("BENEFIT_CAMPAIGN_UNAVAILABLE", "benefit campaigns are unavailable")
	}
	rows, err := s.entClient.QueryContext(ctx, `SELECT id,slug,title,status,starts_at,claim_ends_at,eligibility_starts_at,eligibility_ends_at,duration_days,daily_limit_usd,reset_mode,max_claims,eligibility_snapshot_at FROM benefit_campaigns ORDER BY starts_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]BenefitCampaign, 0)
	for rows.Next() {
		var c BenefitCampaign
		var max sql.NullInt64
		var snapshot sql.NullTime
		if err := rows.Scan(&c.ID, &c.Slug, &c.Title, &c.Status, &c.StartsAt, &c.ClaimEndsAt, &c.EligibilityStartsAt, &c.EligibilityEndsAt, &c.DurationDays, &c.DailyLimitUSD, &c.ResetMode, &max, &snapshot); err != nil {
			return nil, err
		}
		if max.Valid {
			c.MaxClaims = &max.Int64
		}
		if snapshot.Valid {
			c.EligibilitySnapshotAt = &snapshot.Time
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *BenefitCampaignService) SetStatus(ctx context.Context, id int64, status string, actorID int64) error {
	status = strings.TrimSpace(status)
	switch status {
	case BenefitCampaignStatusDraft, BenefitCampaignStatusActive, BenefitCampaignStatusPaused, BenefitCampaignStatusClosed:
	default:
		return infraerrors.BadRequest("BENEFIT_CAMPAIGN_STATUS_INVALID", "invalid benefit campaign status")
	}
	res, err := s.entClient.ExecContext(ctx, `UPDATE benefit_campaigns SET status=$2,updated_by=$3,updated_at=NOW() WHERE id=$1`, id, status, actorID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBenefitCampaignNotFound
	}
	return nil
}

func (s *BenefitCampaignService) Current(ctx context.Context, userID int64, now time.Time) (*BenefitCampaignView, error) {
	if s == nil || s.entClient == nil {
		return nil, infraerrors.ServiceUnavailable("BENEFIT_CAMPAIGN_UNAVAILABLE", "benefit campaigns are unavailable")
	}
	campaign, err := s.findCurrent(ctx, now)
	if err != nil {
		return nil, err
	}
	view := &BenefitCampaignView{
		Campaign: campaignView(campaign),
	}
	var one int
	err = scanCampaignOne(ctx, s.entClient, `SELECT 1 FROM benefit_campaign_eligibility WHERE campaign_id=$1 AND user_id=$2`, []any{campaign.ID, userID}, &one)
	if err == nil {
		view.Eligible = true
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	claim, err := loadCampaignClaim(ctx, s.entClient, campaign.ID, userID)
	if err != nil {
		return nil, err
	}
	if claim != nil {
		view.Claimed = true
		view.Claim = claim
		var bound int
		if err := scanCampaignOne(ctx, s.entClient, `SELECT COUNT(*) FROM api_keys WHERE subscription_id=$1 AND deleted_at IS NULL AND status='active'`, []any{claim.SubscriptionID}, &bound); err != nil {
			return nil, err
		}
		view.NeedsAPIKey = bound == 0
	}
	return view, nil
}

func (s *BenefitCampaignService) Claim(ctx context.Context, userID int64, slug, idempotencyKey string, now time.Time) (*BenefitCampaignClaim, error) {
	if s == nil || s.entClient == nil {
		return nil, infraerrors.ServiceUnavailable("BENEFIT_CAMPAIGN_UNAVAILABLE", "benefit campaigns are unavailable")
	}
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil, ErrBenefitCampaignNotFound
	}
	// The campaign/user unique pair is the idempotency scope. A caller-supplied
	// key must not collide across users or reserve another user's claim.
	idempotencyKey = fmt.Sprintf("benefit:%s:%d", slug, userID)

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	tc := dbent.NewTxContext(ctx, tx)
	defer func() { _ = tx.Rollback() }()
	c := tx.Client()
	campaign, err := lockCampaign(tc, c, slug)
	if err != nil {
		return nil, err
	}
	// A committed claim is the durable idempotency result, even after the
	// campaign pauses, ends, sells out, or the eligibility source changes.
	if existing, e := loadCampaignClaim(tc, c, campaign.ID, userID); e != nil {
		return nil, e
	} else if existing != nil {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if campaign.Status != BenefitCampaignStatusActive || now.Before(campaign.StartsAt) || !now.Before(campaign.ClaimEndsAt) {
		return nil, ErrBenefitCampaignClosed
	}
	if campaign.EligibilitySnapshotAt == nil {
		return nil, ErrBenefitCampaignSnapshot
	}
	var eligible int
	if err = scanCampaignOne(tc, c, `SELECT 1 FROM benefit_campaign_eligibility WHERE campaign_id=$1 AND user_id=$2`, []any{campaign.ID, userID}, &eligible); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrBenefitCampaignIneligible
		}
		return nil, err
	}
	if campaign.MaxClaims != nil {
		var count int64
		if err = scanCampaignOne(tc, c, `SELECT COUNT(*) FROM benefit_campaign_claims WHERE campaign_id=$1`, []any{campaign.ID}, &count); err != nil {
			return nil, err
		}
		if count >= *campaign.MaxClaims {
			return nil, ErrBenefitCampaignClosed
		}
	}
	if err = scanCampaignOne(tc, c, `SELECT id FROM users WHERE id=$1 AND deleted_at IS NULL AND status='active' FOR UPDATE`, []any{userID}, &eligible); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrBenefitCampaignIneligible
		}
		return nil, err
	}

	var subID int64
	var subStatus string
	var activeCount int
	rows, err := c.QueryContext(tc, `SELECT id,status FROM user_subscriptions WHERE user_id=$1 AND deleted_at IS NULL AND status='active' AND starts_at<=NOW() AND expires_at>NOW() ORDER BY id FOR UPDATE`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		activeCount++
		if activeCount == 1 {
			if err = rows.Scan(&subID, &subStatus); err != nil {
				_ = rows.Close()
				return nil, err
			}
		} else {
			var ignoredID int64
			if err = rows.Scan(&ignoredID, &subStatus); err != nil {
				_ = rows.Close()
				return nil, err
			}
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	claimAt := now
	expiresAt := claimAt.AddDate(0, 0, campaign.DurationDays)
	if activeCount != 1 {
		if err = scanCampaignOne(tc, c, `INSERT INTO user_subscriptions(user_id,group_id,plan_id,starts_at,expires_at,status,assigned_at,notes,created_at,updated_at) VALUES($1,NULL,NULL,$2,$3,'active',$2,$4,NOW(),NOW()) RETURNING id`, []any{userID, claimAt, expiresAt, "campaign:" + campaign.Slug}, &subID); err != nil {
			return nil, err
		}
		subStatus = SubscriptionStatusActive
	}
	day := geilisub.DayStart(claimAt)
	var entitlementID int64
	if err = scanCampaignOne(tc, c, `INSERT INTO user_subscription_entitlements(user_subscription_id,plan_id,source_type,source_reference,lot_index,purchase_mode,status,starts_at,expires_at,daily_limit_usd,daily_window_start,created_at,updated_at) VALUES($1,NULL,'campaign',$2,COALESCE((SELECT MAX(lot_index)+1 FROM user_subscription_entitlements WHERE user_subscription_id=$1),0),'campaign','active',$3,$4,$5,$6,NOW(),NOW()) RETURNING id`, []any{subID, campaign.Slug, claimAt, expiresAt, campaign.DailyLimitUSD, day}, &entitlementID); err != nil {
		return nil, err
	}
	detail, marshalErr := json.Marshal(map[string]any{"campaign": campaign.Slug, "daily_limit_usd": campaign.DailyLimitUSD})
	if marshalErr != nil {
		return nil, marshalErr
	}
	if _, err = c.ExecContext(tc, `INSERT INTO subscription_operations(subscription_id,entitlement_id,operation,source_type,source_reference,actor_id,detail,created_at) VALUES($1,$2,'create','campaign',$3,0,$4::jsonb,NOW())`, subID, entitlementID, campaign.Slug, string(detail)); err != nil {
		return nil, err
	}
	if _, err = c.ExecContext(tc, `UPDATE user_subscriptions SET expires_at=GREATEST(expires_at,$2),status=CASE WHEN status='suspended' THEN status ELSE 'active' END,updated_at=NOW() WHERE id=$1`, subID, expiresAt); err != nil {
		return nil, err
	}
	if subStatus == "" {
		subStatus = SubscriptionStatusActive
	}
	var claimID int64
	if err = scanCampaignOne(tc, c, `INSERT INTO benefit_campaign_claims(campaign_id,user_id,subscription_id,entitlement_id,idempotency_key,claimed_at,starts_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$6,$7) RETURNING id`, []any{campaign.ID, userID, subID, entitlementID, idempotencyKey, claimAt, expiresAt}, &claimID); err != nil {
		return nil, err
	}
	if _, err = c.ExecContext(tc, `UPDATE user_subscription_entitlements SET source_reference=$2,updated_at=$3 WHERE id=$1`, entitlementID, fmt.Sprintf("%s#%d", campaign.Slug, claimID), claimAt); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if s.subscriptionService != nil {
		if sub, loadErr := s.subscriptionService.GetByID(context.Background(), subID); loadErr == nil {
			for _, gid := range sub.EntitledGroupIDs {
				s.subscriptionService.InvalidateSubCache(userID, gid)
			}
		}
	}
	return &BenefitCampaignClaim{ID: claimID, CampaignSlug: campaign.Slug, SubscriptionID: subID, EntitlementID: entitlementID, ClaimedAt: claimAt, StartsAt: claimAt, ExpiresAt: expiresAt, DailyLimitUSD: campaign.DailyLimitUSD}, nil
}

func (s *BenefitCampaignService) Snapshot(ctx context.Context, campaignID int64, now time.Time) (int64, error) {
	if s == nil || s.entClient == nil {
		return 0, infraerrors.ServiceUnavailable("BENEFIT_CAMPAIGN_UNAVAILABLE", "benefit campaigns are unavailable")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return 0, err
	}
	tc := dbent.NewTxContext(ctx, tx)
	defer func() { _ = tx.Rollback() }()
	c := tx.Client()
	var start, end time.Time
	var frozen sql.NullTime
	if err = scanCampaignOne(tc, c, `SELECT eligibility_starts_at,eligibility_ends_at,eligibility_snapshot_at FROM benefit_campaigns WHERE id=$1 FOR UPDATE`, []any{campaignID}, &start, &end, &frozen); err != nil {
		if err == sql.ErrNoRows {
			return 0, ErrBenefitCampaignNotFound
		}
		return 0, err
	}
	if frozen.Valid {
		var count int64
		if err = scanCampaignOne(tc, c, `SELECT COUNT(*) FROM benefit_campaign_eligibility WHERE campaign_id=$1`, []any{campaignID}, &count); err != nil {
			return 0, err
		}
		if err = tx.Commit(); err != nil {
			return 0, err
		}
		return count, nil
	}
	if !end.After(start) || now.Before(end) {
		return 0, ErrBenefitCampaignSnapshot
	}
	if _, err = c.ExecContext(tc, `DELETE FROM benefit_campaign_eligibility WHERE campaign_id=$1`, campaignID); err != nil {
		return 0, err
	}
	res, err := c.ExecContext(tc, `INSERT INTO benefit_campaign_eligibility(campaign_id,user_id,reason,snapshot_at) SELECT $1,u.id,'settled_usage',$4 FROM users u WHERE u.deleted_at IS NULL AND u.status='active' AND EXISTS (SELECT 1 FROM usage_logs l WHERE l.user_id=u.id AND l.created_at >= $2 AND l.created_at < $3)`, campaignID, start, end, now)
	if err != nil {
		return 0, err
	}
	if _, err = c.ExecContext(tc, `UPDATE benefit_campaigns SET eligibility_snapshot_at=$2,updated_at=NOW() WHERE id=$1`, campaignID, now); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	count, err := res.RowsAffected()
	return count, err
}

func (s *BenefitCampaignService) findCurrent(ctx context.Context, now time.Time) (*BenefitCampaign, error) {
	var c BenefitCampaign
	var max sql.NullInt64
	var snapshot sql.NullTime
	err := scanCampaignOne(ctx, s.entClient, `SELECT id,slug,title,status,starts_at,claim_ends_at,eligibility_starts_at,eligibility_ends_at,duration_days,daily_limit_usd,reset_mode,max_claims,eligibility_snapshot_at FROM benefit_campaigns WHERE status IN ('active','paused') AND claim_ends_at>$1 ORDER BY starts_at DESC,id DESC LIMIT 1`, []any{now}, &c.ID, &c.Slug, &c.Title, &c.Status, &c.StartsAt, &c.ClaimEndsAt, &c.EligibilityStartsAt, &c.EligibilityEndsAt, &c.DurationDays, &c.DailyLimitUSD, &c.ResetMode, &max, &snapshot)
	if err == sql.ErrNoRows {
		return nil, ErrBenefitCampaignNotFound
	}
	if err != nil {
		return nil, err
	}
	if max.Valid {
		c.MaxClaims = &max.Int64
	}
	if snapshot.Valid {
		c.EligibilitySnapshotAt = &snapshot.Time
	}
	return &c, nil
}

func lockCampaign(ctx context.Context, c *dbent.Client, slug string) (*BenefitCampaign, error) {
	var out BenefitCampaign
	var max sql.NullInt64
	var snapshot sql.NullTime
	err := scanCampaignOne(ctx, c, `SELECT id,slug,title,status,starts_at,claim_ends_at,eligibility_starts_at,eligibility_ends_at,duration_days,daily_limit_usd,reset_mode,max_claims,eligibility_snapshot_at FROM benefit_campaigns WHERE slug=$1 FOR UPDATE`, []any{slug}, &out.ID, &out.Slug, &out.Title, &out.Status, &out.StartsAt, &out.ClaimEndsAt, &out.EligibilityStartsAt, &out.EligibilityEndsAt, &out.DurationDays, &out.DailyLimitUSD, &out.ResetMode, &max, &snapshot)
	if err == sql.ErrNoRows {
		return nil, ErrBenefitCampaignNotFound
	}
	if err != nil {
		return nil, err
	}
	if max.Valid {
		out.MaxClaims = &max.Int64
	}
	if snapshot.Valid {
		out.EligibilitySnapshotAt = &snapshot.Time
	}
	return &out, nil
}

func campaignView(c *BenefitCampaign) *BenefitCampaignClaimView {
	if c == nil {
		return nil
	}
	return &BenefitCampaignClaimView{ID: c.ID, Slug: c.Slug, Title: c.Title, Status: c.Status, StartsAt: c.StartsAt, ClaimEndsAt: c.ClaimEndsAt, EligibilityStartsAt: c.EligibilityStartsAt, EligibilityEndsAt: c.EligibilityEndsAt, DurationDays: c.DurationDays, DailyLimitUSD: c.DailyLimitUSD, ResetMode: c.ResetMode, SnapshotAt: c.EligibilitySnapshotAt}
}

func loadCampaignClaim(ctx context.Context, q campaignSQL, campaignID, userID int64) (*BenefitCampaignClaim, error) {
	var c BenefitCampaignClaim
	err := scanCampaignOne(ctx, q, `SELECT cl.id,ca.slug,cl.subscription_id,cl.entitlement_id,cl.claimed_at,cl.starts_at,cl.expires_at,e.daily_limit_usd FROM benefit_campaign_claims cl JOIN benefit_campaigns ca ON ca.id=cl.campaign_id JOIN user_subscription_entitlements e ON e.id=cl.entitlement_id WHERE cl.campaign_id=$1 AND cl.user_id=$2`, []any{campaignID, userID}, &c.ID, &c.CampaignSlug, &c.SubscriptionID, &c.EntitlementID, &c.ClaimedAt, &c.StartsAt, &c.ExpiresAt, &c.DailyLimitUSD)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}
