-- Administrator-issued commercial invoices. The issuer profile is a single row
-- edited in the admin console, so bank details and the company seal never live
-- in the repository. Each invoice snapshots the issuer text it was issued with.
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS invoice_issuer_geili (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    company_name TEXT NOT NULL DEFAULT '',
    company_address TEXT NOT NULL DEFAULT '',
    company_contact TEXT NOT NULL DEFAULT '',
    number_prefix TEXT NOT NULL DEFAULT 'INV',
    payment_usd TEXT NOT NULL DEFAULT '',
    payment_hkd TEXT NOT NULL DEFAULT '',
    payment_cny TEXT NOT NULL DEFAULT '',
    footer_note TEXT NOT NULL DEFAULT '',
    seal_image TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO invoice_issuer_geili (id, company_name) VALUES (1, 'HONGVEX LIMITED')
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS invoices_geili (
    id BIGSERIAL PRIMARY KEY,
    invoice_no TEXT NOT NULL UNIQUE,
    issue_year INT NOT NULL,
    seq INT NOT NULL,
    issue_date DATE NOT NULL,
    currency TEXT NOT NULL CHECK (currency IN ('USD', 'HKD', 'CNY')),
    amount NUMERIC(14, 2) NOT NULL CHECK (amount > 0),
    description TEXT NOT NULL,
    bill_to_name TEXT NOT NULL DEFAULT '',
    bill_to_address TEXT NOT NULL DEFAULT '',
    notes TEXT NOT NULL DEFAULT '',
    issuer_name TEXT NOT NULL,
    issuer_address TEXT NOT NULL,
    issuer_contact TEXT NOT NULL,
    payment_details TEXT NOT NULL,
    footer_note TEXT NOT NULL,
    created_by BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    voided_at TIMESTAMPTZ,
    UNIQUE (issue_year, seq)
);
CREATE INDEX IF NOT EXISTS invoices_geili_created_idx ON invoices_geili (created_at DESC, id DESC);
