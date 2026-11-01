package service

import "context"

// Cache invalidation must not reconstruct the financial view. That projection
// performs several sequential reads and can exhaust the invalidation deadline
// before the first Redis deletion. These are the exact legacy aliases read by
// GetByID's GroupEntitlements edge and current parent fallback. The caller also
// includes the parent group known when the mutation occurred.
func (s *SubscriptionService) subscriptionInvalidationAliasesGeili(ctx context.Context, subscriptionID int64) ([]int64, error) {
	rows, err := s.entClient.QueryContext(ctx, `SELECT group_id FROM user_subscription_groups WHERE user_subscription_id=$1
 UNION ALL
 SELECT group_id FROM user_subscriptions WHERE id=$1 AND group_id>0
 AND NOT EXISTS(SELECT 1 FROM user_subscription_groups WHERE user_subscription_id=$1)`, subscriptionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var aliases []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		aliases = append(aliases, id)
	}
	return aliases, rows.Err()
}
