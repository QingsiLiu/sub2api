package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type accountSubmissionRepository struct{ db *sql.DB }

func NewAccountSubmissionRepository(db *sql.DB) service.AccountSubmissionRepository {
	return &accountSubmissionRepository{db: db}
}

const submissionColumns = `id, created_by, config, created_at, expires_at, revoked_at, submitted_at, account_id, view_revoked_at`

type submissionScanner interface{ Scan(...any) error }

func scanSubmission(row submissionScanner) (*service.AccountSubmissionInvite, error) {
	return scanSubmissionCount(row, false)
}
func scanSubmissionCount(row submissionScanner, withCount bool) (*service.AccountSubmissionInvite, error) {
	i := &service.AccountSubmissionInvite{}
	var config []byte
	dest := []any{&i.ID, &i.CreatedBy, &config, &i.CreatedAt, &i.ExpiresAt, &i.RevokedAt, &i.SubmittedAt, &i.AccountID, &i.ViewRevokedAt}
	if withCount {
		dest = append(dest, &i.AccountCount)
	}
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrSubmissionInvalid
		}
		return nil, service.ErrSubmissionUnavailable
	}
	if json.Unmarshal(config, &i.Config) != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	i.Status = i.State(time.Now())
	return i, nil
}

// Lock references until the invite/account transaction commits. Platform and expiry changes
// between invitation creation and submission must never create an invalid binding.
func validateSubmissionReferences(ctx context.Context, tx *sql.Tx, c service.AccountSubmissionConfig) error {
	ids := append([]int64(nil), c.GroupIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		var platform, status string
		if err := tx.QueryRowContext(ctx, `SELECT platform, status FROM groups WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, id).Scan(&platform, &status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return service.ErrSubmissionConfig
			}
			return service.ErrSubmissionUnavailable
		}
		if platform != c.Platform || status != service.StatusActive {
			return service.ErrSubmissionConfig
		}
	}
	if c.ProxyID != nil {
		var usable bool
		err := tx.QueryRowContext(ctx, `SELECT status='active' AND (expires_at IS NULL OR expires_at>clock_timestamp()) FROM proxies WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, *c.ProxyID).Scan(&usable)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !usable) {
			return service.ErrSubmissionConfig
		}
		if err != nil {
			return service.ErrSubmissionUnavailable
		}
	}
	return nil
}

func (r *accountSubmissionRepository) CreateInvite(ctx context.Context, hash string, actor int64, c service.AccountSubmissionConfig) (*service.AccountSubmissionInvite, error) {
	if err := service.ValidateAccountSubmissionConfig(&c); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateSubmissionReferences(ctx, tx, c); err != nil {
		return nil, err
	}
	config, err := json.Marshal(c)
	if err != nil {
		return nil, service.ErrSubmissionConfig
	}
	invite, err := scanSubmission(tx.QueryRowContext(ctx, `INSERT INTO account_submission_invites_geili (token_hash,created_by,config,expires_at) VALUES ($1,$2,$3,clock_timestamp()+$4::integer*interval '1 hour') RETURNING `+submissionColumns, hash, actor, string(config), c.ExpiryHours()))
	if err != nil {
		return nil, err
	}
	if tx.Commit() != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	return invite, nil
}

func (r *accountSubmissionRepository) InspectInvite(ctx context.Context, hash string) (*service.AccountSubmissionInvite, error) {
	return scanSubmission(r.db.QueryRowContext(ctx, `SELECT `+submissionColumns+` FROM account_submission_invites_geili WHERE token_hash=$1`, hash))
}

