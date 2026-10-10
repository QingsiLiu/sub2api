package service

import (
	"context"
	"encoding/json"
)

type openAITransportSnapshotRepositoryGeili interface {
	ListOpenAITransportSnapshotCandidatesGeili(context.Context, *int64, bool) ([]Account, error)
}

// Snapshot membership must survive a short transport quarantine. The regular
// scheduling query drops cooled accounts until the next full rebuild (normally
// five minutes), even after their sixty-second block has expired. Keep only this
// new kind of transient member; request-time checks still enforce every block.
func (s *SchedulerSnapshotService) loadOpenAITransportSnapshotGeili(ctx context.Context, bucket SchedulerBucket, useMixed bool) ([]Account, bool, error) {
	if bucket.Platform != PlatformOpenAI || useMixed {
		return nil, false, nil
	}
	repository, ok := s.accountRepo.(openAITransportSnapshotRepositoryGeili)
	if !ok {
		return nil, false, nil
	}
	var groupID *int64
	if bucket.GroupID > 0 && !s.isRunModeSimple() {
		id := bucket.GroupID
		groupID = &id
	}
	accounts, err := repository.ListOpenAITransportSnapshotCandidatesGeili(ctx, groupID, s.isRunModeSimple())
	if err != nil {
		return nil, true, err
	}
	retained := make([]Account, 0, len(accounts))
	for _, account := range accounts {
		if account.IsSchedulable() {
			retained = append(retained, account)
			continue
		}
		if !isOpenAITransportHealthAccountGeili(&account) || account.TempUnschedulableUntil == nil {
			continue
		}
		var reason struct {
			MatchedKeyword string `json:"matched_keyword"`
		}
		if json.Unmarshal([]byte(account.TempUnschedulableReason), &reason) != nil || reason.MatchedKeyword != openAITransportHealthReasonGeili {
			continue
		}
		withoutTransportBlock := account
		withoutTransportBlock.TempUnschedulableUntil = nil
		if withoutTransportBlock.IsSchedulable() {
			retained = append(retained, account)
		}
	}
	return retained, true, nil
}
