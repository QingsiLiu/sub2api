package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func readGroupSchedulingGeili(ctx context.Context, exec sqlExecutor, ids []int64) ([]service.GroupSchedulingStateGeili, error) {
	if len(ids) == 0 {
		return []service.GroupSchedulingStateGeili{}, nil
	}
	rows, err := exec.QueryContext(ctx, `SELECT g.id,g.name,g.platform,g.group_scheduling_enabled,g.group_scheduling_version,
 a.id,a.priority,ag.priority,ag.priority_mode
 FROM groups g LEFT JOIN account_groups ag ON ag.group_id=g.id
 LEFT JOIN accounts a ON a.id=ag.account_id AND a.deleted_at IS NULL
 WHERE g.id=ANY($1) AND g.deleted_at IS NULL ORDER BY g.id,a.id`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []service.GroupSchedulingStateGeili{}
	index := map[int64]int{}
	for rows.Next() {
		var g service.GroupSchedulingStateGeili
		var aid sql.NullInt64
		var accountPriority, priority sql.NullInt64
		var mode sql.NullString
		if err = rows.Scan(&g.GroupID, &g.Name, &g.Platform, &g.Enabled, &g.Version, &aid, &accountPriority, &priority, &mode); err != nil {
			return nil, err
		}
		i, ok := index[g.GroupID]
		if !ok {
			i = len(result)
			index[g.GroupID] = i
			g.Supported = service.GeiliGroupPriorityPilot(g.GroupID) && g.Platform == service.PlatformOpenAI
			g.Rows = []service.GroupSchedulingRowGeili{}
			result = append(result, g)
		}
		if aid.Valid {
			result[i].Rows = append(result[i].Rows, service.GroupSchedulingRowGeili{AccountID: aid.Int64, AccountPriority: int(accountPriority.Int64), GroupPriority: int(priority.Int64), Mode: mode.String})
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i := range result {
		result[i].MembersVersion = service.GroupSchedulingMembersVersionGeili(result[i].Rows)
	}
	return result, nil
}

func (r *accountRepository) GetGroupSchedulingGeili(ctx context.Context, ids []int64) ([]service.GroupSchedulingStateGeili, error) {
	return readGroupSchedulingGeili(ctx, r.sql, ids)
}

func (r *accountRepository) UpdateGroupSchedulingGeili(ctx context.Context, id int64, input service.GroupSchedulingUpdateGeili, actor string) error {
	if err := input.Validate(); err != nil {
		return err
	}
	db, ok := r.sql.(*sql.DB)
	if !ok {
		return infraerrors.New(http.StatusServiceUnavailable, "GROUP_PRIORITY_UNAVAILABLE", "transactional database unavailable")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var enabled bool
	var version int64
	var platform string
	if err = tx.QueryRowContext(ctx, `SELECT group_scheduling_enabled,group_scheduling_version,platform FROM groups WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&enabled, &version, &platform); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrGroupNotFound
		}
		return err
	}
	if version != *input.ExpectedVersion {
		return service.ErrGroupSchedulingConflictGeili
	}
	if !service.GeiliGroupPriorityPilot(id) || platform != service.PlatformOpenAI {
		return infraerrors.BadRequest("GROUP_PRIORITY_NOT_SUPPORTED", "only pilot OpenAI groups 4/27/126 support group priorities")
	}
	// Locks also serialize ordinary binding changes and the initial global-priority snapshot.
	locked, err := tx.QueryContext(ctx, `SELECT ag.account_id FROM account_groups ag JOIN accounts a ON a.id=ag.account_id WHERE ag.group_id=$1 ORDER BY ag.account_id FOR UPDATE OF ag,a`, id)
	if err != nil {
		return err
	}
	ids := []int64{}
	for locked.Next() {
		var aid int64
		if err = locked.Scan(&aid); err != nil {
			locked.Close()
			return err
		}
		ids = append(ids, aid)
	}
	err = locked.Err()
	locked.Close()
	if err != nil {
		return err
	}
	before, err := readGroupSchedulingGeili(ctx, tx, []int64{id})
	if err != nil {
		return err
	}
	if len(before) != 1 || before[0].MembersVersion != input.ExpectedMembersVersion {
		return service.ErrGroupSchedulingConflictGeili
	}
	oldModes := map[int64]string{}
	oldPriorities := map[int64]int{}
	for _, row := range before[0].Rows {
		oldModes[row.AccountID] = row.Mode
		oldPriorities[row.AccountID] = row.GroupPriority
	}
	if input.Enabled != nil && len(input.Rows) > 0 {
		return infraerrors.BadRequest("GROUP_PRIORITY_INVALID", "activate separately from priority edits")
	}
	if !enabled && input.Enabled == nil && len(input.Rows) > 0 {
		return infraerrors.BadRequest("GROUP_PRIORITY_DISABLED", "enable this group before editing priorities")
	}
	changed := false
	if input.Enabled != nil && *input.Enabled != enabled {
		if *input.Enabled && version == 0 {
			// Historical group priorities and S2A local pins never become active accidentally.
			_, err = tx.ExecContext(ctx, `UPDATE account_groups ag SET priority=a.priority,priority_mode=CASE WHEN a.platform='openai' AND a.type='apikey' THEN 'auto' ELSE 'inherit' END FROM accounts a WHERE ag.account_id=a.id AND ag.group_id=$1`, id)
			if err != nil {
				return err
			}
		}
		enabled = *input.Enabled
		changed = true
	}
	for _, row := range input.Rows {
		mode, exists := oldModes[row.AccountID]
		if !exists {
			return service.ErrGroupSchedulingConflictGeili
		}
		if input.Source == "automatic" && mode != service.GroupPriorityAuto {
			return service.ErrGroupSchedulingConflictGeili
		}
		priority := oldPriorities[row.AccountID]
		if row.Priority != nil {
			priority = *row.Priority
		}
		if mode == row.Mode && priority == oldPriorities[row.AccountID] {
			continue
		}
		_, err = tx.ExecContext(ctx, `UPDATE account_groups SET priority=$3,priority_mode=$4 WHERE group_id=$1 AND account_id=$2`, id, row.AccountID, priority, row.Mode)
		if err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `UPDATE groups SET group_scheduling_enabled=$2,group_scheduling_version=group_scheduling_version+1,updated_at=NOW() WHERE id=$1`, id, enabled)
	if err != nil {
		return err
	}
	after, err := readGroupSchedulingGeili(ctx, tx, []int64{id})
	if err != nil {
		return err
	}
	beforeJSON, _ := json.Marshal(before[0])
	afterJSON, _ := json.Marshal(after[0])
	if len(actor) > 200 {
		actor = actor[:200]
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO geili_group_scheduling_audits(group_id,version,actor,source,before_state,after_state) VALUES($1,$2,$3,$4,$5,$6)`, id, version+1, actor, input.Source, beforeJSON, afterJSON)
	if err != nil {
		return err
	}
	if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventGroupChanged, nil, &id, nil); err != nil {
		return err
	}
	for _, aid := range ids {
		a := aid
		if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountGroupsChanged, &a, &id, buildSchedulerGroupPayload([]int64{id})); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// Best-effort acceleration; outbox remains the durable repair path.
	propagation, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	r.syncSchedulerAccountSnapshots(propagation, ids)
	return nil
}

func (r *accountRepository) GroupSchedulingRuntimeGeili(ctx context.Context, ids []int64) (map[int64]*service.Account, error) {
	if r.schedulerCache == nil {
		return nil, nil
	}
	if cache, ok := r.schedulerCache.(interface {
		GetAccountsByIDsGeili(context.Context, []int64) (map[int64]*service.Account, error)
	}); ok {
		return cache.GetAccountsByIDsGeili(ctx, ids)
	}
	result := map[int64]*service.Account{}
	for _, id := range ids {
		account, err := r.schedulerCache.GetAccount(ctx, id)
		if err != nil {
			return nil, err
		}
		if account != nil {
			result[id] = account
		}
	}
	return result, nil
}
