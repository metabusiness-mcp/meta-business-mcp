package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type Limiter struct {
	rdb *redis.Client
}

func NewLimiter(rdb *redis.Client) *Limiter {
	return &Limiter{rdb: rdb}
}

// Lua script to implement atomic sliding window token bucket rate limiting.
const rateLimitLua = `
local key = KEYS[1]
local mps = tonumber(ARGV[1])
local now = tonumber(ARGV[2])

local last_update = tonumber(redis.call('HGET', key, 'last_update') or 0)
local tokens = tonumber(redis.call('HGET', key, 'tokens') or mps)

-- Calculate replenishment
local elapsed = math.max(0, now - last_update)
local replenished = tokens + ((elapsed / 1000.0) * mps)
tokens = math.min(mps, replenished)

if tokens >= 1.0 then
	tokens = tokens - 1.0
	redis.call('HSET', key, 'tokens', tokens, 'last_update', now)
	redis.call('EXPIRE', key, 86400) -- expire key after 1 day of inactivity
	return 1
else
	return 0
end
`

// RateLimitStatus holds the current state of a rate limit bucket.
type RateLimitStatus struct {
	Key          string  `json:"key"`
	Capacity     float64 `json:"capacity"`
	TokensUsed   float64 `json:"tokens_used"`
	TokensLeft   float64 `json:"tokens_left"`
	LastUpdateMs int64   `json:"last_update_ms"`
}

// GetStatus reads the current state of the rate limit bucket for a given key.
// Returns capacity (MPS), tokens remaining, and the last update timestamp.
func (l *Limiter) GetStatus(ctx context.Context, key string, mps float64) (*RateLimitStatus, error) {
	rateKey := fmt.Sprintf("rate:%s", key)

	tokensVal, err := l.rdb.HGet(ctx, rateKey, "tokens").Result()
	if err != nil {
		// Key does not exist — bucket is full
		return &RateLimitStatus{
			Key:        key,
			Capacity:   mps,
			TokensUsed: 0,
			TokensLeft: mps,
		}, nil
	}

	lastUpdateVal, _ := l.rdb.HGet(ctx, rateKey, "last_update").Result()

	tokens, _ := strconv.ParseFloat(tokensVal, 64)
	lastUpdate, _ := strconv.ParseInt(lastUpdateVal, 10, 64)

	// Calculate replenished tokens since last update
	nowMilli := time.Now().UnixMilli()
	elapsed := float64(nowMilli-lastUpdate) / 1000.0
	if elapsed < 0 {
		elapsed = 0
	}
	replenished := tokens + (elapsed * mps)
	if replenished > mps {
		replenished = mps
	}

	return &RateLimitStatus{
		Key:          key,
		Capacity:     mps,
		TokensUsed:   mps - replenished,
		TokensLeft:   replenished,
		LastUpdateMs: lastUpdate,
	}, nil
}

func (l *Limiter) Allow(ctx context.Context, key string, mps float64) (bool, error) {
	nowMilli := time.Now().UnixMilli()
	
	// Execute script
	res, err := l.rdb.Eval(ctx, rateLimitLua, []string{fmt.Sprintf("rate:%s", key)}, mps, nowMilli).Result()
	if err != nil {
		return false, fmt.Errorf("failed to execute Redis rate limit script: %w", err)
	}

	allowed, ok := res.(int64)
	if !ok {
		return false, fmt.Errorf("unexpected rate limit script return type: %T", res)
	}

	return allowed == 1, nil
}
