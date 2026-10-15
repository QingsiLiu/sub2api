package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type supplierRepoStubGeili struct {
	owned   map[int64][]int64
	created []*Account
	owner   int64
}

func (r *supplierRepoStubGeili) CreateSupplierAccount(_ context.Context, account *Account, ownerID int64) error {
	account.ID = 900 + int64(len(r.created))
	r.created = append(r.created, account)
	r.owner = ownerID
	return nil
}
func (r *supplierRepoStubGeili) SupplierAccountIDs(_ context.Context, ownerID int64) ([]int64, error) {
	return r.owned[ownerID], nil
}
func (r *supplierRepoStubGeili) IsLiveSupplierAccount(_ context.Context, ownerID, accountID int64) (bool, error) {
	for _, id := range r.owned[ownerID] {
		if id == accountID {
			return true, nil
		}
	}
	return false, nil
}
func (r *supplierRepoStubGeili) SetSupplierOwner(context.Context, int64, int64) error { return nil }
func (r *supplierRepoStubGeili) SupplierOwners(context.Context, []int64) (map[int64]SupplierAccountOwnerGeili, error) {
	return nil, nil
}

type supplierAdminStubGeili struct {
	AdminService
	accounts      map[int64]*Account
	listed        []Account
	listCtxOwner  int64
	listCtxScoped bool
	schedulable   []bool
	updates       []*UpdateAccountInput
	deleted       []int64
}

func (a *supplierAdminStubGeili) ListAccounts(ctx context.Context, _, _ int, _, _, _, _ string, _ int64, _, _, _ string) ([]Account, int64, error) {
	a.listCtxOwner, a.listCtxScoped = AccountSupplierOwnerFilterGeili(ctx)
	return a.listed, int64(len(a.listed)), nil
}
func (a *supplierAdminStubGeili) GetAccount(_ context.Context, id int64) (*Account, error) {
	if acc, ok := a.accounts[id]; ok {
		return acc, nil
	}
	return nil, ErrAccountNotFound
}
func (a *supplierAdminStubGeili) SetAccountSchedulable(_ context.Context, id int64, schedulable bool) (*Account, error) {
	a.schedulable = append(a.schedulable, schedulable)
	a.accounts[id].Schedulable = schedulable
	return a.accounts[id], nil
}
func (a *supplierAdminStubGeili) UpdateAccount(_ context.Context, id int64, input *UpdateAccountInput) (*Account, error) {
	a.updates = append(a.updates, input)
	return a.accounts[id], nil
}
func (a *supplierAdminStubGeili) DeleteAccount(_ context.Context, id int64) error {
	a.deleted = append(a.deleted, id)
	return nil
}
func (a *supplierAdminStubGeili) CreateAccount(ctx context.Context, input *CreateAccountInput) (*Account, error) {
	account, err := buildAccountForCreate(input, input.Extra)
	if err != nil {
		return nil, err
	}
	return submissionAccountCreate(ctx)(ctx, account, input.GroupIDs)
}

