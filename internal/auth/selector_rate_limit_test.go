package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/ratelimit"
	"gorm.io/gorm"
)

func TestSelectorRateLimitBlocksAfterLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	selector := &Selector{
		db: &gorm.DB{},
		rateLimiter: ratelimit.NewManager(func() ratelimit.SettingsConfig {
			return ratelimit.SettingsConfig{}
		}, func() time.Time {
			return now
		}, nil),
		resolveRateLimit: func(_ context.Context, _ *gorm.DB, _ uint64, _ string, _ string, _ string) (ratelimit.Decision, error) {
			return ratelimit.Decision{Limit: 1, Scope: ratelimit.ScopeUser}, nil
		},
	}

	ctx := buildTestContext("/v1/chat/completions", "123")
	auths := []*coreauth.Auth{{ID: "auth-1", Status: coreauth.StatusActive}}

	if _, errPick := selector.Pick(ctx, "provider", "model", cliproxyexecutor.Options{}, auths); errPick != nil {
		t.Fatalf("expected first pick ok, got %v", errPick)
	}
	if _, errPick := selector.Pick(ctx, "provider", "model", cliproxyexecutor.Options{}, auths); errPick == nil {
		t.Fatalf("expected rate limit error, got nil")
	}
}

func TestSelectorRateLimitSkipsModelsPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	selector := &Selector{
		db: &gorm.DB{},
		rateLimiter: ratelimit.NewManager(func() ratelimit.SettingsConfig {
			return ratelimit.SettingsConfig{}
		}, func() time.Time {
			return now
		}, nil),
		resolveRateLimit: func(_ context.Context, _ *gorm.DB, _ uint64, _ string, _ string, _ string) (ratelimit.Decision, error) {
			return ratelimit.Decision{Limit: 1, Scope: ratelimit.ScopeUser}, nil
		},
	}

	ctx := buildTestContext("/v1/models", "123")
	auths := []*coreauth.Auth{{ID: "auth-1", Status: coreauth.StatusActive}}

	if _, errPick := selector.Pick(ctx, "provider", "model", cliproxyexecutor.Options{}, auths); errPick != nil {
		t.Fatalf("expected pick ok, got %v", errPick)
	}
	if _, errPick := selector.Pick(ctx, "provider", "model", cliproxyexecutor.Options{}, auths); errPick != nil {
		t.Fatalf("expected pick ok, got %v", errPick)
	}
}

func buildTestContext(path string, userID string) context.Context {
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, path, nil)
	if userID != "" {
		ginCtx.Set("accessMetadata", map[string]string{"user_id": userID})
	}
	return context.WithValue(context.Background(), "gin", ginCtx)
}
