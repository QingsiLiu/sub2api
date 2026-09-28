package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"strconv"
)

// One bounded MGET per chunk, not a Redis round trip per displayed account.
func (c *schedulerCache) GetAccountsByIDsGeili(ctx context.Context, ids []int64) (map[int64]*service.Account, error) {
	result := map[int64]*service.Account{}
	ids = uniquePositiveInt64s(ids)
	for start := 0; start < len(ids); start += 200 {
		end := start + 200
		if end > len(ids) {
			end = len(ids)
		}
		keys := make([]string, 0, (end-start)*2)
		for _, id := range ids[start:end] {
			value := strconv.FormatInt(id, 10)
			keys = append(keys, schedulerAccountKey(value), schedulerLastUsedKey(value))
		}
		values, err := c.rdb.MGet(ctx, keys...).Result()
		if err != nil {
			return nil, err
		}
		for i, id := range ids[start:end] {
			if values[i*2] == nil {
				continue
			}
			a, err := decodeCachedAccount(values[i*2])
			if err != nil {
				return nil, err
			}
			if err = applySchedulerLastUsed(a, values[i*2+1]); err != nil {
				return nil, err
			}
			result[id] = a
		}
	}
	return result, nil
}
