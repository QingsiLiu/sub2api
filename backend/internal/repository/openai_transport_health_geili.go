package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// One hash per account keeps streak and block atomic across gateway instances.
var openAITransportFailureScriptGeili = redis.NewScript(`
local key = KEYS[1]
local clock = redis.call('TIME')
local now = tonumber(clock[1])*1000 + math.floor(tonumber(clock[2])/1000)
local until_ms = tonumber(redis.call('HGET', key, 'until') or '0')
if until_ms > now then return {0, 0, until_ms} end
local count = tonumber(redis.call('HGET', key, 'count') or '0') + 1
local tripped = 0
if count >= tonumber(ARGV[1]) then
  until_ms = now + tonumber(ARGV[2])
  tripped = 1
  redis.call('HSET', key, 'count', 0, 'until', until_ms)
else
  redis.call('HSET', key, 'count', count, 'until', 0)
end
redis.call('PEXPIRE', key, ARGV[3])
return {count, tripped, until_ms}
`)

// A stale in-flight success must never remove an active cooldown.
var openAITransportSuccessScriptGeili = redis.NewScript(`
local clock = redis.call('TIME')
local now = tonumber(clock[1])*1000 + math.floor(tonumber(clock[2])/1000)
local until_ms = tonumber(redis.call('HGET', KEYS[1], 'until') or '0')
if until_ms > now then redis.call('HSET', KEYS[1], 'count', 0)
else redis.call('DEL', KEYS[1]) end
return 1
`)

func openAITransportHealthKeyGeili(id int64) string {
	return fmt.Sprintf("openai_transport_health:{%d}", id)
}

func (c *tempUnschedCache) RecordOpenAITransportFailureGeili(ctx context.Context, id int64) (service.OpenAITransportHealthDecisionGeili, error) {
	values, err := openAITransportFailureScriptGeili.Run(ctx, c.transportRDBGeili, []string{openAITransportHealthKeyGeili(id)},
		service.OpenAITransportFailureThresholdGeili, service.OpenAITransportCooldownGeili.Milliseconds(),
		service.OpenAITransportFailureTTLGeili.Milliseconds()).Int64Slice()
	if err != nil {
		return service.OpenAITransportHealthDecisionGeili{}, err
	}
	if len(values) != 3 {
		return service.OpenAITransportHealthDecisionGeili{}, fmt.Errorf("transport health: unexpected result length %d", len(values))
	}
	decision := service.OpenAITransportHealthDecisionGeili{Count: values[0], Tripped: values[1] == 1}
	if values[2] > 0 {
		decision.Until = time.UnixMilli(values[2])
	}
	return decision, nil
}

func (c *tempUnschedCache) ResetOpenAITransportFailuresGeili(ctx context.Context, id int64) error {
	return openAITransportSuccessScriptGeili.Run(ctx, c.transportRDBGeili, []string{openAITransportHealthKeyGeili(id)}).Err()
}

func (c *tempUnschedCache) OpenAITransportBlockedUntilGeili(ctx context.Context, id int64) (time.Time, error) {
	value, err := c.transportRDBGeili.HGet(ctx, openAITransportHealthKeyGeili(id), "until").Int64()
	if err == redis.Nil {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	if value <= 0 {
		return time.Time{}, nil
	}
	return time.UnixMilli(value), nil
}

var _ service.OpenAITransportHealthCacheGeili = (*tempUnschedCache)(nil)

// WithTimeout owns cloned options but shares the parent pool/lifecycle. Only this
// view honors the health deadline and disables retries; never Close the view.
func openAITransportRedisGeili(parent *redis.Client) *redis.Client {
	client := parent.WithTimeout(200 * time.Millisecond)
	client.Options().ContextTimeoutEnabled = true
	client.Options().MaxRetries = 0 // options are already normalized on the parent
	return client
}

func (c *tempUnschedCache) OpenAITransportBlocksGeili(ctx context.Context, ids []int64) (map[int64]time.Time, error) {
	pipe := c.transportRDBGeili.Pipeline()
	commands := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		commands[i] = pipe.HGet(ctx, openAITransportHealthKeyGeili(id), "until")
	}
	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, err
	}
	blocks := make(map[int64]time.Time, len(ids))
	for i, command := range commands {
		value, err := command.Int64()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return nil, err
		}
		if value > 0 {
			blocks[ids[i]] = time.UnixMilli(value)
		}
	}
	return blocks, nil
}