func newSupplierServiceForTestGeili() (*SupplierAccountServiceGeili, *supplierRepoStubGeili, *supplierAdminStubGeili) {
	repo := &supplierRepoStubGeili{owned: map[int64][]int64{7: {11, 12}, 8: {21}}}
	admin := &supplierAdminStubGeili{accounts: map[int64]*Account{
		11: {ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			Credentials: map[string]any{"api_key": "sk-old-key-123456", "model_mapping": map[string]any{"a": "b"}}, Extra: map[string]any{}},
		12: {ID: 12, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true},
		21: {ID: 21, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
	}}
	return NewSupplierAccountServiceGeili(repo, admin, nil, nil), repo, admin
}

func TestSupplierListScopesContextAndRejectsForeignRowsGeili(t *testing.T) {
	svc, _, admin := newSupplierServiceForTestGeili()
	admin.listed = []Account{{ID: 11}, {ID: 12}}
	out, total, err := svc.List(context.Background(), 7, 1, 20, "", "", "")
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.EqualValues(t, 2, total)
	require.True(t, admin.listCtxScoped)
	require.EqualValues(t, 7, admin.listCtxOwner)

	// A missing repository hook must fail closed instead of leaking another supplier's row.
	admin.listed = []Account{{ID: 11}, {ID: 21}}
	_, _, err = svc.List(context.Background(), 7, 1, 20, "", "", "")
	require.Error(t, err)
}

func TestSupplierForeignAccountLooksMissingGeili(t *testing.T) {
	svc, _, admin := newSupplierServiceForTestGeili()
	_, err := svc.Get(context.Background(), 7, 21)
	require.ErrorIs(t, err, ErrSupplierAccountNotFound)
	_, err = svc.Pause(context.Background(), 7, 21)
	require.ErrorIs(t, err, ErrSupplierAccountNotFound)
	require.ErrorIs(t, svc.Delete(context.Background(), 7, 21), ErrSupplierAccountNotFound)
	_, err = svc.Update(context.Background(), 7, 21, SupplierAccountInputGeili{Name: "x"})
	require.ErrorIs(t, err, ErrSupplierAccountNotFound)
	require.Empty(t, admin.schedulable)
	require.Empty(t, admin.updates)
	require.Empty(t, admin.deleted)
}

func TestSupplierCreateIsInactiveUngroupedAndPublicEgressGeili(t *testing.T) {
	svc, repo, _ := newSupplierServiceForTestGeili()
	account, err := svc.Create(context.Background(), 7, SupplierAccountInputGeili{Platform: PlatformOpenAI, Name: " my key ", APIKey: "sk-test-1234567890"})
	require.NoError(t, err)
	require.Equal(t, "inactive", account.Status)
	require.False(t, account.Schedulable)
	require.Nil(t, account.ProxyID)
	require.Empty(t, account.GroupIDs)
	require.Equal(t, "my key", account.Name)
	require.Equal(t, AccountTypeAPIKey, account.Type)
	require.Equal(t, true, account.Extra[SupplierAccountPublicEgressExtraKeyGeili])
	require.EqualValues(t, 7, repo.owner)

	_, err = svc.Create(context.Background(), 7, SupplierAccountInputGeili{Platform: PlatformAntigravity, Name: "x", APIKey: "sk-test-1234567890"})
	require.ErrorIs(t, err, ErrSupplierPlatformInvalid)
	_, err = svc.Create(context.Background(), 7, SupplierAccountInputGeili{Platform: PlatformOpenAI, Name: "x", APIKey: "bad key"})
	require.ErrorIs(t, err, ErrSupplierAccountInvalid)
	// Loopback / private base URLs are rejected before anything is written.
	local := "http://127.0.0.1:8080"
	_, err = svc.Create(context.Background(), 7, SupplierAccountInputGeili{Platform: PlatformOpenAI, Name: "x", APIKey: "sk-test-1234567890", BaseURL: &local})
	require.ErrorIs(t, err, ErrSupplierAccountInvalid)
	require.Len(t, repo.created, 1)
}

func TestSupplierCredentialChangePausesAndKeepsAdminCredentialsGeili(t *testing.T) {
	svc, _, admin := newSupplierServiceForTestGeili()
	concurrency := 5
	_, err := svc.Update(context.Background(), 7, 11, SupplierAccountInputGeili{Name: "renamed", Concurrency: &concurrency})
	require.NoError(t, err)
	require.Empty(t, admin.schedulable, "metadata edits keep scheduling")
	require.Nil(t, admin.updates[0].Credentials)

	_, err = svc.Update(context.Background(), 7, 11, SupplierAccountInputGeili{APIKey: "sk-new-key-654321"})
	require.NoError(t, err)
	require.Equal(t, []bool{false}, admin.schedulable)
	creds := admin.updates[1].Credentials
	require.Equal(t, "sk-new-key-654321", creds["api_key"])
	require.NotNil(t, creds["model_mapping"], "admin-managed credential keys survive")
	require.Equal(t, true, admin.updates[1].Extra[SupplierAccountPublicEgressExtraKeyGeili])
	require.Nil(t, admin.updates[1].GroupIDs)
	require.Nil(t, admin.updates[1].RateMultiplier)
	require.Nil(t, admin.updates[1].Priority)
	require.Empty(t, admin.updates[1].Status)

	_, err = svc.Update(context.Background(), 7, 12, SupplierAccountInputGeili{APIKey: "sk-new-key-654321"})
	require.Error(t, err, "non API-Key credentials are read-only")
	empty := ""
	_, err = svc.Update(context.Background(), 7, 12, SupplierAccountInputGeili{Name: "oauth renamed", BaseURL: &empty})
	require.NoError(t, err, "unchanged credential fields do not block metadata edits")
}

func TestSupplierUsageScopeNeverRunsUnscopedGeili(t *testing.T) {
	svc, _, _ := newSupplierServiceForTestGeili()
	filters := usagestats.UsageLogFilters{UserID: 99, APIKeyID: 98, GroupID: 3}
	ok, err := svc.scopeUsageFilters(context.Background(), 7, &filters)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []int64{11, 12}, filters.AccountIDs)
	require.Zero(t, filters.UserID)
	require.Zero(t, filters.APIKeyID)
	require.Zero(t, filters.GroupID)

	foreign := usagestats.UsageLogFilters{AccountID: 21}
	ok, err = svc.scopeUsageFilters(context.Background(), 7, &foreign)
	require.NoError(t, err)
	require.False(t, ok)

	none := usagestats.UsageLogFilters{}
	ok, err = svc.scopeUsageFilters(context.Background(), 404, &none)
	require.NoError(t, err)
	require.False(t, ok)
	logs, page, err := svc.ListUsage(context.Background(), 404, pagination.PaginationParams{Page: 1, PageSize: 20}, usagestats.UsageLogFilters{})
	require.NoError(t, err)
	require.Empty(t, logs)
	require.Zero(t, page.Total)
}