func (r *accountSubmissionRepository) ListInvites(ctx context.Context, page, size int) ([]service.AccountSubmissionInvite, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM account_submission_invites_geili WHERE parent_invite_id IS NULL`).Scan(&total); err != nil {
		return nil, 0, service.ErrSubmissionUnavailable
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+submissionColumns+`,(SELECT count(*) FROM account_submission_invites_geili m WHERE (m.id=i.id OR m.parent_invite_id=i.id) AND m.account_id IS NOT NULL) FROM account_submission_invites_geili i WHERE parent_invite_id IS NULL ORDER BY id DESC LIMIT $1 OFFSET $2`, size, (page-1)*size)
	if err != nil {
		return nil, 0, service.ErrSubmissionUnavailable
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.AccountSubmissionInvite, 0)
	for rows.Next() {
		i, err := scanSubmissionCount(rows, true)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *i)
	}
	if rows.Err() != nil {
		return nil, 0, service.ErrSubmissionUnavailable
	}
	return items, total, nil
}

func (r *accountSubmissionRepository) RevokeInvite(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `UPDATE account_submission_invites_geili SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$1 AND submitted_at IS NULL`, id)
	if err != nil {
		return service.ErrSubmissionUnavailable
	}
	count, err := result.RowsAffected()
	if err != nil {
		return service.ErrSubmissionUnavailable
	}
	if count == 0 {
		return service.ErrSubmissionInactive
	}
	return nil
}

func (r *accountSubmissionRepository) SubmitInvite(ctx context.Context, hash, key string) error {
	return r.SubmitInviteWithOptions(ctx, hash, key, service.SubmissionOptions{})
}
func (r *accountSubmissionRepository) SubmitInviteWithOptions(ctx context.Context, hash, key string, options service.SubmissionOptions) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.ErrSubmissionUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	invite, err := scanSubmission(tx.QueryRowContext(ctx, `SELECT `+submissionColumns+` FROM account_submission_invites_geili WHERE token_hash=$1 FOR UPDATE`, hash))
	if err != nil {
		return err
	}
	if invite.SubmittedAt != nil {
		return nil
	}
	// Database time is authoritative, including after waiting for a concurrent transaction.
	var usable bool
	if err := tx.QueryRowContext(ctx, `SELECT revoked_at IS NULL AND expires_at>clock_timestamp() FROM account_submission_invites_geili WHERE id=$1`, invite.ID).Scan(&usable); err != nil {
		return service.ErrSubmissionUnavailable
	}
	if !usable {
		return service.ErrSubmissionInactive
	}
	if err := createSubmittedAccount(ctx, tx, invite, key, options); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return service.ErrSubmissionUnavailable
	}
	return nil
}

// Shared transaction body: batching must keep accounts, provenance, proxies and outbox atomic.
func createSubmittedAccount(ctx context.Context, tx *sql.Tx, invite *service.AccountSubmissionInvite, key string, options service.SubmissionOptions) error {
	return createSubmittedAccountInBatch(ctx, tx, invite, key, options, false)
}
func createSubmittedAccountInBatch(ctx context.Context, tx *sql.Tx, invite *service.AccountSubmissionInvite, key string, options service.SubmissionOptions, referencesLocked bool) error {
	var err error
	c := invite.Config
	prepared := options.PreparedAccount
	if prepared != nil {
		if prepared.Platform != c.Platform {
			return service.ErrSubmissionInvalid
		}
		c.Name = prepared.Name
		c.Concurrency = prepared.Concurrency
		c.Priority = prepared.Priority
		if prepared.RateMultiplier != nil {
			c.RateMultiplier = *prepared.RateMultiplier
		}
		c.ProxyID = prepared.ProxyID
	}
	if options.Name != nil {
		c.Name = *options.Name
	}
	if options.Concurrency != nil {
		c.Concurrency = *options.Concurrency
	}
	if options.Connection != nil && options.Connection.Mode != "preset" {
		c.ProxyID = nil
	}
	if err := service.ValidateAccountSubmissionConfig(&c); err != nil {
		return err
	}
	if !referencesLocked {
		if err := validateSubmissionReferences(ctx, tx, c); err != nil {
			return err
		}
	}
	if options.Connection != nil {
		if _, err = tx.ExecContext(ctx, `INSERT INTO account_submission_connections_geili(invite_id) VALUES($1) ON CONFLICT DO NOTHING`, invite.ID); err != nil {
			return service.ErrSubmissionUnavailable
		}
		c.ProxyID, err = writeSubmissionProxy(ctx, tx, invite, *options.Connection)
		if err != nil {
			return err
		}
	}
	baseURL := "https://api.anthropic.com"
	if c.Platform == service.PlatformOpenAI {
		baseURL = "https://api.openai.com"
	}
	credentialValues := map[string]any{"api_key": key, "base_url": baseURL}
	accountType := "apikey"
	if prepared != nil {
		credentialValues = prepared.Credentials
		accountType = prepared.Type
	}
	credentials, err := json.Marshal(credentialValues)
	if err != nil {
		return service.ErrSubmissionInvalid
	}
	extra := `{}`
	if c.Platform == service.PlatformOpenAI {
		extra = `{"openai_long_context_billing_enabled":false}`
	}
	var notes *string
	var loadFactor *int
	var expiresAt *time.Time
	autoPause := true
	if prepared != nil {
		encoded, e := json.Marshal(prepared.Extra)
		if e != nil {
			return service.ErrSubmissionInvalid
		}
		extra = string(encoded)
		notes = prepared.Notes
		loadFactor = prepared.LoadFactor
		expiresAt = prepared.ExpiresAt
		autoPause = prepared.AutoPauseOnExpired
	}
	var accountID int64
	// Never return/log database errors from this write: a PostgreSQL detail can contain secrets.
	if prepared == nil {
		err = tx.QueryRowContext(ctx, `INSERT INTO accounts (name,platform,type,credentials,extra,proxy_id,concurrency,priority,rate_multiplier,status,schedulable,auto_pause_on_expired)
        VALUES ($1,$2,'apikey',$3,$4,$5,$6,$7,$8,'inactive',false,true) RETURNING id`, c.Name, c.Platform, string(credentials), extra, c.ProxyID, c.Concurrency, c.Priority, c.RateMultiplier).Scan(&accountID)
	} else {
		err = tx.QueryRowContext(ctx, `INSERT INTO accounts (name,platform,type,credentials,extra,proxy_id,concurrency,priority,rate_multiplier,status,schedulable,auto_pause_on_expired,notes,load_factor,expires_at)
        VALUES ($1,$2,$9,$3,$4,$5,$6,$7,$8,'inactive',false,$13,$10,$11,$12) RETURNING id`, c.Name, c.Platform, string(credentials), extra, c.ProxyID, c.Concurrency, c.Priority, c.RateMultiplier, accountType, notes, loadFactor, expiresAt, autoPause).Scan(&accountID)
	}
	if err != nil {
		return service.ErrSubmissionUnavailable
	}
	if referencesLocked && len(c.GroupIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority) SELECT $1,id,ordinal FROM unnest($2::bigint[]) WITH ORDINALITY AS g(id,ordinal)`, accountID, pq.Array(c.GroupIDs)); err != nil {
			return service.ErrSubmissionUnavailable
		}
	} else {
		for index, id := range c.GroupIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO account_groups (account_id,group_id,priority) VALUES ($1,$2,$3)`, accountID, id, index+1); err != nil {
				return service.ErrSubmissionUnavailable
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_submission_invites_geili SET account_id=$1,submitted_at=clock_timestamp() WHERE id=$2`, accountID, invite.ID); err != nil {
		return service.ErrSubmissionUnavailable
	}
	if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, buildSchedulerGroupPayload(c.GroupIDs)); err != nil {
		return service.ErrSubmissionUnavailable
	}
	return nil
}

