package ratelimit

import (
	"context"
	"time"
)

// Result describes the outcome of a rate limit check.
type Result struct {
	Allowed   bool
	Remaining int
	Reset     time.Time
}

// Limiter provides rate limit checks.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, now time.Time) (Result, error)
}

// Scope indicates which dimension the rate limit applies to.
type Scope int

const (
	ScopeNone Scope = iota
	ScopeUser
	ScopeModelMapping
)

// Decision describes the resolved rate limit and scope.
type Decision struct {
	Limit     int
	Scope     Scope
	MappingID uint64
}
