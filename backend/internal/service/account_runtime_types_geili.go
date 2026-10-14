package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

var (
	ErrAccountRuntimeConflictGeili = errors.New("account configuration or runtime state changed during probe")
	ErrAccountBalanceBlockedGeili  = errors.New("account balance is blocked; a trusted balance verification is required")
	ErrAccountProbeUnhealthyGeili  = errors.New("account probe did not verify a complete healthy response")
)

type accountAvailabilityFilterContextKeyGeili struct{}

func WithAccountAvailabilityFilterGeili(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, accountAvailabilityFilterContextKeyGeili{}, value)
}

func AccountAvailabilityFilterGeili(ctx context.Context) string {
	value, _ := ctx.Value(accountAvailabilityFilterContextKeyGeili{}).(string)
	return value
}

type AccountModelHealthGeili struct {
	ProbeFailureCount      int                      `json:"probe_failure_count"`
	FailureWindowStartedAt *time.Time               `json:"failure_window_started_at,omitempty"`
	Blocked                bool                     `json:"blocked"`
	NextCheckAt            *time.Time               `json:"next_check_at,omitempty"`
	LastProbe              *AccountProbeResultGeili `json:"last_probe,omitempty"`
	Health                 string                   `json:"health"`
	FailureCount           int                      `json:"failure_count"`
	LastFailureAt          *time.Time               `json:"last_failure_at,omitempty"`
	LastCheckedAt          *time.Time               `json:"last_checked_at,omitempty"`
	LastSuccessAt          *time.Time               `json:"last_success_at,omitempty"`
	NextProbeAt            *time.Time               `json:"next_probe_at,omitempty"`
	LastError              string                   `json:"last_error,omitempty"`
}

type AccountRuntimeStateGeili struct {
	LastProbe         *AccountProbeResultGeili           `json:"last_probe,omitempty"`
	NextCheckAt       *time.Time                         `json:"next_check_at,omitempty"`
	AccountID         int64                              `json:"account_id"`
	Version           int64                              `json:"version"`
	BalanceBlocked    bool                               `json:"balance_blocked"`
	BalanceReason     string                             `json:"balance_reason,omitempty"`
	BalanceBlockedAt  *time.Time                         `json:"balance_blocked_at,omitempty"`
	StopReason        string                             `json:"stop_reason,omitempty"`
	ProtectionEnabled bool                               `json:"protection_enabled"`
	Models            map[string]AccountModelHealthGeili `json:"models"`
	UpdatedAt         time.Time                          `json:"updated_at"`
}

// The fingerprint contains no credentials; it binds a probe grant to the exact
// credential, transport, group and administrator state observed before I/O.
type AccountProbeSnapshotGeili struct {
	AccountID      int64  `json:"account_id"`
	RuntimeVersion int64  `json:"runtime_version"`
	Fingerprint    string `json:"fingerprint"`
}

func MakeAccountProbeSnapshotGeili(account *Account) AccountProbeSnapshotGeili {
	if account == nil {
		return AccountProbeSnapshotGeili{}
	}
	groups := append([]int64(nil), account.GroupIDs...)
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	identity := struct {
		Platform, Type, Status string
		Credentials            map[string]any
		ProxyID                *int64
		Proxy                  *Proxy
		GroupIDs               []int64
		Schedulable            bool
		UpdatedAt              time.Time
	}{account.Platform, account.Type, account.Status, account.Credentials, account.ProxyID, account.Proxy, groups, account.Schedulable, account.UpdatedAt}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return AccountProbeSnapshotGeili{AccountID: account.ID}
	}
	digest := sha256.Sum256(encoded)
	snapshot := AccountProbeSnapshotGeili{AccountID: account.ID, Fingerprint: hex.EncodeToString(digest[:])}
	if account.RuntimeState != nil {
		snapshot.RuntimeVersion = account.RuntimeState.Version
	}
	return snapshot
}

type AccountProbeResultGeili struct {
	Snapshot           AccountProbeSnapshotGeili `json:"snapshot"`
	Model              string                    `json:"model"`
	Source             string                    `json:"source"`
	Success            bool                      `json:"success"`
	Complete           bool                      `json:"complete"`
	BalanceVerified    bool                      `json:"balance_verified"`
	HTTPStatus         int                       `json:"http_status"`
	Reason             string                    `json:"reason,omitempty"`
	DurationMs         int64                     `json:"duration_ms"`
	TimeToFirstTokenMs *int64                    `json:"time_to_first_token_ms,omitempty"`
	CheckedAt          time.Time                 `json:"checked_at"`
	NextProbeAt        *time.Time                `json:"next_probe_at,omitempty"`
}

type AccountProbeActivationGeili struct {
	Snapshot               AccountProbeSnapshotGeili
	Model                  string
	EnableProtection       bool
	TrustedBalanceVerified bool
}

type AccountProbeLeaseGeili struct {
	AccountID int64
	Model     string
	Token     string
	ExpiresAt time.Time
}

type AccountRuntimeAdmissionGeili struct {
	CredentialAccountID int64
	Allowed             bool
	BalanceBlocked      bool
	Reason              string
}

// Optional capability: existing AccountRepository implementations and mocks do
// not need to implement runtime persistence or distributed probe coordination.
type AccountRuntimeRepositoryGeili interface {
	GetAccountRuntimeGeili(context.Context, int64) (*AccountRuntimeStateGeili, error)
	LoadAccountRuntimeBatchGeili(context.Context, []int64) (map[int64]*AccountRuntimeStateGeili, error)
	MarkAccountBalanceBlockedGeili(context.Context, int64, string) error
	RecordModelFailureGeili(context.Context, int64, string, string, time.Time) error
	ResetAccountModelSuccessGeili(context.Context, int64, string) error
	RecordAccountProbeGeili(context.Context, AccountProbeResultGeili) error
	ActivateAccountAfterProbeGeili(context.Context, AccountProbeActivationGeili) (*AccountRuntimeStateGeili, error)
	AcquireProbeLeaseGeili(context.Context, int64, string, time.Duration) (*AccountProbeLeaseGeili, error)
	ReleaseProbeLeaseGeili(context.Context, *AccountProbeLeaseGeili) error
	CheckAccountRuntimeAdmissionGeili(context.Context, int64, string) (*AccountRuntimeAdmissionGeili, error)
}

// Optional startup capability; reclaim only bounded, recent confirmed errors.
type AccountBalanceBlockInitializerGeili interface {
	InitializeAccountBalanceBlocksGeili(context.Context, int) (int, error)
}

type AccountBalanceBlockedReaderGeili interface {
	ListAccountBalanceBlockedGeili(context.Context, int64, int) ([]int64, bool, error)
}