func (r *accountSubmissionRepository) ProtectedAccountIDs(ctx context.Context, ids []int64) (map[int64]bool, error) {
	return protectedSubmissionAccountIDs(ctx, r.db, ids)
}

// geili hook: shared by invitation handling and the existing account repository.
func (r *accountRepository) ProtectedAccountIDs(ctx context.Context, ids []int64) (map[int64]bool, error) {
	return protectedSubmissionAccountIDs(ctx, r.sql, ids)
}

func protectedSubmissionAccountIDs(ctx context.Context, db sqlExecutor, ids []int64) (map[int64]bool, error) {
	protected := make(map[int64]bool)
	if len(ids) == 0 {
		return protected, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT account_id FROM account_submission_invites_geili WHERE account_id=ANY($1)`, pq.Array(ids))
	if err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) != nil {
			return nil, service.ErrSubmissionUnavailable
		}
		protected[id] = true
	}
	if rows.Err() != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	return protected, nil
}

func (r *accountSubmissionRepository) HasProtectedAccounts(ctx context.Context) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_submission_invites_geili WHERE submitted_at IS NOT NULL) OR EXISTS(SELECT 1 FROM proxies WHERE name LIKE '[external] %')`).Scan(&exists)
	if err != nil {
		return false, service.ErrSubmissionUnavailable
	}
	return exists, nil
}
