package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// Administrator-issued commercial invoices: one amount in USD/HKD/CNY, the
// issuer's per-currency payment instructions and an optional seal image.

var (
	ErrInvoiceNotFound       = infraerrors.NotFound("INVOICE_NOT_FOUND", "invoice not found")
	ErrInvoiceInvalid        = infraerrors.BadRequest("INVOICE_INVALID", "invalid invoice input")
	ErrInvoiceIssuerInvalid  = infraerrors.BadRequest("INVOICE_ISSUER_INVALID", "invalid invoice issuer input")
	ErrInvoiceIssuerNotReady = infraerrors.BadRequest("INVOICE_ISSUER_NOT_READY", "invoice issuer is not configured for this currency")
)

var InvoiceCurrenciesGeili = []string{"USD", "HKD", "CNY"}

const (
	invoiceSealMaxBytesGeili = 1 << 20
	invoiceTextMaxGeili      = 2000
	invoiceShortMaxGeili     = 200
)

var (
	invoiceAmountPatternGeili = regexp.MustCompile(`^(0|[1-9][0-9]{0,11})(\.[0-9]{1,2})?$`)
	invoicePrefixPatternGeili = regexp.MustCompile(`^[A-Z0-9]{1,12}$`)
	invoiceSealPrefixGeili    = regexp.MustCompile(`^data:(image/(?:png|jpeg|webp));base64,`)
)

type InvoiceIssuerGeili struct {
	CompanyName    string            `json:"company_name"`
	CompanyAddress string            `json:"company_address"`
	CompanyContact string            `json:"company_contact"`
	NumberPrefix   string            `json:"number_prefix"`
	Payment        map[string]string `json:"payment"`
	FooterNote     string            `json:"footer_note"`
	SealImage      string            `json:"seal_image"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// InvoiceIssuerUpdateGeili replaces the issuer text; SealImage nil keeps the
// stored seal, an empty string removes it.
type InvoiceIssuerUpdateGeili struct {
	CompanyName    string
	CompanyAddress string
	CompanyContact string
	NumberPrefix   string
	Payment        map[string]string
	FooterNote     string
	SealImage      *string
}

type InvoiceGeili struct {
	ID             int64      `json:"id"`
	InvoiceNo      string     `json:"invoice_no"`
	IssueDate      string     `json:"issue_date"`
	Currency       string     `json:"currency"`
	Amount         string     `json:"amount"`
	Description    string     `json:"description"`
	BillToName     string     `json:"bill_to_name"`
	BillToAddress  string     `json:"bill_to_address"`
	Notes          string     `json:"notes"`
	IssuerName     string     `json:"issuer_name"`
	IssuerAddress  string     `json:"issuer_address"`
	IssuerContact  string     `json:"issuer_contact"`
	PaymentDetails string     `json:"payment_details"`
	FooterNote     string     `json:"footer_note"`
	CreatedBy      int64      `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	VoidedAt       *time.Time `json:"voided_at"`
}

type InvoiceInputGeili struct {
	IssueDate     string
	Currency      string
	Amount        string
	Description   string
	BillToName    string
	BillToAddress string
	Notes         string
}

// InvoiceCreateGeili is a validated input; the repository allocates the number
// and snapshots the issuer inside one transaction.
type InvoiceCreateGeili struct {
	IssueDate     time.Time
	Currency      string
	Amount        string
	Description   string
	BillToName    string
	BillToAddress string
	Notes         string
	CreatedBy     int64
}

type InvoiceRepositoryGeili interface {
	GetIssuer(ctx context.Context) (*InvoiceIssuerGeili, error)
	UpdateIssuer(ctx context.Context, in InvoiceIssuerUpdateGeili) (*InvoiceIssuerGeili, error)
	CreateInvoice(ctx context.Context, in InvoiceCreateGeili) (*InvoiceGeili, error)
	ListInvoices(ctx context.Context, params pagination.PaginationParams) ([]InvoiceGeili, int64, error)
	GetInvoice(ctx context.Context, id int64) (*InvoiceGeili, error)
	VoidInvoice(ctx context.Context, id int64) (*InvoiceGeili, error)
}

type InvoiceServiceGeili struct {
	repo InvoiceRepositoryGeili
}

func NewInvoiceServiceGeili(repo InvoiceRepositoryGeili) *InvoiceServiceGeili {
	return &InvoiceServiceGeili{repo: repo}
}

func (s *InvoiceServiceGeili) GetIssuer(ctx context.Context) (*InvoiceIssuerGeili, error) {
	return s.repo.GetIssuer(ctx)
}

