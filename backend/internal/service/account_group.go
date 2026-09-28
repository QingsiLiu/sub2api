package service

import "time"

type AccountGroup struct {
	// geili hook: copied with the account snapshot, never a mutated global priority.
	PriorityMode    string
	PriorityEnabled bool
	PriorityVersion int64
	AccountID       int64
	GroupID         int64
	Priority        int
	CreatedAt       time.Time

	Account *Account
	Group   *Group
}
