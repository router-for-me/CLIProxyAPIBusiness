package ratelimit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

const redisBreakerDuration = 30 * time.Second

// SettingsProvider supplies the latest settings snapshot.
type SettingsProvider func() SettingsConfig

// RedisClientFactory constructs a Redis client for the given options.
type RedisClientFactory func(options *redis.Options) *redis.Client

type redisConfig struct {
	addr     string
	password string
	prefix   string
	db       int
}

// Manager selects a limiter backend and enforces rate limits.
type Manager struct {
	provider       SettingsProvider
	nowFn          func() time.Time
	memoryLimiter  Limiter
	newRedisClient RedisClientFactory
	mu             sync.Mutex
	redisLimiter   *RedisLimiter
	redisCfg       redisConfig
	breakerUntil   time.Time
}

// NewManager constructs a Manager with default dependencies when nil.
func NewManager(provider SettingsProvider, nowFn func() time.Time, newRedisClient RedisClientFactory) *Manager {
	if provider == nil {
		provider = LoadSettingsConfig
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	if newRedisClient == nil {
		newRedisClient = redis.NewClient
	}
	return &Manager{
		provider:       provider,
		nowFn:          nowFn,
		memoryLimiter:  NewMemoryLimiter(),
		newRedisClient: newRedisClient,
	}
}

// Allow checks whether the request should be allowed using the best available backend.
func (m *Manager) Allow(ctx context.Context, key string, limit int) (Result, error) {
	if limit <= 0 || key == "" {
		return Result{Allowed: true}, nil
	}
	if m == nil {
		return Result{Allowed: true}, nil
	}
	now := m.nowFn()
	cfg := m.provider()

	if cfg.RedisEnabled {
		if result, ok := m.allowRedis(ctx, key, limit, now, cfg); ok {
			return result, nil
		}
	}
	return m.memoryLimiter.Allow(ctx, key, limit, now)
}

func (m *Manager) allowRedis(ctx context.Context, key string, limit int, now time.Time, cfg SettingsConfig) (Result, bool) {
	if m == nil {
		return Result{}, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if m.isBreakerActive(now) {
		return Result{}, false
	}
	limiter, errEnsure := m.ensureRedis(ctx, cfg, now)
	if errEnsure != nil {
		m.tripBreaker(errEnsure, now)
		return Result{}, false
	}
	if limiter == nil {
		return Result{}, false
	}
	result, errAllow := limiter.Allow(ctx, key, limit, now)
	if errAllow != nil {
		m.tripBreaker(errAllow, now)
		return Result{}, false
	}
	return result, true
}

func (m *Manager) isBreakerActive(now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.breakerUntil.IsZero() {
		return false
	}
	if now.Before(m.breakerUntil) {
		return true
	}
	m.breakerUntil = time.Time{}
	return false
}

func (m *Manager) tripBreaker(err error, now time.Time) {
	if err == nil || m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.breakerUntil.IsZero() && now.Before(m.breakerUntil) {
		return
	}
	m.breakerUntil = now.Add(redisBreakerDuration)
	log.WithError(err).Warn("rate limit: redis unavailable, falling back to memory")
}

func (m *Manager) ensureRedis(ctx context.Context, cfg SettingsConfig, now time.Time) (*RedisLimiter, error) {
	addr := strings.TrimSpace(cfg.RedisAddr)
	if addr == "" {
		return nil, errors.New("rate limit redis: missing address")
	}

	nextCfg := redisConfig{
		addr:     addr,
		password: strings.TrimSpace(cfg.RedisPassword),
		prefix:   strings.TrimSpace(cfg.RedisPrefix),
		db:       cfg.RedisDB,
	}
	if nextCfg.db < 0 {
		nextCfg.db = 0
	}
	if nextCfg.prefix == "" {
		nextCfg.prefix = cfg.RedisPrefix
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.redisLimiter != nil && m.redisCfg == nextCfg {
		return m.redisLimiter, nil
	}
	if m.redisLimiter != nil {
		_ = m.redisLimiter.client.Close()
		m.redisLimiter = nil
	}

	client := m.newRedisClient(&redis.Options{
		Addr:     nextCfg.addr,
		Password: nextCfg.password,
		DB:       nextCfg.db,
	})
	ctxPing, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if errPing := client.Ping(ctxPing).Err(); errPing != nil {
		_ = client.Close()
		return nil, errPing
	}
	m.redisLimiter = NewRedisLimiter(client, nextCfg.prefix)
	m.redisCfg = nextCfg
	return m.redisLimiter, nil
}
