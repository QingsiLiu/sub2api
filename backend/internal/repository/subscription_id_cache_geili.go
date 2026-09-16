package repository

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

func billingSubscriptionIDKey(id int64) string { return fmt.Sprintf("billing:sub:v2:id:%d", id) }

var seedSubscriptionIDScript = redis.NewScript(`
 if redis.call('EXISTS',KEYS[1]) == 1 then return 0 end
 redis.call('HSET',KEYS[1], 'status',ARGV[1], 'expires_at',ARGV[2], 'daily_usage',ARGV[3], 'weekly_usage',ARGV[4], 'monthly_usage',ARGV[5], 'version',ARGV[6])
 redis.call('EXPIRE',KEYS[1],ARGV[7])
 return 1
`)

func (c *billingCache) GetSubscriptionCacheByID(ctx context.Context, id int64) (*service.SubscriptionCacheData, error) {
	fields, err := c.rdb.HGetAll(ctx, billingSubscriptionIDKey(id)).Result()
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, redis.Nil
	}
	return c.parseSubscriptionCache(fields)
}
func (c *billingCache) SeedSubscriptionCacheByID(ctx context.Context, id int64, data *service.SubscriptionCacheData) error {
	if data == nil {
		return nil
	}
	return seedSubscriptionIDScript.Run(ctx, c.rdb, []string{billingSubscriptionIDKey(id)}, data.Status, data.ExpiresAt.Unix(), data.DailyUsage, data.WeeklyUsage, data.MonthlyUsage, data.Version, int(jitteredTTL().Seconds())).Err()
}
func (c *billingCache) UpdateSubscriptionUsageByID(ctx context.Context, id int64, cost float64) error {
	return updateSubUsageScript.Run(ctx, c.rdb, []string{billingSubscriptionIDKey(id)}, cost, int(jitteredTTL().Seconds())).Err()
}
func (c *billingCache) InvalidateSubscriptionCacheByID(ctx context.Context, id int64) error {
	return c.rdb.Del(ctx, billingSubscriptionIDKey(id)).Err()
}
