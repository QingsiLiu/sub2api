package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type invoiceRepositoryGeili struct {
	db *sql.DB
}

func NewInvoiceRepositoryGeili(db *sql.DB) service.InvoiceRepositoryGeili {
	return &invoiceRepositoryGeili{db: db}
}

const invoiceIssuerColumnsGeili = `company_name, company_address, company_contact, number_prefix,
	payment_usd, payment_hkd, payment_cny, footer_note, seal_image, updated_at`

type invoiceRowScannerGeili interface {
	Scan(dest ...any) error
}

func scanInvoiceIssuerGeili(row invoiceRowScannerGeili) (*service.InvoiceIssuerGeili, error) {
	var out service.InvoiceIssuerGeili
	var usd, hkd, cny string
	if err := row.Scan(&out.CompanyName, &out.CompanyAddress, &out.CompanyContact, &out.NumberPrefix,
		&usd, &hkd, &cny, &out.FooterNote, &out.SealImage, &out.UpdatedAt); err != nil {
		return nil, err
	}
	out.Payment = map[string]string{"USD": usd, "HKD": hkd, "CNY": cny}
	return &out, nil
}

func (r *invoiceRepositoryGeili) GetIssuer(ctx context.Context) (*service.InvoiceIssuerGeili, error) {
	issuer, err := scanInvoiceIssuerGeili(r.db.QueryRowContext(ctx,
		`SELECT `+invoiceIssuerColumnsGeili+` FROM invoice_issuer_geili WHERE id = 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return &service.InvoiceIssuerGeili{NumberPrefix: "INV", Payment: map[string]string{"USD": "", "HKD": "", "CNY": ""}}, nil
	}
	return issuer, err
}

func (r *invoiceRepositoryGeili) UpdateIssuer(ctx context.Context, in service.InvoiceIssuerUpdateGeili) (*service.InvoiceIssuerGeili, error) {
	// $9 NULL keeps the stored seal; the insert branch then starts without one.
	var seal sql.NullString
	if in.SealImage != nil {
		seal = sql.NullString{String: *in.SealImage, Valid: true}
	}
	return scanInvoiceIssuerGeili(r.db.QueryRowContext(ctx, `
INSERT INTO invoice_issuer_geili (id, company_name, company_address, company_contact, number_prefix,
	payment_usd, payment_hkd, payment_cny, footer_note, seal_image, updated_at)
VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, ''), NOW())
ON CONFLICT (id) DO UPDATE SET
	company_name = EXCLUDED.company_name,
	company_address = EXCLUDED.company_address,
	company_contact = EXCLUDED.company_contact,
	number_prefix = EXCLUDED.number_prefix,
	payment_usd = EXCLUDED.payment_usd,
	payment_hkd = EXCLUDED.payment_hkd,
	payment_cny = EXCLUDED.payment_cny,
	footer_note = EXCLUDED.footer_note,
	seal_image = COALESCE($9, invoice_issuer_geili.seal_image),
	updated_at = NOW()
RETURNING `+invoiceIssuerColumnsGeili,
		in.CompanyName, in.CompanyAddress, in.CompanyContact, in.NumberPrefix,
		in.Payment["USD"], in.Payment["HKD"], in.Payment["CNY"], in.FooterNote, seal))
}

const invoiceColumnsGeili = `id, invoice_no, to_char(issue_date, 'YYYY-MM-DD'), currency, amount::text,
	description, bill_to_name, bill_to_address, notes, issuer_name, issuer_address, issuer_contact,
	payment_details, footer_note, created_by, created_at, voided_at`

func scanInvoiceGeili(row invoiceRowScannerGeili) (*service.InvoiceGeili, error) {
	var out service.InvoiceGeili
	var voided sql.NullTime
	if err := row.Scan(&out.ID, &out.InvoiceNo, &out.IssueDate, &out.Currency, &out.Amount,
		&out.Description, &out.BillToName, &out.BillToAddress, &out.Notes, &out.IssuerName, &out.IssuerAddress, &out.IssuerContact,
		&out.PaymentDetails, &out.FooterNote, &out.CreatedBy, &out.CreatedAt, &voided); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrInvoiceNotFound
		}
		return nil, err
	}
	if voided.Valid {
		t := voided.Time
		out.VoidedAt = &t
	}
	return &out, nil
}

func (r *invoiceRepositoryGeili) CreateInvoice(ctx context.Context, in service.InvoiceCreateGeili) (*service.InvoiceGeili, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// The issuer row lock serializes number allocation so numbers stay gapless.
	issuer, err := scanInvoiceIssuerGeili(tx.QueryRowContext(ctx,
		`SELECT `+invoiceIssuerColumnsGeili+` FROM invoice_issuer_geili WHERE id = 1 FOR UPDATE`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrInvoiceIssuerNotReady
	}
	if err != nil {
		return nil, err
	}
	payment := issuer.Payment[in.Currency]
	if issuer.CompanyName == "" || payment == "" {
		return nil, service.ErrInvoiceIssuerNotReady.WithMetadata(map[string]string{"currency": in.Currency})
	}
	year := in.IssueDate.Year()
	var seq int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM invoices_geili WHERE issue_year = $1`, year).Scan(&seq); err != nil {
		return nil, err
	}
	invoice, err := scanInvoiceGeili(tx.QueryRowContext(ctx, `
INSERT INTO invoices_geili (invoice_no, issue_year, seq, issue_date, currency, amount, description,
	bill_to_name, bill_to_address, notes, issuer_name, issuer_address, issuer_contact,
	payment_details, footer_note, created_by)
VALUES ($1, $2, $3, $4::date, $5, $6::numeric, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
RETURNING `+invoiceColumnsGeili,
		service.FormatInvoiceNumberGeili(issuer.NumberPrefix, year, seq), year, seq,
		in.IssueDate.Format("2006-01-02"), in.Currency, in.Amount, in.Description,
		in.BillToName, in.BillToAddress, in.Notes, issuer.CompanyName, issuer.CompanyAddress, issuer.CompanyContact,
		payment, issuer.FooterNote, in.CreatedBy))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return invoice, nil
}

func (r *invoiceRepositoryGeili) ListInvoices(ctx context.Context, params pagination.PaginationParams) ([]service.InvoiceGeili, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM invoices_geili`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+invoiceColumnsGeili+` FROM invoices_geili ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2`,
		params.Limit(), params.Offset())
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.InvoiceGeili, 0, params.Limit())
	for rows.Next() {
		invoice, err := scanInvoiceGeili(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *invoice)
	}
	return out, total, rows.Err()
}

func (r *invoiceRepositoryGeili) GetInvoice(ctx context.Context, id int64) (*service.InvoiceGeili, error) {
	return scanInvoiceGeili(r.db.QueryRowContext(ctx,
		`SELECT `+invoiceColumnsGeili+` FROM invoices_geili WHERE id = $1`, id))
}

// VoidInvoice keeps the number and content; repeating it keeps the first void time.
func (r *invoiceRepositoryGeili) VoidInvoice(ctx context.Context, id int64) (*service.InvoiceGeili, error) {
	return scanInvoiceGeili(r.db.QueryRowContext(ctx,
		`UPDATE invoices_geili SET voided_at = COALESCE(voided_at, NOW()) WHERE id = $1 RETURNING `+invoiceColumnsGeili, id))
}
