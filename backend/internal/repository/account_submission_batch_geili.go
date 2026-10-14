package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Root locking serializes single/form/batch submission. Every member retains the existing
// provenance/export/credential protection and independent proxy version, without new capabilities.
func (r *accountSubmissionRepository) SubmitInviteBatch(ctx context.Context, hash string, keys []string, options service.SubmissionOptions) error {
	keys, err := service.NormalizeSubmissionBatchKeys(keys)
	if err != nil {
		return err
	}
	if options.PreparedAccount != nil || (len(options.PerKeyConnections) > 0 && (len(options.PerKeyConnections) != len(keys) || options.Connection != nil)) {
		return service.ErrSubmissionInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.ErrSubmissionUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	invite, err := scanSubmission(tx.QueryRowContext(ctx, `SELECT `+submissionColumns+` FROM account_submission_invites_geili WHERE token_hash=$1 AND parent_invite_id IS NULL FOR UPDATE`, hash))
	if err != nil {
		return err
	}
	if invite.SubmittedAt != nil {
		return nil
	}
	var usable bool
	if tx.QueryRowContext(ctx, `SELECT revoked_at IS NULL AND expires_at>clock_timestamp() FROM account_submission_invites_geili WHERE id=$1`, invite.ID).Scan(&usable) != nil {
		return service.ErrSubmissionUnavailable
	}
	if !usable {
		return service.ErrSubmissionInactive
	}
	if len(options.PerKeyConnections) > 0 {
		// Lock shared groups once and the preset proxy if any member uses it.
		// The first member may be direct while a later one uses the preset.
		references := invite.Config
		references.ProxyID = nil
		for _, connection := range options.PerKeyConnections {
			if connection.Mode == "preset" {
				references.ProxyID = invite.Config.ProxyID
			}
		}
		if err = validateSubmissionReferences(ctx, tx, references); err != nil {
			return err
		}
	}
	baseName := invite.Config.Name
	if options.Name != nil {
		baseName = *options.Name
	}
	for index, key := range keys {
		member := invite
		if index > 0 {
			var entropy [32]byte
			if _, err = rand.Read(entropy[:]); err != nil {
				return service.ErrSubmissionUnavailable
			}
			member, err = scanSubmission(tx.QueryRowContext(ctx, `INSERT INTO account_submission_invites_geili(token_hash,created_by,config,expires_at,parent_invite_id) SELECT $1,created_by,config,expires_at,id FROM account_submission_invites_geili WHERE id=$2 RETURNING `+submissionColumns, fmt.Sprintf("%x", sha256.Sum256(entropy[:])), invite.ID))
			if err != nil {
				return err
			}
		}
		itemOptions := options
		itemOptions.PerKeyConnections = nil
		if len(options.PerKeyConnections) > 0 {
			itemOptions.Connection = &options.PerKeyConnections[index]
		}
		name := baseName
		if len(keys) > 1 {
			suffix := " #" + strconv.Itoa(index+1)
			runes := []rune(baseName)
			if len(runes) > 100-len(suffix) {
				runes = runes[:100-len(suffix)]
			}
			name = string(runes) + suffix
		}
		itemOptions.Name = &name
		if err = createSubmittedAccountInBatch(ctx, tx, member, key, itemOptions, index > 0 || len(options.PerKeyConnections) > 0); err != nil {
			return err
		}
	}
	if tx.Commit() != nil {
		return service.ErrSubmissionUnavailable
	}
	return nil
}
