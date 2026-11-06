//go:build integration

package repository

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestInvoiceGeiliIntegrationNumberingIssuerAndVoid(t *testing.T) {
	ctx := context.Background()
	repo := NewInvoiceRepositoryGeili(integrationDB)
	_, err := integrationDB.ExecContext(ctx, `DELETE FROM invoices_geili WHERE issue_year IN (2093, 2094, 2095)`)
	require.NoError(t, err)

	seal := "data:image/png;base64,iVBORw0KGgo="
	_, err = repo.UpdateIssuer(ctx, service.InvoiceIssuerUpdateGeili{
		CompanyName: "HONGVEX LIMITED", CompanyAddress: "Hong Kong", NumberPrefix: "HVT",
		Payment:   map[string]string{"USD": "Wise USD details", "HKD": "Wise HKD details", "CNY": ""},
		SealImage: &seal,
	})
	require.NoError(t, err)

	date := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		require.NoError(t, err)
		return d
	}
	create := func(issueDate, currency string) (*service.InvoiceGeili, error) {
		return repo.CreateInvoice(ctx, service.InvoiceCreateGeili{
			IssueDate: date(issueDate), Currency: currency, Amount: "1234.50", Description: "API service", CreatedBy: 1,
		})
	}

	first, err := create("2093-03-01", "USD")
	require.NoError(t, err)
	require.Equal(t, "HVT-2093-0001", first.InvoiceNo)
	require.Equal(t, "2093-03-01", first.IssueDate)
	require.Equal(t, "1234.50", first.Amount)
	require.Equal(t, "HONGVEX LIMITED", first.IssuerName)
	require.Equal(t, "Wise USD details", first.PaymentDetails)

	second, err := create("2093-12-31", "HKD")
	require.NoError(t, err)
	require.Equal(t, "HVT-2093-0002", second.InvoiceNo)
	require.Equal(t, "Wise HKD details", second.PaymentDetails)

	nextYear, err := create("2094-01-01", "USD")
	require.NoError(t, err)
	require.Equal(t, "HVT-2094-0001", nextYear.InvoiceNo, "sequence restarts each year")

	_, err = create("2093-05-05", "CNY")
	require.ErrorIs(t, err, service.ErrInvoiceIssuerNotReady, "currency without payment details is refused")

	// Updating text without a seal keeps it; issued invoices keep their snapshot.
	issuer, err := repo.UpdateIssuer(ctx, service.InvoiceIssuerUpdateGeili{
		CompanyName: "HONGVEX LIMITED", NumberPrefix: "HVT",
		Payment: map[string]string{"USD": "New USD details", "HKD": "Wise HKD details", "CNY": "CNY details"},
	})
	require.NoError(t, err)
	require.Equal(t, seal, issuer.SealImage)
	got, err := repo.GetInvoice(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, "Wise USD details", got.PaymentDetails)
	require.Equal(t, "Hong Kong", got.IssuerAddress)

	var wg sync.WaitGroup
	numbers := make([]string, 8)
	errs := make([]error, 8)
	for i := range numbers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			inv, err := create("2095-06-01", "CNY")
			errs[i] = err
			if err == nil {
				numbers[i] = inv.InvoiceNo
			}
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	sort.Strings(numbers)
	require.Equal(t, []string{
		"HVT-2095-0001", "HVT-2095-0002", "HVT-2095-0003", "HVT-2095-0004",
		"HVT-2095-0005", "HVT-2095-0006", "HVT-2095-0007", "HVT-2095-0008",
	}, numbers, "concurrent issuing yields gapless unique numbers")

	voided, err := repo.VoidInvoice(ctx, second.ID)
	require.NoError(t, err)
	require.NotNil(t, voided.VoidedAt)
	again, err := repo.VoidInvoice(ctx, second.ID)
	require.NoError(t, err)
	require.True(t, again.VoidedAt.Equal(*voided.VoidedAt), "repeat void keeps the first time")
	require.Equal(t, "HVT-2093-0002", again.InvoiceNo)

	_, err = repo.GetInvoice(ctx, -1)
	require.ErrorIs(t, err, service.ErrInvoiceNotFound)
	_, err = repo.VoidInvoice(ctx, -1)
	require.ErrorIs(t, err, service.ErrInvoiceNotFound)

	items, total, err := repo.ListInvoices(ctx, pagination.PaginationParams{Page: 1, PageSize: 5})
	require.NoError(t, err)
	require.GreaterOrEqual(t, total, int64(11))
	require.Len(t, items, 5)
}
