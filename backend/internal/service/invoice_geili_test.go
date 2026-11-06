package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type invoiceRepoStubGeili struct {
	update *InvoiceIssuerUpdateGeili
	create *InvoiceCreateGeili
}

func (r *invoiceRepoStubGeili) GetIssuer(context.Context) (*InvoiceIssuerGeili, error) {
	return &InvoiceIssuerGeili{}, nil
}
func (r *invoiceRepoStubGeili) UpdateIssuer(_ context.Context, in InvoiceIssuerUpdateGeili) (*InvoiceIssuerGeili, error) {
	r.update = &in
	return &InvoiceIssuerGeili{CompanyName: in.CompanyName}, nil
}
func (r *invoiceRepoStubGeili) CreateInvoice(_ context.Context, in InvoiceCreateGeili) (*InvoiceGeili, error) {
	r.create = &in
	return &InvoiceGeili{Currency: in.Currency, Amount: in.Amount}, nil
}
func (r *invoiceRepoStubGeili) ListInvoices(context.Context, pagination.PaginationParams) ([]InvoiceGeili, int64, error) {
	return nil, 0, nil
}
func (r *invoiceRepoStubGeili) GetInvoice(context.Context, int64) (*InvoiceGeili, error) {
	return nil, ErrInvoiceNotFound
}
func (r *invoiceRepoStubGeili) VoidInvoice(context.Context, int64) (*InvoiceGeili, error) {
	return nil, ErrInvoiceNotFound
}

func invoicePNGDataURLGeili(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))))
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestInvoiceGeiliNormalizeInput(t *testing.T) {
	now := time.Date(2026, 11, 6, 23, 30, 0, 0, time.FixedZone("CST", 8*3600))
	base := InvoiceInputGeili{Currency: "usd", Amount: "1200.50", Description: " API service "}

	out, err := normalizeInvoiceInputGeili(base, now)
	require.NoError(t, err)
	require.Equal(t, "USD", out.Currency)
	require.Equal(t, "1200.50", out.Amount)
	require.Equal(t, "API service", out.Description)
	require.Equal(t, "2026-11-06", out.IssueDate.Format("2006-01-02"), "default date is today in the server zone")

	withDate := base
	withDate.IssueDate = "2025-12-31"
	out, err = normalizeInvoiceInputGeili(withDate, now)
	require.NoError(t, err)
	require.Equal(t, 2025, out.IssueDate.Year())

	for name, mutate := range map[string]func(*InvoiceInputGeili){
		"unsupported currency": func(in *InvoiceInputGeili) { in.Currency = "EUR" },
		"zero amount":          func(in *InvoiceInputGeili) { in.Amount = "0.00" },
		"negative amount":      func(in *InvoiceInputGeili) { in.Amount = "-5" },
		"three decimals":       func(in *InvoiceInputGeili) { in.Amount = "1.005" },
		"leading zero":         func(in *InvoiceInputGeili) { in.Amount = "012" },
		"exponent":             func(in *InvoiceInputGeili) { in.Amount = "1e3" },
		"too large":            func(in *InvoiceInputGeili) { in.Amount = "1000000000000" },
		"empty description":    func(in *InvoiceInputGeili) { in.Description = "  " },
		"bad date":             func(in *InvoiceInputGeili) { in.IssueDate = "2026/11/06" },
		"long bill-to name":    func(in *InvoiceInputGeili) { in.BillToName = strings.Repeat("x", 201) },
	} {
		in := base
		mutate(&in)
		_, err := normalizeInvoiceInputGeili(in, now)
		require.ErrorIs(t, err, ErrInvoiceInvalid, name)
	}

	for _, amount := range []string{"0.01", "5", "999999999999.99"} {
		in := base
		in.Amount = amount
		_, err := normalizeInvoiceInputGeili(in, now)
		require.NoError(t, err, amount)
	}
}

