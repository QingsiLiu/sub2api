package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const accountBalanceLatchPrefixGeili = "geili:account:balance_latch:"

var clearAccountBalanceLatchScriptGeili = redis.NewScript(`
 local token=redis.call('GET',KEYS[1])
 if not token then return 1 end
 if token~=ARGV[1] then return 0 end
 redis.call('DEL',KEYS[1])
 return 1
`)

var _ service.AccountBalanceLatchCacheGeili = (*tempUnschedCache)(nil)

func (c *tempUnschedCache) balanceLatchRedisGeili() (*redis.Client, error) {
	if c == nil {
		return nil, errors.New("account balance latch unavailable")
	}
	if c.transportRDBGeili != nil {
		return c.transportRDBGeili, nil
	}
	if c.rdb == nil {
		return nil, errors.New("account balance latch unavailable")
	}
	return c.rdb, nil
}

func (c *tempUnschedCache) SetAccountBalanceLatchGeili(ctx context.Context, id int64) (string, error) {
	if id <= 0 {
		return "", service.ErrAccountNotFound
	}
	rdb, err := c.balanceLatchRedisGeili()
	if err != nil {
		return "", err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(nonce[:])
	if err := rdb.Set(ctx, accountBalanceLatchPrefixGeili+strconv.FormatInt(id, 10), token, 0).Err(); err != nil {
		return "", err
	}
	return token, nil
}

func (c *tempUnschedCache) GetAccountBalanceLatchGeili(ctx context.Context, id int64) (string, error) {
	if id <= 0 {
		return "", service.ErrAccountNotFound
	}
	rdb, err := c.balanceLatchRedisGeili()
	if err != nil {
		return "", err
	}
	token, err := rdb.Get(ctx, accountBalanceLatchPrefixGeili+strconv.FormatInt(id, 10)).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(token) != 32 {
		return "", errors.New("invalid account balance latch")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", errors.New("invalid account balance latch")
	}
	return token, nil
}

func (c *tempUnschedCache) ClearAccountBalanceLatchGeili(ctx context.Context, id int64, token string) (bool, error) {
	if id <= 0 {
		return false, service.ErrAccountNotFound
	}
	rdb, err := c.balanceLatchRedisGeili()
	if err != nil {
		return false, err
	}
	result, err := clearAccountBalanceLatchScriptGeili.Run(ctx, rdb, []string{accountBalanceLatchPrefixGeili + strconv.FormatInt(id, 10)}, token).Int64()
	return result == 1, err
}
