package service

import "context"

type explicitRouteInitialReadGeili struct {
	owner         *User
	firstGroup    *Group
	firstGroupErr error
}

// The owner and first group are independent authoritative reads. Overlap their
// database waits, then join before applying the existing authorization checks.
// Only one extra read can be in flight, regardless of the number of key groups;
// subsequent groups retain their original order. No result is cached.
func (s *APIKeyService) readExplicitRouteInitialGeili(ctx context.Context, userID int64, ids []int64) (explicitRouteInitialReadGeili, error) {
	if len(ids) == 0 {
		owner, err := s.userRepo.GetByID(ctx, userID)
		return explicitRouteInitialReadGeili{owner: owner}, err
	}
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type groupRead struct {
		group      *Group
		err        error
		panicValue any
	}
	firstID := ids[0]
	finished := make(chan groupRead, 1)
	go func() {
		var first groupRead
		defer func() {
			// Repository panics must still reach request recovery, rather than
			// escaping on this goroutine and terminating the server process.
			first.panicValue = recover()
			finished <- first
		}()
		first.group, first.err = s.groupRepo.GetByIDLite(readCtx, firstID)
	}()
	owner, err := s.userRepo.GetByID(readCtx, userID)
	if err != nil {
		// Do not leave a speculative database read running after an owner
		// lookup failure. Preserve the owner's original error and join cleanup.
		cancel()
	}
	first := <-finished
	if err == nil && first.panicValue != nil {
		panic(first.panicValue)
	}
	return explicitRouteInitialReadGeili{owner: owner, firstGroup: first.group, firstGroupErr: first.err}, err
}
