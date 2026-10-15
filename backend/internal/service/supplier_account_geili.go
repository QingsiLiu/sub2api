package service

import (
	"context"
	"errors"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// Supplier accounts: a supplier uploads API-Key accounts, which stay inactive,
// unschedulable and ungrouped until an administrator reviews them. Ownership is
// stored in account_supplier_owners_geili, never in editable account fields.

var (
	ErrSupplierAccountNotFound = infraerrors.NotFound("SUPPLIER_ACCOUNT_NOT_FOUND", "account not found")
	ErrSupplierAccountInvalid  = infraerrors.BadRequest("SUPPLIER_ACCOUNT_INVALID", "invalid supplier account input")
	ErrSupplierPlatformInvalid = infraerrors.BadRequest("SUPPLIER_PLATFORM_INVALID", "platform is not available for suppliers")
	ErrSupplierOwnerInvalid    = infraerrors.BadRequest("SUPPLIER_OWNER_INVALID", "owner must be an active supplier user")
)

// SupplierAccountPublicEgressExtraKeyGeili reuses the external-intake egress marker:
// HTTP upstream dials for these accounts only reach validated public addresses.
const SupplierAccountPublicEgressExtraKeyGeili = "external_submission_form_geili"

const supplierAccountDefaultPriorityGeili = 50

// SupplierPlatformsGeili lists the platforms whose plain API-Key accounts a supplier may upload.
var SupplierPlatformsGeili = []string{
	PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformGrok,
	PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax,
	PlatformTypeSafe, PlatformCommandCode, PlatformCline,
}

func supplierPlatformAllowedGeili(platform string) bool {
	for _, p := range SupplierPlatformsGeili {
		if p == platform {
			return true
		}
	}
	return false
}

type SupplierAccountOwnerGeili struct {
	UserID int64  `json:"id"`
	Email  string `json:"email"`
}

type SupplierAccountRepositoryGeili interface {
	// CreateSupplierAccount persists the account, its owner row and the scheduler outbox atomically.
	CreateSupplierAccount(ctx context.Context, account *Account, ownerID int64) error
	// SupplierAccountIDs returns every account the supplier owns, including soft-deleted ones
	// so historical usage stays visible.
	SupplierAccountIDs(ctx context.Context, ownerID int64) ([]int64, error)
	// IsLiveSupplierAccount reports whether a non-deleted account belongs to the supplier.
	IsLiveSupplierAccount(ctx context.Context, ownerID, accountID int64) (bool, error)
	// SetSupplierOwner assigns (ownerID>0) or clears (ownerID==0) the supplier of an account.
	SetSupplierOwner(ctx context.Context, accountID, ownerID int64) error
	SupplierOwners(ctx context.Context, accountIDs []int64) (map[int64]SupplierAccountOwnerGeili, error)
}

type accountSupplierOwnerFilterKeyGeili struct{}

// WithAccountSupplierOwnerFilterGeili scopes account list queries to one supplier.
func WithAccountSupplierOwnerFilterGeili(ctx context.Context, ownerID int64) context.Context {
	return context.WithValue(ctx, accountSupplierOwnerFilterKeyGeili{}, ownerID)
}

func AccountSupplierOwnerFilterGeili(ctx context.Context) (int64, bool) {
	if ctx == nil {
		return 0, false
	}
	id, ok := ctx.Value(accountSupplierOwnerFilterKeyGeili{}).(int64)
	return id, ok
}

type SupplierAccountServiceGeili struct {
	repo         SupplierAccountRepositoryGeili
	adminService AdminService
	usageService *UsageService
	userRepo     UserRepository
}

func NewSupplierAccountServiceGeili(repo SupplierAccountRepositoryGeili, adminService AdminService, usageService *UsageService, userRepo UserRepository) *SupplierAccountServiceGeili {
	return &SupplierAccountServiceGeili{repo: repo, adminService: adminService, usageService: usageService, userRepo: userRepo}
}

type SupplierAccountInputGeili struct {
	Platform    string
	Name        string
	Notes       *string
	APIKey      string
	BaseURL     *string
	Concurrency *int
}

func normalizeSupplierNameGeili(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 {
		return "", ErrSupplierAccountInvalid
	}
	return name, nil
}

func validateSupplierConcurrencyGeili(c *int) error {
	if c != nil && (*c < 1 || *c > 1000) {
		return ErrSupplierAccountInvalid
	}
	return nil
}

// validateSupplierCredentialsGeili checks the key format and resolves every base URL host
// to public addresses, shared with the external intake form.
func validateSupplierCredentialsGeili(ctx context.Context, platform string, credentials map[string]any) error {
	probe := &Account{Platform: platform, Type: AccountTypeAPIKey, Credentials: credentials}
	if err := validateSubmissionFormCredentialsGeili(ctx, probe); err != nil {
		return ErrSupplierAccountInvalid
	}
	return nil
}

func (s *SupplierAccountServiceGeili) List(ctx context.Context, ownerID int64, page, pageSize int, platform, status, search string) ([]Account, int64, error) {
	if platform != "" && !supplierPlatformAllowedGeili(platform) {
		return nil, 0, ErrSupplierPlatformInvalid
	}
	if len(search) > 100 {
		search = search[:100]
	}
	scoped := WithAccountSupplierOwnerFilterGeili(ctx, ownerID)
	accounts, total, err := s.adminService.ListAccounts(scoped, page, pageSize, platform, "", status, strings.TrimSpace(search), 0, "", "created_at", "desc")
	if err != nil {
		return nil, 0, err
	}
	// Defence in depth: never return an account the owner table does not confirm.
	owned, err := s.ownedSet(ctx, ownerID)
	if err != nil {
		return nil, 0, err
	}
	out := accounts[:0]
	for _, a := range accounts {
		if owned[a.ID] {
			out = append(out, a)
		}
	}
	if len(out) != len(accounts) {
		return nil, 0, errors.New("supplier account scope mismatch")
	}
	return out, total, nil
}

func (s *SupplierAccountServiceGeili) ownedSet(ctx context.Context, ownerID int64) (map[int64]bool, error) {
	ids, err := s.repo.SupplierAccountIDs(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	set := make(map[int64]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set, nil
}

// Get loads an account only when the supplier owns it; foreign IDs look like missing accounts.
func (s *SupplierAccountServiceGeili) Get(ctx context.Context, ownerID, accountID int64) (*Account, error) {
	if ownerID <= 0 || accountID <= 0 {
		return nil, ErrSupplierAccountNotFound
	}
	ok, err := s.repo.IsLiveSupplierAccount(ctx, ownerID, accountID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrSupplierAccountNotFound
	}
	account, err := s.adminService.GetAccount(ctx, accountID)
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			return nil, ErrSupplierAccountNotFound
		}
		return nil, err
	}
	return account, nil
}

func (s *SupplierAccountServiceGeili) Create(ctx context.Context, ownerID int64, input SupplierAccountInputGeili) (*Account, error) {
	if !supplierPlatformAllowedGeili(input.Platform) {
		return nil, ErrSupplierPlatformInvalid
	}
	name, err := normalizeSupplierNameGeili(input.Name)
	if err != nil {
		return nil, err
	}
	concurrency := 3
	if input.Concurrency != nil {
		if err := validateSupplierConcurrencyGeili(input.Concurrency); err != nil {
			return nil, err
		}
		concurrency = *input.Concurrency
	}
	credentials := map[string]any{"api_key": strings.TrimSpace(input.APIKey)}
	if input.BaseURL != nil && strings.TrimSpace(*input.BaseURL) != "" {
		credentials["base_url"] = strings.TrimSpace(*input.BaseURL)
	}
	if err := validateSupplierCredentialsGeili(ctx, input.Platform, credentials); err != nil {
		return nil, err
	}
	createInput := &CreateAccountInput{
		Name:                 name,
		Notes:                input.Notes,
		Platform:             input.Platform,
		Type:                 AccountTypeAPIKey,
		Credentials:          credentials,
		Extra:                map[string]any{},
		Concurrency:          concurrency,
		Priority:             supplierAccountDefaultPriorityGeili,
		SkipDefaultGroupBind: true,
	}
	// The official service validates and normalizes; the hook replaces only the write.
	hooked := WithSubmissionAccountCreate(ctx, func(ctx context.Context, account *Account, groupIDs []int64) (*Account, error) {
		if len(groupIDs) > 0 {
			return nil, ErrSupplierAccountInvalid
		}
		if account.Extra == nil {
			account.Extra = map[string]any{}
		}
		account.Extra[SupplierAccountPublicEgressExtraKeyGeili] = true
		account.Status = "inactive"
		account.Schedulable = false
		account.ProxyID = nil
		account.GroupIDs = nil
		if err := s.repo.CreateSupplierAccount(ctx, account, ownerID); err != nil {
			return nil, err
		}
		return account, nil
	})
	return s.adminService.CreateAccount(hooked, createInput)
}

// Update edits supplier-managed fields. A new key or base URL pauses scheduling so the
// administrator re-checks the upstream before customers are routed to it again.
func (s *SupplierAccountServiceGeili) Update(ctx context.Context, ownerID, accountID int64, input SupplierAccountInputGeili) (*Account, error) {
	account, err := s.Get(ctx, ownerID, accountID)
	if err != nil {
		return nil, err
	}
	update := &UpdateAccountInput{Notes: input.Notes}
	if strings.TrimSpace(input.Name) != "" {
		name, err := normalizeSupplierNameGeili(input.Name)
		if err != nil {
			return nil, err
		}
		update.Name = name
	}
	if err := validateSupplierConcurrencyGeili(input.Concurrency); err != nil {
		return nil, err
	}
	update.Concurrency = input.Concurrency

	newKey := strings.TrimSpace(input.APIKey)
	keyChanged := newKey != "" && newKey != stringCredentialGeili(account.Credentials, "api_key")
	baseURLChanged := input.BaseURL != nil && strings.TrimSpace(*input.BaseURL) != stringCredentialGeili(account.Credentials, "base_url")
	credentialChanged := keyChanged || baseURLChanged
	if credentialChanged {
		if account.Type != AccountTypeAPIKey || account.IsShadow() {
			return nil, infraerrors.New(http.StatusBadRequest, "SUPPLIER_CREDENTIALS_READONLY", "only API Key credentials can be edited by suppliers")
		}
		credentials := make(map[string]any, len(account.Credentials)+2)
		for k, v := range account.Credentials {
			credentials[k] = v
		}
		if keyChanged {
			credentials["api_key"] = newKey
		}
		if baseURLChanged {
			if baseURL := strings.TrimSpace(*input.BaseURL); baseURL == "" {
				delete(credentials, "base_url")
			} else {
				credentials["base_url"] = baseURL
			}
		}
		if err := validateSupplierCredentialsGeili(ctx, account.Platform, credentials); err != nil {
			return nil, err
		}
		update.Credentials = credentials
		extra := make(map[string]any, len(account.Extra)+1)
		for k, v := range account.Extra {
			extra[k] = v
		}
		extra[SupplierAccountPublicEgressExtraKeyGeili] = true
		update.Extra = extra
	}
	if credentialChanged && account.Schedulable {
		// Pause first: a failure after this point leaves the account safely unschedulable.
		if _, err := s.adminService.SetAccountSchedulable(ctx, accountID, false); err != nil {
			return nil, err
		}
	}
	return s.adminService.UpdateAccount(ctx, accountID, update)
}

// Pause stops scheduling. Suppliers can never re-enable scheduling themselves.
func (s *SupplierAccountServiceGeili) Pause(ctx context.Context, ownerID, accountID int64) (*Account, error) {
	if _, err := s.Get(ctx, ownerID, accountID); err != nil {
		return nil, err
	}
	return s.adminService.SetAccountSchedulable(ctx, accountID, false)
}

func (s *SupplierAccountServiceGeili) Delete(ctx context.Context, ownerID, accountID int64) error {
	if _, err := s.Get(ctx, ownerID, accountID); err != nil {
		return err
	}
	return s.adminService.DeleteAccount(ctx, accountID)
}

// scopeUsageFilters forces the usage query onto the supplier's accounts. It returns
// false when nothing can match, so callers must not run an unscoped query.
func (s *SupplierAccountServiceGeili) scopeUsageFilters(ctx context.Context, ownerID int64, filters *usagestats.UsageLogFilters) (bool, error) {
	owned, err := s.repo.SupplierAccountIDs(ctx, ownerID)
	if err != nil {
		return false, err
	}
	requested := filters.AccountID
	filters.UserID, filters.APIKeyID, filters.SubscriptionID, filters.GroupID = 0, 0, 0, 0
	filters.AccountID = 0
	filters.AccountIDs = nil
	if requested > 0 {
		for _, id := range owned {
			if id == requested {
				filters.AccountIDs = []int64{requested}
				return true, nil
			}
		}
		return false, nil
	}
	if len(owned) == 0 {
		return false, nil
	}
	filters.AccountIDs = owned
	return true, nil
}

func (s *SupplierAccountServiceGeili) ListUsage(ctx context.Context, ownerID int64, params pagination.PaginationParams, filters usagestats.UsageLogFilters) ([]UsageLog, *pagination.PaginationResult, error) {
	ok, err := s.scopeUsageFilters(ctx, ownerID, &filters)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return []UsageLog{}, &pagination.PaginationResult{Total: 0, Page: params.Page, PageSize: params.Limit(), Pages: 0}, nil
	}
	filters.ExactTotal = true
	return s.usageService.ListWithFilters(ctx, params, filters)
}

