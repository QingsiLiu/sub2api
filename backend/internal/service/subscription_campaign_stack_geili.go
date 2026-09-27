package service

import (
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"time"
)

type CampaignStackOffer = geilisub.CampaignStackOffer

// Eligibility is local to this pool. Both the purchase UI and locked payment
// transaction also require this to be the user's only live pool.
func (s *UserSubscription) CampaignStackOffer(now time.Time) *CampaignStackOffer {
	if s == nil || s.DeletedAt != nil || s.Status != "active" || s.StartsAt.After(now) || !s.ExpiresAt.After(now) {
		return nil
	}
	if gift := geilisub.CampaignStackCandidate(s.Contract, s.Entitlements, now); gift != nil {
		return &gift.CampaignStackOffer
	}
	return nil
}
