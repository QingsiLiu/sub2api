package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	GroupPriorityInherit = "inherit"
	GroupPriorityAuto    = "auto"
	GroupPriorityFixed   = "fixed"
)

func GeiliGroupPriorityPilot(id int64) bool { return id == 4 || id == 27 || id == 126 }

// EffectiveGroupPriority is shared by execution, administrator display and previews.
// Old snapshots have an empty mode/disabled flag and keep the global behavior.
func EffectiveGroupPriority(account *Account, groupID int64) (int, string) {
	if account == nil {
		return 0, GroupPriorityInherit
	}
	base := account.Priority
	if account.groupPriorityBase != nil {
		base = *account.groupPriorityBase
	}
	if account.Platform != PlatformOpenAI || !GeiliGroupPriorityPilot(groupID) {
		return base, GroupPriorityInherit
	}
	for _, binding := range account.AccountGroups {
		if binding.GroupID == groupID && binding.PriorityEnabled && (binding.PriorityMode == GroupPriorityAuto || binding.PriorityMode == GroupPriorityFixed) {
			return binding.Priority, binding.PriorityMode
		}
	}
	return base, GroupPriorityInherit
}

func projectGroupPrioritiesGeili(accounts []Account, groupID *int64) []Account {
	if groupID == nil || !GeiliGroupPriorityPilot(*groupID) {
		return accounts
	}
	out := make([]Account, len(accounts))
	copy(out, accounts)
	for i := range out {
		base := out[i].Priority
		if out[i].groupPriorityBase != nil {
			base = *out[i].groupPriorityBase
		}
		priority, _ := EffectiveGroupPriority(&out[i], *groupID)
		out[i].groupPriorityBase = &base
		out[i].Priority = priority
		for _, binding := range out[i].AccountGroups {
			if binding.GroupID == *groupID && binding.PriorityEnabled && out[i].Platform == PlatformOpenAI {
				out[i].groupPriorityApplied = true
			}
		}
	}
	return out
}

type GroupSchedulingRowGeili struct {
	AccountID          int64  `json:"account_id"`
	AccountPriority    int    `json:"account_priority"`
	GroupPriority      int    `json:"group_priority"`
	Mode               string `json:"mode"`
	ConfiguredPriority int    `json:"configured_priority"`
	EffectivePriority  *int   `json:"effective_priority"`
	EffectiveMode      string `json:"effective_mode"`
	CachePending       bool   `json:"cache_pending"`
	Eligible           bool   `json:"eligible"`
	EligibilityKnown   bool   `json:"eligibility_known"`
	Reason             string `json:"reason,omitempty"`
	LoadRate           *int   `json:"load_rate,omitempty"`
}

type GroupSchedulingStateGeili struct {
	GroupID        int64                     `json:"group_id"`
	Name           string                    `json:"name"`
	Platform       string                    `json:"platform"`
	Supported      bool                      `json:"supported"`
	Enabled        bool                      `json:"enabled"`
	Version        int64                     `json:"version"`
	MembersVersion string                    `json:"members_version"`
	Rows           []GroupSchedulingRowGeili `json:"rows"`
}

