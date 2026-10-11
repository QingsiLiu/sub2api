package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"strings"
)

func writeSubmissionProxy(ctx context.Context, tx *sql.Tx, i *service.AccountSubmissionInvite, c service.SubmissionConnectionInput) (*int64, error) {
	var id *int64
	switch c.Mode {
	case "preset":
		id = i.Config.ProxyID
		if id != nil {
			var valid bool
			if tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM proxies WHERE id=$1 AND deleted_at IS NULL AND status='active' AND (expires_at IS NULL OR expires_at>clock_timestamp()))`, *id).Scan(&valid) != nil || !valid {
				return nil, service.ErrSubmissionInvalid
			}
		}
	case "direct":
	case "custom":
		p := c.Proxy
		if p == nil {
			return nil, service.ErrSubmissionInvalid
		}
		password := p.Password
		if password == "" && !p.ClearPassword {
			err := tx.QueryRowContext(ctx, `SELECT COALESCE(p.password,'') FROM account_submission_connections_geili c JOIN proxies p ON p.id=c.proxy_id WHERE c.invite_id=$1`, i.ID).Scan(&password)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, service.ErrSubmissionUnavailable
			}
		}
		var newID int64
		if tx.QueryRowContext(ctx, `INSERT INTO proxies(name,protocol,host,port,username,password,status,fallback_mode) VALUES($1,$2,$3,$4,$5,$6,'active','none') RETURNING id`, proxyutil.ExternalProxyNamePrefix+p.Name, p.Protocol, p.Host, p.Port, p.Username, password).Scan(&newID) != nil {
			return nil, service.ErrSubmissionUnavailable
		}
		id = &newID
	default:
		return nil, service.ErrSubmissionInvalid
	}
	var owned *int64
	if c.Mode == "custom" {
		owned = id
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_submission_connections_geili SET proxy_id=$1,updated_at=clock_timestamp() WHERE invite_id=$2`, owned, i.ID); err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	return id, nil
}

func readSubmissionConnection(ctx context.Context, tx *sql.Tx, i *service.AccountSubmissionInvite) (*service.SubmissionConnection, error) {
	result := &service.SubmissionConnection{Mode: "direct", HasPreset: i.Config.ProxyID != nil}
	var owned, current *int64
	err := tx.QueryRowContext(ctx, `SELECT a.proxy_id,c.proxy_id,COALESCE(c.version,0) FROM accounts a LEFT JOIN account_submission_connections_geili c ON c.invite_id=$2 WHERE a.id=$1 AND a.deleted_at IS NULL`, *i.AccountID, i.ID).Scan(&current, &owned, &result.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrSubmissionInactive
	}
	if err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	if current == nil {
		return result, nil
	}
	result.Mode = "preset"
	if owned == nil || *owned != *current {
		return result, nil
	}
	p := &service.SubmissionProxyDraft{}
	err = tx.QueryRowContext(ctx, `SELECT name,protocol,host,port,COALESCE(username,''),COALESCE(password,'')<>'' FROM proxies WHERE id=$1 AND deleted_at IS NULL`, *owned).Scan(&p.Name, &p.Protocol, &p.Host, &p.Port, &p.Username, &result.HasPassword)
	if err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	p.Name = strings.TrimPrefix(p.Name, proxyutil.ExternalProxyNamePrefix)
	result.Mode = "custom"
	result.Proxy = p
	return result, nil
}
func connectionInvite(ctx context.Context, tx *sql.Tx, hash string, lock bool) (*service.AccountSubmissionInvite, error) {
	query := `SELECT ` + submissionColumns + ` FROM account_submission_invites_geili WHERE token_hash=$1 AND submitted_at IS NOT NULL AND view_revoked_at IS NULL`
	if lock {
		query += ` FOR UPDATE`
	}
	i, err := scanSubmission(tx.QueryRowContext(ctx, query, hash))
	if errors.Is(err, service.ErrSubmissionInvalid) {
		return nil, service.ErrSubmissionInactive
	}
	return i, err
}
func (r *accountSubmissionRepository) ReadSubmissionConnection(ctx context.Context, hash string) (*service.SubmissionConnection, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout='4s'`); err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	i, err := connectionInvite(ctx, tx, hash, false)
	if err != nil {
		return nil, err
	}
	result, err := readSubmissionConnection(ctx, tx, i)
	if err != nil {
		return nil, err
	}
	if tx.Commit() != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	return result, nil
}
func (r *accountSubmissionRepository) UpdateSubmissionConnection(ctx context.Context, hash string, version int64, requestID string, c service.SubmissionConnectionInput) (*service.SubmissionConnection, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout='4s'`); err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	i, err := connectionInvite(ctx, tx, hash, true)
	if err != nil {
		return nil, err
	}
	var accountID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, *i.AccountID).Scan(&accountID); errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrSubmissionInactive
	} else if err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO account_submission_connections_geili(invite_id) VALUES($1) ON CONFLICT DO NOTHING`, i.ID); err != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	var previousID string
	var currentVersion int64
	var oldProxy *int64
	if tx.QueryRowContext(ctx, `SELECT version,COALESCE(request_id::text,''),proxy_id FROM account_submission_connections_geili WHERE invite_id=$1`, i.ID).Scan(&currentVersion, &previousID, &oldProxy) != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	if previousID != strings.ToLower(requestID) {
		if currentVersion != version {
			return nil, service.ErrSubmissionConnectionConflict
		}
		proxyID, err := writeSubmissionProxy(ctx, tx, i, c)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE accounts SET proxy_id=$1,updated_at=clock_timestamp() WHERE id=$2`, proxyID, accountID); err != nil {
			return nil, service.ErrSubmissionUnavailable
		}
		if _, err = tx.ExecContext(ctx, `UPDATE account_submission_connections_geili SET version=version+1,request_id=$1 WHERE invite_id=$2`, requestID, i.ID); err != nil {
			return nil, service.ErrSubmissionUnavailable
		}
		if oldProxy != nil {
			if _, err = tx.ExecContext(ctx, `DELETE FROM proxies WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM accounts WHERE proxy_id=$1) AND NOT EXISTS(SELECT 1 FROM proxies WHERE backup_proxy_id=$1)`, *oldProxy); err != nil {
				return nil, service.ErrSubmissionUnavailable
			}
		}
		groups, groupErr := submissionConnectionGroups(ctx, tx, accountID)
		if groupErr != nil {
			return nil, service.ErrSubmissionUnavailable
		}
		if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, buildSchedulerGroupPayload(groups)); err != nil {
			return nil, service.ErrSubmissionUnavailable
		}
	}
	result, err := readSubmissionConnection(ctx, tx, i)
	if err != nil {
		return nil, err
	}
	if tx.Commit() != nil {
		return nil, service.ErrSubmissionUnavailable
	}
	return result, nil
}
func submissionConnectionGroups(ctx context.Context, tx *sql.Tx, id int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT group_id FROM account_groups WHERE account_id=$1 ORDER BY group_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var groupID int64
		if err = rows.Scan(&groupID); err != nil {
			return nil, err
		}
		ids = append(ids, groupID)
	}
	return ids, rows.Err()
}
