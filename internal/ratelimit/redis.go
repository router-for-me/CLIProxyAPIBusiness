package ratelimit

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisWindowTTLSeconds = 2

var redisIncrScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("EXPIRE", KEYS[1], ARGV[1])
end
return current
`)

// RedisLimiter implements a fixed-window rate limiter backed by Redis.
type RedisLimiter struct {
	client *redis.Client
	prefix string
}

// NewRedisLimiter constructs a RedisLimiter.
func NewRedisLimiter(client *redis.Client, prefix string) *RedisLimiter {
	return &RedisLimiter{
		client: client,
		prefix: strings.TrimSpace(prefix),
	}
}

// Allow checks whether the request should be allowed in the current second.
func (l *RedisLimiter) Allow(ctx context.Context, key string, limit int, now time.Time) (Result, error) {
	if limit <= 0 || key == "" || l == nil || l.client == nil {
		return Result{Allowed: true}, nil
	}
	sec := now.Unix()
	reset := time.Unix(sec+1, 0).UTC()
	redisKey := l.buildKey(key, sec)
	res, errEval := redisIncrScript.Run(ctx, l.client, []string{redisKey}, redisWindowTTLSeconds).Result()
	if errEval != nil {
		return Result{}, errEval
	}
	count, ok := res.(int64)
	if !ok {
		switch v := res.(type) {
		case int:
			count = int64(v)
		case uint64:
			count = int64(v)
		default:
			return Result{}, errors.New("rate limit redis: unexpected response type")
		}
	}
	if count > int64(limit) {
		return Result{Allowed: false, Remaining: 0, Reset: reset}, nil
	}
	remaining := limit - int(count)
	if remaining < 0 {
		remaining = 0
	}
	return Result{Allowed: true, Remaining: remaining, Reset: reset}, nil
}

func (l *RedisLimiter) buildKey(key string, sec int64) string {
	secStr := strconv.FormatInt(sec, 10)
	prefix := strings.TrimSpace(l.prefix)
	if prefix == "" {
		return key + ":" + secStr
	}
	return prefix + ":" + key + ":" + secStr
}