func (s *InvoiceServiceGeili) UpdateIssuer(ctx context.Context, in InvoiceIssuerUpdateGeili) (*InvoiceIssuerGeili, error) {
	in.CompanyName = strings.TrimSpace(in.CompanyName)
	in.CompanyAddress = strings.TrimSpace(in.CompanyAddress)
	in.CompanyContact = strings.TrimSpace(in.CompanyContact)
	in.NumberPrefix = strings.ToUpper(strings.TrimSpace(in.NumberPrefix))
	in.FooterNote = strings.TrimSpace(in.FooterNote)
	if in.CompanyName == "" || utf8.RuneCountInString(in.CompanyName) > invoiceShortMaxGeili {
		return nil, ErrInvoiceIssuerInvalid.WithMetadata(map[string]string{"field": "company_name"})
	}
	if !invoicePrefixPatternGeili.MatchString(in.NumberPrefix) {
		return nil, ErrInvoiceIssuerInvalid.WithMetadata(map[string]string{"field": "number_prefix"})
	}
	for field, value := range map[string]string{"company_address": in.CompanyAddress, "company_contact": in.CompanyContact, "footer_note": in.FooterNote} {
		if utf8.RuneCountInString(value) > invoiceTextMaxGeili {
			return nil, ErrInvoiceIssuerInvalid.WithMetadata(map[string]string{"field": field})
		}
	}
	payment := make(map[string]string, len(InvoiceCurrenciesGeili))
	for _, currency := range InvoiceCurrenciesGeili {
		value := strings.TrimSpace(in.Payment[currency])
		if utf8.RuneCountInString(value) > invoiceTextMaxGeili {
			return nil, ErrInvoiceIssuerInvalid.WithMetadata(map[string]string{"field": "payment." + currency})
		}
		payment[currency] = value
	}
	in.Payment = payment
	if in.SealImage != nil {
		seal := strings.TrimSpace(*in.SealImage)
		if seal != "" {
			if err := validateInvoiceSealGeili(seal); err != nil {
				return nil, err
			}
		}
		in.SealImage = &seal
	}
	return s.repo.UpdateIssuer(ctx, in)
}

// validateInvoiceSealGeili accepts a base64 data URL whose decoded bytes really
// are the declared PNG/JPEG/WebP image, bounded to 1 MiB.
func validateInvoiceSealGeili(seal string) error {
	invalid := ErrInvoiceIssuerInvalid.WithMetadata(map[string]string{"field": "seal_image"})
	m := invoiceSealPrefixGeili.FindStringSubmatch(seal)
	if m == nil {
		return invalid
	}
	payload := seal[len(m[0]):]
	if base64.StdEncoding.DecodedLen(len(payload)) > invoiceSealMaxBytesGeili+3 {
		return invalid
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(raw) == 0 || len(raw) > invoiceSealMaxBytesGeili {
		return invalid
	}
	if http.DetectContentType(raw) != m[1] {
		return invalid
	}
	return nil
}

func (s *InvoiceServiceGeili) Create(ctx context.Context, adminID int64, in InvoiceInputGeili) (*InvoiceGeili, error) {
	create, err := normalizeInvoiceInputGeili(in, timezone.Now())
	if err != nil {
		return nil, err
	}
	create.CreatedBy = adminID
	return s.repo.CreateInvoice(ctx, create)
}

func normalizeInvoiceInputGeili(in InvoiceInputGeili, now time.Time) (InvoiceCreateGeili, error) {
	invalid := func(field string) (InvoiceCreateGeili, error) {
		return InvoiceCreateGeili{}, ErrInvoiceInvalid.WithMetadata(map[string]string{"field": field})
	}
	out := InvoiceCreateGeili{
		Currency:      strings.ToUpper(strings.TrimSpace(in.Currency)),
		Amount:        strings.TrimSpace(in.Amount),
		Description:   strings.TrimSpace(in.Description),
		BillToName:    strings.TrimSpace(in.BillToName),
		BillToAddress: strings.TrimSpace(in.BillToAddress),
		Notes:         strings.TrimSpace(in.Notes),
	}
	if !invoiceCurrencySupportedGeili(out.Currency) {
		return invalid("currency")
	}
	if !invoiceAmountPatternGeili.MatchString(out.Amount) || strings.Trim(out.Amount, "0.") == "" {
		return invalid("amount")
	}
	if out.Description == "" || utf8.RuneCountInString(out.Description) > invoiceTextMaxGeili {
		return invalid("description")
	}
	if utf8.RuneCountInString(out.BillToName) > invoiceShortMaxGeili {
		return invalid("bill_to_name")
	}
	if utf8.RuneCountInString(out.BillToAddress) > invoiceTextMaxGeili {
		return invalid("bill_to_address")
	}
	if utf8.RuneCountInString(out.Notes) > invoiceTextMaxGeili {
		return invalid("notes")
	}
	date := strings.TrimSpace(in.IssueDate)
	if date == "" {
		y, m, d := now.Date()
		out.IssueDate = time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	} else {
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil || parsed.Year() < 2000 || parsed.Year() > 9999 {
			return invalid("issue_date")
		}
		out.IssueDate = parsed
	}
	return out, nil
}

func invoiceCurrencySupportedGeili(currency string) bool {
	for _, c := range InvoiceCurrenciesGeili {
		if c == currency {
			return true
		}
	}
	return false
}

// FormatInvoiceNumberGeili renders PREFIX-YYYY-NNNN; the sequence restarts each year.
func FormatInvoiceNumberGeili(prefix string, year, seq int) string {
	return fmt.Sprintf("%s-%d-%04d", prefix, year, seq)
}

func (s *InvoiceServiceGeili) List(ctx context.Context, params pagination.PaginationParams) ([]InvoiceGeili, int64, error) {
	return s.repo.ListInvoices(ctx, params)
}

func (s *InvoiceServiceGeili) Get(ctx context.Context, id int64) (*InvoiceGeili, error) {
	return s.repo.GetInvoice(ctx, id)
}

func (s *InvoiceServiceGeili) Void(ctx context.Context, id int64) (*InvoiceGeili, error) {
	return s.repo.VoidInvoice(ctx, id)
}
