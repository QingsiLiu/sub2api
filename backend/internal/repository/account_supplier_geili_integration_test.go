//go:build integration

package repository

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountSupplierGeiliIntegrationOwnershipScopesListsAndEgress(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	supplierA := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("supplier-a-%d@example.com", suffix), Role: service.RoleSupplier})
	supplierB := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("supplier-b-%d@example.com", suffix), Role: service.RoleSupplier})
	plain := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("plain-%d@example.com", suffix)})
	repo := NewSupplierAccountRepositoryGeili(integrationEntClient, integrationDB)
	accounts := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)

	newAccount := func(name string) *service.Account {
		return &service.Account{Name: fmt.Sprintf("%s-%d", name, suffix), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: "inactive", Credentials: map[string]any{"api_key": "sk-test"},
			Extra: map[string]any{service.SupplierAccountPublicEgressExtraKeyGeili: true}}
	}
	a := newAccount("supplier-a")
	require.NoError(t, repo.CreateSupplierAccount(ctx, a, supplierA.ID))
	b := newAccount("supplier-b")
	require.NoError(t, repo.CreateSupplierAccount(ctx, b, supplierB.ID))
	other := runtimeIntegrationAccountGeili(t, accounts)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`DELETE FROM accounts WHERE id = ANY($1::bigint[])`, fmt.Sprintf("{%d,%d}", a.ID, b.ID))
	})

	// Only supplier users may own new accounts; the failed write leaves nothing behind.
	rejected := newAccount("plain")
	require.ErrorIs(t, repo.CreateSupplierAccount(ctx, rejected, plain.ID), service.ErrSupplierOwnerInvalid)
	var leaked int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM accounts WHERE name=$1`, rejected.Name).Scan(&leaked))
	require.Zero(t, leaked)

	scoped := service.WithAccountSupplierOwnerFilterGeili(ctx, supplierA.ID)
	listed, page, err := accounts.ListWithFilters(scoped, pagination.PaginationParams{Page: 1, PageSize: 100}, "", "", "", "", 0, "")
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.Len(t, listed, 1)
	require.Equal(t, a.ID, listed[0].ID)
	all, err := accounts.ListAllWithFilters(scoped, "", "", "", "", 0, "")
	require.NoError(t, err)
	require.Len(t, all, 1)

	ok, err := repo.IsLiveSupplierAccount(ctx, supplierA.ID, a.ID)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.IsLiveSupplierAccount(ctx, supplierA.ID, b.ID)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = repo.IsLiveSupplierAccount(ctx, supplierA.ID, other.ID)
	require.NoError(t, err)
	require.False(t, ok)

	// Administrators can reassign and clear; the owner shows up in presentation reads.
	require.NoError(t, repo.SetSupplierOwner(ctx, other.ID, supplierA.ID))
	ids, err := repo.SupplierAccountIDs(ctx, supplierA.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{a.ID, other.ID}, ids)
	presented, err := accounts.GetByIDs(service.WithAccountRuntimePresentationGeili(ctx), []int64{other.ID})
	require.NoError(t, err)
	require.NotNil(t, presented[0].SupplierOwner)
	require.Equal(t, supplierA.Email, presented[0].SupplierOwner.Email)
	require.NoError(t, repo.SetSupplierOwner(ctx, other.ID, 0))
	ids, err = repo.SupplierAccountIDs(ctx, supplierA.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{a.ID}, ids)

	// Soft-deleted accounts keep usage history visible but are no longer manageable.
	require.NoError(t, accounts.Delete(ctx, a.ID))
	ok, err = repo.IsLiveSupplierAccount(ctx, supplierA.ID, a.ID)
	require.NoError(t, err)
	require.False(t, ok)
	ids, err = repo.SupplierAccountIDs(ctx, supplierA.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{a.ID}, ids)

	// Supplier accounts get the public-egress dial policy; ordinary accounts do not.
	upstream := ProvideSubmissionHTTPUpstreamGeili(&config.Config{}, integrationDB).(*httpUpstreamService)
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:9/v1/models", nil)
	require.NoError(t, err)
	_, err = upstream.submissionProxyPolicyGeili(req, "", b.ID)
	require.ErrorIs(t, err, service.ErrSubmissionInvalid)
	raw, err := upstream.submissionProxyPolicyGeili(req, "", other.ID)
	require.NoError(t, err)
	require.Empty(t, raw)
}
