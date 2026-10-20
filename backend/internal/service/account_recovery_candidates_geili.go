package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const accountRecoveryCandidatePageSizeGeili = 100

// Read one bounded page without the legacy active/schedulable filter. Keeping
// the group constraint in the query and checking fresh membership at dispatch
// lets error-stopped accounts recover without changing repository APIs. Manual
// inactive accounts are excluded by the shared recovery eligibility policy.
func listAccountRecoveryCandidatePageGeili(ctx context.Context, repo AccountRepository, groupID int64, platform string, page int) ([]Account, int, error) {
	if page < 1 {
		page = 1
	}
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	params := pagination.PaginationParams{Page: page, PageSize: accountRecoveryCandidatePageSizeGeili, SortBy: "priority", SortOrder: pagination.SortOrderAsc}
	accounts, result, err := repo.ListWithFilters(readCtx, params, platform, "", "", "", groupID, "")
	if err != nil {
		return nil, 0, err
	}
	if len(accounts) > params.PageSize {
		accounts = accounts[:params.PageSize]
	}
	next := 0
	if result != nil {
		if page < result.Pages {
			next = page + 1
		}
	} else if len(accounts) == params.PageSize {
		next = page + 1
	}
	return accounts, next, nil
}

func accountRecoveryGroupMemberGeili(a *Account, groupID int64) bool {
	if a == nil {
		return false
	}
	for _, id := range a.GroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

// Discovery and scheduler waiting share a single account-recovery clock. A
// catalog-only rejection never starts the separate upstream retry budget.
func accountRecoveryForegroundRemainingGeili(ctx context.Context, start bool) time.Duration {
	state := requestRecoveryFromContext(ctx)
	if state == nil {
		return 30 * time.Second
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	now := state.now()
	if state.accountRecoveryDeadline.IsZero() {
		if !start {
			return 30 * time.Second
		}
		state.accountRecoveryDeadline = now.Add(30 * time.Second)
	}
	remaining := state.accountRecoveryDeadline.Sub(now)
	if remaining < 0 {
		return 0
	}
	return remaining
}