func (s *SupplierAccountServiceGeili) UsageStats(ctx context.Context, ownerID int64, filters usagestats.UsageLogFilters) (*usagestats.UsageStats, error) {
	ok, err := s.scopeUsageFilters(ctx, ownerID, &filters)
	if err != nil {
		return nil, err
	}
	if !ok {
		zero := 0.0
		return &usagestats.UsageStats{TotalAccountCost: &zero}, nil
	}
	return s.usageService.GetStatsWithFilters(ctx, filters)
}

// SetOwner is the administrator path for assigning an existing account to a supplier.
func (s *SupplierAccountServiceGeili) SetOwner(ctx context.Context, accountID, ownerID int64) error {
	if _, err := s.adminService.GetAccount(ctx, accountID); err != nil {
		return err
	}
	if ownerID > 0 {
		user, err := s.userRepo.GetByID(ctx, ownerID)
		if err != nil || user == nil || user.Role != RoleSupplier {
			return ErrSupplierOwnerInvalid
		}
	}
	return s.repo.SetSupplierOwner(ctx, accountID, ownerID)
}

func (s *SupplierAccountServiceGeili) Owners(ctx context.Context, accountIDs []int64) (map[int64]SupplierAccountOwnerGeili, error) {
	return s.repo.SupplierOwners(ctx, accountIDs)
}

func stringCredentialGeili(credentials map[string]any, key string) string {
	v, _ := credentials[key].(string)
	return strings.TrimSpace(v)
}
