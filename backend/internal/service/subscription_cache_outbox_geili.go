package service

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

func (s *SubscriptionService) startSubscriptionCacheOutbox() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cacheOutboxCancel = cancel
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.drainSubscriptionCacheOutbox(ctx); err != nil && ctx.Err() == nil {
					slog.Warn("subscription cache invalidation pending retry", "error", err)
				}
			}
		}
	}()
}
func (s *SubscriptionService) drainSubscriptionCacheOutbox(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := s.entClient.QueryContext(ctx, `SELECT subscription_id,user_id,group_id,version FROM subscription_cache_outbox ORDER BY subscription_id LIMIT 100`)
	if err != nil {
		return err
	}
	type entry struct {
		id, user, version int64
		group             sql.NullInt64
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.user, &e.group, &e.version); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := s.invalidateSubscriptionCaches(e.user, e.group.Int64, e.id); err != nil {
			return err
		}
		if _, err := s.entClient.ExecContext(ctx, `DELETE FROM subscription_cache_outbox WHERE subscription_id=$1 AND version=$2`, e.id, e.version); err != nil {
			return err
		}
	}
	return nil
}