func TestInvoiceGeiliIssuerValidation(t *testing.T) {
	repo := &invoiceRepoStubGeili{}
	svc := NewInvoiceServiceGeili(repo)
	ctx := context.Background()
	seal := invoicePNGDataURLGeili(t)

	_, err := svc.UpdateIssuer(ctx, InvoiceIssuerUpdateGeili{
		CompanyName: " HONGVEX LIMITED ", NumberPrefix: "hv",
		Payment:   map[string]string{"USD": " Wise USD ", "EUR": "ignored"},
		SealImage: &seal,
	})
	require.NoError(t, err)
	require.Equal(t, "HONGVEX LIMITED", repo.update.CompanyName)
	require.Equal(t, "HV", repo.update.NumberPrefix)
	require.Equal(t, map[string]string{"USD": "Wise USD", "HKD": "", "CNY": ""}, repo.update.Payment)
	require.Equal(t, seal, *repo.update.SealImage)

	_, err = svc.UpdateIssuer(ctx, InvoiceIssuerUpdateGeili{CompanyName: "A", NumberPrefix: "INV"})
	require.NoError(t, err)
	require.Nil(t, repo.update.SealImage, "omitted seal keeps the stored one")

	empty := ""
	_, err = svc.UpdateIssuer(ctx, InvoiceIssuerUpdateGeili{CompanyName: "A", NumberPrefix: "INV", SealImage: &empty})
	require.NoError(t, err)
	require.Equal(t, "", *repo.update.SealImage, "empty seal removes it")

	jpegLabel := strings.Replace(seal, "image/png", "image/jpeg", 1)
	html := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("<html><script>alert(1)</script>"))
	huge := "data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, invoiceSealMaxBytesGeili+1))
	for name, in := range map[string]InvoiceIssuerUpdateGeili{
		"empty company":      {CompanyName: " ", NumberPrefix: "INV"},
		"prefix with dash":   {CompanyName: "A", NumberPrefix: "IN-V"},
		"prefix too long":    {CompanyName: "A", NumberPrefix: strings.Repeat("A", 13)},
		"svg seal":           {CompanyName: "A", NumberPrefix: "INV", SealImage: ptrInvoiceGeili("data:image/svg+xml;base64,PHN2Zy8+")},
		"mislabeled seal":    {CompanyName: "A", NumberPrefix: "INV", SealImage: &jpegLabel},
		"html as png":        {CompanyName: "A", NumberPrefix: "INV", SealImage: &html},
		"oversized seal":     {CompanyName: "A", NumberPrefix: "INV", SealImage: &huge},
		"not base64":         {CompanyName: "A", NumberPrefix: "INV", SealImage: ptrInvoiceGeili("data:image/png;base64,@@@")},
		"long payment field": {CompanyName: "A", NumberPrefix: "INV", Payment: map[string]string{"HKD": strings.Repeat("x", 2001)}},
	} {
		_, err := svc.UpdateIssuer(ctx, in)
		require.ErrorIs(t, err, ErrInvoiceIssuerInvalid, name)
	}
}

func TestInvoiceGeiliCreateRecordsAdmin(t *testing.T) {
	repo := &invoiceRepoStubGeili{}
	_, err := NewInvoiceServiceGeili(repo).Create(context.Background(), 7, InvoiceInputGeili{Currency: "HKD", Amount: "88", Description: "Service"})
	require.NoError(t, err)
	require.Equal(t, int64(7), repo.create.CreatedBy)
	require.Equal(t, "HKD", repo.create.Currency)
}

func TestInvoiceGeiliNumberFormat(t *testing.T) {
	require.Equal(t, "HV-2026-0001", FormatInvoiceNumberGeili("HV", 2026, 1))
	require.Equal(t, "INV-2026-12345", FormatInvoiceNumberGeili("INV", 2026, 12345))
}

func ptrInvoiceGeili(s string) *string { return &s }
