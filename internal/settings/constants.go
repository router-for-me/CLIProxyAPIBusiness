package settings

// DB config keys and defaults for settings.
const (
	// SiteNameKey is the DB config key for the UI site name.
	SiteNameKey = "SITE_NAME"
	// DefaultSiteName is the fallback UI site name.
	DefaultSiteName = "CLIProxyAPI"
	// QuotaPollIntervalSecondsKey controls the quota poll interval in seconds.
	QuotaPollIntervalSecondsKey = "QUOTA_POLL_INTERVAL_SECONDS"
	// QuotaPollMaxConcurrencyKey controls the max concurrent quota requests.
	QuotaPollMaxConcurrencyKey = "QUOTA_POLL_MAX_CONCURRENCY"
	// AutoAssignProxyKey toggles auto assignment of proxies on create.
	AutoAssignProxyKey = "AUTO_ASSIGN_PROXY"
	// RateLimitKey controls the default rate limit per second.
	RateLimitKey = "RATE_LIMIT"
	// RateLimitRedisEnabledKey toggles Redis-backed rate limiting.
	RateLimitRedisEnabledKey = "RATE_LIMIT_REDIS_ENABLED"
	// RateLimitRedisAddrKey defines the Redis address for rate limiting.
	RateLimitRedisAddrKey = "RATE_LIMIT_REDIS_ADDR"
	// RateLimitRedisPasswordKey defines the Redis password for rate limiting.
	RateLimitRedisPasswordKey = "RATE_LIMIT_REDIS_PASSWORD"
	// RateLimitRedisDBKey defines the Redis DB index for rate limiting.
	RateLimitRedisDBKey = "RATE_LIMIT_REDIS_DB"
	// RateLimitRedisPrefixKey defines the Redis key prefix for rate limiting.
	RateLimitRedisPrefixKey = "RATE_LIMIT_REDIS_PREFIX"
	// DefaultQuotaPollIntervalSeconds is the fallback poll interval (seconds).
	DefaultQuotaPollIntervalSeconds = 180
	// DefaultQuotaPollMaxConcurrency is the fallback max concurrency.
	DefaultQuotaPollMaxConcurrency = 5
	// DefaultAutoAssignProxy sets auto-assign proxy default.
	DefaultAutoAssignProxy = false
	// DefaultRateLimit is the fallback rate limit (0 means unlimited).
	DefaultRateLimit = 0
	// DefaultRateLimitRedisPrefix is the fallback Redis key prefix.
	DefaultRateLimitRedisPrefix = "cpab:rl"
)