// Membership revision also catches ordinary binding edits outside this feature.
func GroupSchedulingMembersVersionGeili(rows []GroupSchedulingRowGeili) string {
	type member struct {
		ID       int64
		Priority int
		Mode     string
	}
	values := make([]member, 0, len(rows))
	for _, r := range rows {
		values = append(values, member{r.AccountID, r.GroupPriority, r.Mode})
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	raw, _ := json.Marshal(values)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type GroupSchedulingUpdateRowGeili struct {
	AccountID int64  `json:"account_id"`
	Mode      string `json:"mode"`
	Priority  *int   `json:"priority"`
}

type GroupSchedulingUpdateGeili struct {
	ExpectedVersion        *int64                          `json:"expected_version"`
	ExpectedMembersVersion string                          `json:"expected_members_version"`
	Enabled                *bool                           `json:"enabled,omitempty"`
	Source                 string                          `json:"source"`
	Rows                   []GroupSchedulingUpdateRowGeili `json:"rows"`
}

func (u GroupSchedulingUpdateGeili) Validate() error {
	bad := func(message string) error { return infraerrors.BadRequest("GROUP_PRIORITY_INVALID", message) }
	if _, err := hex.DecodeString(u.ExpectedMembersVersion); err != nil {
		return bad("invalid membership revision")
	}
	if u.ExpectedVersion == nil || *u.ExpectedVersion < 0 || len(u.ExpectedMembersVersion) != 64 {
		return bad("expected version and membership revision are required")
	}
	if u.Source != "manual" && u.Source != "automatic" {
		return bad("source must be manual or automatic")
	}
	if len(u.Rows) > 500 {
		return bad("at most 500 updates per group")
	}
	if u.Source == "automatic" && u.Enabled != nil {
		return bad("automatic updates cannot enable or disable groups")
	}
	seen := map[int64]bool{}
	for _, row := range u.Rows {
		if row.AccountID <= 0 || seen[row.AccountID] {
			return bad("invalid or duplicate account ID")
		}
		seen[row.AccountID] = true
		if row.Mode != GroupPriorityInherit && row.Mode != GroupPriorityAuto && row.Mode != GroupPriorityFixed {
			return bad("invalid priority mode")
		}
		if (row.Mode != GroupPriorityInherit && row.Priority == nil) || (row.Priority != nil && (*row.Priority < 0 || *row.Priority > 2000000000)) {
			return bad("priority must be between 0 and 2000000000")
		}
		if u.Source == "automatic" && row.Mode != GroupPriorityAuto {
			return bad("automatic updates can only change auto priorities")
		}
	}
	return nil
}

var ErrGroupSchedulingConflictGeili = infraerrors.New(http.StatusConflict, "GROUP_PRIORITY_CONFLICT", "group configuration or membership changed; refresh before retrying")

type GroupSchedulingRepositoryGeili interface {
	GetGroupSchedulingGeili(context.Context, []int64) ([]GroupSchedulingStateGeili, error)
	UpdateGroupSchedulingGeili(context.Context, int64, GroupSchedulingUpdateGeili, string) error
	GroupSchedulingRuntimeGeili(context.Context, []int64) (map[int64]*Account, error)
}

type GroupSchedulingSnapshotGeili struct {
	APIVersion int                         `json:"api_version"`
	ObservedAt time.Time                   `json:"observed_at"`
	Groups     []GroupSchedulingStateGeili `json:"groups"`
}

type GroupSchedulingPreviewGeili struct {
	Model          string                   `json:"model"`
	Transport      OpenAIUpstreamTransport  `json:"transport"`
	Capability     OpenAIEndpointCapability `json:"capability"`
	RequireCompact bool                     `json:"require_compact"`
}

type GroupSchedulingAdminGeili interface {
	GroupSchedulingSnapshotGeili(context.Context, []int64) (*GroupSchedulingSnapshotGeili, error)
	SetGroupSchedulingGeili(context.Context, int64, GroupSchedulingUpdateGeili, string) (*GroupSchedulingSnapshotGeili, error)
	PreviewGroupSchedulingGeili(context.Context, int64, GroupSchedulingPreviewGeili) (*GroupSchedulingSnapshotGeili, error)
}

func (s *adminServiceImpl) groupSchedulingRepoGeili() (GroupSchedulingRepositoryGeili, error) {
	repo, ok := s.accountRepo.(GroupSchedulingRepositoryGeili)
	if !ok {
		return nil, infraerrors.New(503, "GROUP_PRIORITY_UNAVAILABLE", "group scheduling is unavailable")
	}
	return repo, nil
}

func (s *adminServiceImpl) GroupSchedulingSnapshotGeili(ctx context.Context, ids []int64) (*GroupSchedulingSnapshotGeili, error) {
	return s.groupSchedulingSnapshotGeili(ctx, ids, nil)
}

func (s *adminServiceImpl) groupSchedulingSnapshotGeili(ctx context.Context, ids []int64, preview *GroupSchedulingPreviewGeili) (*GroupSchedulingSnapshotGeili, error) {
	if len(ids) == 0 || len(ids) > 64 {
		return nil, infraerrors.BadRequest("GROUP_PRIORITY_INVALID", "between 1 and 64 group IDs are required")
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, infraerrors.BadRequest("GROUP_PRIORITY_INVALID", "group IDs must be positive")
		}
	}
	repo, err := s.groupSchedulingRepoGeili()
	if err != nil {
		return nil, err
	}
	groups, err := repo.GetGroupSchedulingGeili(ctx, ids)
	if err != nil {
		return nil, err
	}
	accountIDs := []int64{}
	seen := map[int64]bool{}
	for _, g := range groups {
		for _, row := range g.Rows {
			if !seen[row.AccountID] {
				seen[row.AccountID] = true
				accountIDs = append(accountIDs, row.AccountID)
			}
		}
	}
	stored, err := s.accountRepo.GetByIDs(ctx, accountIDs)
	if err != nil {
		return nil, err
	}
	storedMap := map[int64]*Account{}
	for _, a := range stored {
		storedMap[a.ID] = a
	}
	runtime, runtimeErr := repo.GroupSchedulingRuntimeGeili(ctx, accountIDs)
	gateway, _ := s.runtimeBlocker.(*OpenAIGatewayService)
	if gateway != nil {
		ctx = gateway.withOpenAIQuotaAutoPauseContext(ctx)
	}
	var loadMap map[int64]*AccountLoadInfo
	var loadErr error
	if gateway != nil && gateway.concurrencyService != nil {
		requests := make([]AccountWithConcurrency, 0, len(accountIDs))
		for _, id := range accountIDs {
			if a := runtime[id]; a != nil {
				requests = append(requests, AccountWithConcurrency{ID: id, MaxConcurrency: a.EffectiveLoadFactor()})
			}
		}
		loadMap, loadErr = gateway.concurrencyService.GetAccountsLoadBatch(ctx, requests)
	}
	for gi := range groups {
		group := &groups[gi]
		groupConfig, err := s.groupRepo.GetByID(ctx, group.GroupID)
		if err != nil {
			return nil, err
		}
		for ri := range group.Rows {
			row := &group.Rows[ri]
			account := storedMap[row.AccountID]
			if account == nil {
				row.Reason = "account_missing"
				continue
			}
			row.ConfiguredPriority, _ = EffectiveGroupPriority(account, group.GroupID)
			current := runtime[row.AccountID]
			// No cache configured (unit/DB-only installations) uses repository state.
			if runtime == nil && runtimeErr == nil {
				current = account
			}
			if current == nil || runtimeErr != nil {
				row.CachePending = true
				row.Reason = "runtime_snapshot_unavailable"
				continue
			}
			effective, mode := EffectiveGroupPriority(current, group.GroupID)
			row.EffectivePriority = &effective
			row.EffectiveMode = mode
			row.CachePending = effective != row.ConfiguredPriority
			matched := false
			for _, binding := range current.AccountGroups {
				if binding.GroupID == group.GroupID {
					matched = true
					row.CachePending = row.CachePending || binding.PriorityEnabled != group.Enabled || binding.PriorityVersion != group.Version || func() string {
						if binding.PriorityMode == "" {
							return GroupPriorityInherit
						}
						return binding.PriorityMode
					}() != row.Mode
					break
				}
			}
			if !matched {
				row.CachePending = true
			}
			if row.CachePending {
				row.Reason = "runtime_snapshot_pending"
				continue
			}
			row.EligibilityKnown = true
			if groupConfig.Status != StatusActive {
				row.Reason = "group_disabled"
				continue
			}
			if !current.IsSchedulable() {
				row.Reason = "not_schedulable"
				continue
			}
			if groupConfig.RequirePrivacySet && !current.IsPrivacySet() {
				row.Reason = "privacy_not_set"
				continue
			}
			if current.Platform != PlatformOpenAI {
				row.Eligible = true
				continue
			}
			req := OpenAIAccountScheduleRequest{GroupID: &group.GroupID, Platform: PlatformOpenAI, RequirePrivacySet: groupConfig.RequirePrivacySet}
			if preview != nil {
				req.RequestedModel = preview.Model
				req.RequiredTransport = preview.Transport
				req.RequiredCapability = preview.Capability
				req.RequireCompact = preview.RequireCompact
			}
			checker := &defaultOpenAIAccountScheduler{service: gateway}
			if gateway != nil {
				compatible, reason := checker.isAccountRequestCompatibleReason(ctx, current, req)
				if !compatible {
					row.Reason = reason
					continue
				}
				if !checker.isAccountTransportCompatible(current, req.RequiredTransport) {
					row.Reason = "transport_incompatible"
					continue
				}
			} else if reason := openAICompatibleAccountEligibilityFailureReasonBeforeProfit(ctx, current, PlatformOpenAI, req.RequestedModel, req.RequireCompact, req.RequiredCapability); reason != "" {
				row.Reason = reason
				continue
			}
			row.Eligible = true
			if load, ok := loadMap[row.AccountID]; ok && load != nil {
				value := load.LoadRate
				row.LoadRate = &value
				if value >= 100 {
					row.Eligible = false
					row.Reason = "capacity_full"
				}
			} else if loadErr != nil {
				row.EligibilityKnown = false
				row.Reason = "load_unavailable"
			}
			if preview != nil && gateway != nil && gateway.checkChannelPricingRestriction(ctx, &group.GroupID, preview.Model) {
				row.Eligible = false
				row.Reason = "channel_pricing_restricted"
			}
			if gateway != nil && gateway.isOpenAIAccountBlockedBySchedulingThreshold(ctx, current) {
				row.Eligible = false
				row.Reason = "scheduling_threshold"
			}
			// A preview has no API-key billing/sticky context. Never pretend to predict them.
			if groupConfig.ProfitControlEnabled {
				row.Reason = "request_profit_context_required"
				row.EligibilityKnown = false
			}
		}
	}
	return &GroupSchedulingSnapshotGeili{APIVersion: 1, ObservedAt: time.Now().UTC(), Groups: groups}, nil
}

func (s *adminServiceImpl) SetGroupSchedulingGeili(ctx context.Context, id int64, input GroupSchedulingUpdateGeili, actor string) (*GroupSchedulingSnapshotGeili, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	repo, err := s.groupSchedulingRepoGeili()
	if err != nil {
		return nil, err
	}
	if err = repo.UpdateGroupSchedulingGeili(ctx, id, input, actor); err != nil {
		return nil, err
	}
	return s.GroupSchedulingSnapshotGeili(ctx, []int64{id})
}

func (s *adminServiceImpl) PreviewGroupSchedulingGeili(ctx context.Context, id int64, input GroupSchedulingPreviewGeili) (*GroupSchedulingSnapshotGeili, error) {
	if input.Transport != "" && input.Transport != OpenAIUpstreamTransportAny && input.Transport != OpenAIUpstreamTransportHTTPSSE && input.Transport != OpenAIUpstreamTransportResponsesWebsocketV2 {
		return nil, infraerrors.BadRequest("GROUP_PRIORITY_INVALID", "unsupported transport")
	}
	switch input.Capability {
	case "", OpenAIEndpointCapabilityResponses, OpenAIEndpointCapabilityChatCompletions, OpenAIEndpointCapabilityEmbeddings:
	default:
		return nil, infraerrors.BadRequest("GROUP_PRIORITY_INVALID", "unsupported endpoint capability")
	}
	if len(input.Model) > 200 {
		return nil, infraerrors.BadRequest("GROUP_PRIORITY_INVALID", "model name too long")
	}
	snapshot, err := s.groupSchedulingSnapshotGeili(ctx, []int64{id}, &input)
	if err != nil {
		return nil, err
	}
	for i := range snapshot.Groups {
		sort.SliceStable(snapshot.Groups[i].Rows, func(a, b int) bool {
			x, y := snapshot.Groups[i].Rows[a], snapshot.Groups[i].Rows[b]
			if x.Eligible != y.Eligible {
				return x.Eligible
			}
			if x.EffectivePriority == nil {
				return false
			}
			if y.EffectivePriority == nil {
				return true
			}
			return *x.EffectivePriority < *y.EffectivePriority
		})
	}
	return snapshot, nil
}

// DefaultSchedulingPriority protects persistence paths from request-local projections.
func (a *Account) DefaultSchedulingPriority() int {
	if a.groupPriorityBase != nil {
		return *a.groupPriorityBase
	}
	return a.Priority
}
