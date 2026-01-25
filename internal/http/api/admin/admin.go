package admin

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	sdkapi "github.com/router-for-me/CLIProxyAPI/v6/sdk/api"
	sdkhandlers "github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/config"
	handlers "github.com/router-for-me/CLIProxyAPIBusiness/internal/http/api/admin/handlers"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/http/api/admin/permissions"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/security"
	"gorm.io/gorm"
)

// RegisterAdminRoutes registers admin routes, middleware, and handlers.
func RegisterAdminRoutes(r *gin.Engine, db *gorm.DB, jwtCfg config.JWTConfig, configPath string, cfg *sdkconfig.Config, baseHandler *sdkhandlers.BaseAPIHandler) {
	if r == nil || db == nil {
		return
	}

	healthHandler := handlers.NewHealthHandler(db)
	r.GET("/healthz", healthHandler.Healthz)

	versionHandler := handlers.NewVersionHandler()
	r.GET("/v0/version", versionHandler.GetVersion)

	adminGroup := r.Group("/v0/admin")

	webAuthn, errWebAuthn := security.NewWebAuthn()
	if errWebAuthn != nil {
		webAuthn = nil
	}

	authHandler := handlers.NewAuthHandler(db, jwtCfg, webAuthn)
	adminGroup.POST("/login", authHandler.Login)
	adminGroup.POST("/login/prepare", authHandler.LoginPrepare)
	adminGroup.POST("/login/totp", authHandler.LoginTOTP)
	adminGroup.POST("/login/passkey/options", authHandler.LoginPasskeyOptions)
	adminGroup.POST("/login/passkey/verify", authHandler.LoginPasskeyVerify)

	selfAuthed := adminGroup.Group("")
	selfAuthed.Use(adminAuthMiddleware(db, jwtCfg))

	mfaHandler := handlers.NewMFAHandler(db, webAuthn)
	selfAuthed.GET("/mfa/status", mfaHandler.Status)
	selfAuthed.POST("/mfa/totp/prepare", mfaHandler.PrepareTOTP)
	selfAuthed.POST("/mfa/totp/confirm", mfaHandler.ConfirmTOTP)
	selfAuthed.POST("/mfa/totp/disable", mfaHandler.DisableTOTP)
	selfAuthed.POST("/mfa/passkey/options", mfaHandler.BeginPasskeyRegistration)
	selfAuthed.POST("/mfa/passkey/verify", mfaHandler.FinishPasskeyRegistration)
	selfAuthed.POST("/mfa/passkey/disable", mfaHandler.DisablePasskey)

	authed := adminGroup.Group("")
	authed.Use(adminAuthMiddleware(db, jwtCfg))
	authed.Use(adminPermissionMiddleware(db))

	apiKeyHandler := handlers.NewAPIKeyHandler(db)
	authed.POST("/api-keys", apiKeyHandler.Create)
	authed.GET("/api-keys", apiKeyHandler.List)
	authed.DELETE("/api-keys/:id", apiKeyHandler.Revoke)
	authed.POST("/users/:id/api-keys", apiKeyHandler.CreateForUser)
	authed.GET("/users/:id/api-keys", apiKeyHandler.ListByUser)

	providerKeyHandler := handlers.NewProviderAPIKeyHandler(db, configPath)
	authed.POST("/provider-api-keys", providerKeyHandler.Create)
	authed.GET("/provider-api-keys", providerKeyHandler.List)
	authed.PUT("/provider-api-keys/:id", providerKeyHandler.Update)
	authed.DELETE("/provider-api-keys/:id", providerKeyHandler.Delete)

	proxyHandler := handlers.NewProxyHandler(db)
	authed.POST("/proxies", proxyHandler.Create)
	authed.POST("/proxies/batch", proxyHandler.BatchCreate)
	authed.GET("/proxies", proxyHandler.List)
	authed.PUT("/proxies/:id", proxyHandler.Update)
	authed.DELETE("/proxies/:id", proxyHandler.Delete)

	usageHandler := handlers.NewUsageHandler(db)
	authed.GET("/usage", usageHandler.List)

	billingHandler := handlers.NewBillingHandler(db)
	authed.GET("/billing/summary", billingHandler.Summary)

	userHandler := handlers.NewUserHandler(db)
	authed.POST("/users", userHandler.Create)
	authed.GET("/users", userHandler.List)
	authed.GET("/users/:id", userHandler.Get)
	authed.PUT("/users/:id", userHandler.Update)
	authed.DELETE("/users/:id", userHandler.Delete)
	authed.POST("/users/:id/disable", userHandler.Disable)
	authed.POST("/users/:id/enable", userHandler.Enable)
	authed.PUT("/users/:id/password", userHandler.ChangePassword)

	authGroupHandler := handlers.NewAuthGroupHandler(db)
	authed.POST("/auth-groups", authGroupHandler.Create)
	authed.GET("/auth-groups", authGroupHandler.List)
	authed.GET("/auth-groups/:id", authGroupHandler.Get)
	authed.PUT("/auth-groups/:id", authGroupHandler.Update)
	authed.DELETE("/auth-groups/:id", authGroupHandler.Delete)
	authed.POST("/auth-groups/:id/default", authGroupHandler.SetDefault)

	authFileHandler := handlers.NewAuthFileHandler(db)
	authed.POST("/auth-files", authFileHandler.Create)
	authed.POST("/auth-files/import", authFileHandler.Import)
	authed.GET("/auth-files", authFileHandler.List)
	authed.GET("/auth-files/:id", authFileHandler.Get)
	authed.PUT("/auth-files/:id", authFileHandler.Update)
	authed.DELETE("/auth-files/:id", authFileHandler.Delete)
	authed.POST("/auth-files/:id/available", authFileHandler.SetAvailable)
	authed.POST("/auth-files/:id/unavailable", authFileHandler.SetUnavailable)
	authed.GET("/auth-files/types", authFileHandler.ListTypes)

	quotaHandler := handlers.NewQuotaHandler(db)
	authed.GET("/quotas", quotaHandler.List)

	userGroupHandler := handlers.NewUserGroupHandler(db)
	authed.POST("/user-groups", userGroupHandler.Create)
	authed.GET("/user-groups", userGroupHandler.List)
	authed.GET("/user-groups/:id", userGroupHandler.Get)
	authed.PUT("/user-groups/:id", userGroupHandler.Update)
	authed.DELETE("/user-groups/:id", userGroupHandler.Delete)
	authed.POST("/user-groups/:id/default", userGroupHandler.SetDefault)

	billingRuleHandler := handlers.NewBillingRuleHandler(db)
	authed.POST("/billing-rules", billingRuleHandler.Create)
	authed.GET("/billing-rules", billingRuleHandler.List)
	authed.GET("/billing-rules/:id", billingRuleHandler.Get)
	authed.PUT("/billing-rules/:id", billingRuleHandler.Update)
	authed.DELETE("/billing-rules/:id", billingRuleHandler.Delete)
	authed.POST("/billing-rules/:id/enabled", billingRuleHandler.SetEnabled)
	authed.POST("/billing-rules/batch-import", billingRuleHandler.BatchImport)

	prepaidCardHandler := handlers.NewPrepaidCardHandler(db)
	authed.POST("/prepaid-cards", prepaidCardHandler.Create)
	authed.POST("/prepaid-cards/batch", prepaidCardHandler.BatchCreate)
	authed.GET("/prepaid-cards", prepaidCardHandler.List)
	authed.GET("/prepaid-cards/:id", prepaidCardHandler.Get)
	authed.PUT("/prepaid-cards/:id", prepaidCardHandler.Update)
	authed.DELETE("/prepaid-cards/:id", prepaidCardHandler.Delete)

	adminHandler := handlers.NewAdminHandler(db)
	authed.POST("/admins", adminHandler.Create)
	authed.GET("/admins", adminHandler.List)
	authed.GET("/admins/:id", adminHandler.Get)
	authed.PUT("/admins/:id", adminHandler.Update)
	authed.DELETE("/admins/:id", adminHandler.Delete)
	authed.POST("/admins/:id/disable", adminHandler.Disable)
	authed.POST("/admins/:id/enable", adminHandler.Enable)
	authed.PUT("/admins/:id/password", adminHandler.ChangePassword)

	permissionHandler := handlers.NewPermissionHandler()
	authed.GET("/permissions", permissionHandler.List)

	billHandler := handlers.NewBillHandler(db)
	authed.POST("/bills", billHandler.Create)
	authed.GET("/bills", billHandler.List)
	authed.GET("/bills/:id", billHandler.Get)
	authed.PUT("/bills/:id", billHandler.Update)
	authed.DELETE("/bills/:id", billHandler.Delete)
	authed.POST("/bills/:id/enable", billHandler.Enable)
	authed.POST("/bills/:id/disable", billHandler.Disable)

	modelMappingHandler := handlers.NewModelMappingHandler(db)
	authed.POST("/model-mappings", modelMappingHandler.Create)
	authed.GET("/model-mappings", modelMappingHandler.List)
	authed.GET("/model-mappings/available-models", modelMappingHandler.AvailableModels)
	authed.GET("/model-mappings/:id", modelMappingHandler.Get)
	authed.PUT("/model-mappings/:id", modelMappingHandler.Update)
	authed.DELETE("/model-mappings/:id", modelMappingHandler.Delete)
	authed.POST("/model-mappings/:id/enable", modelMappingHandler.Enable)
	authed.POST("/model-mappings/:id/disable", modelMappingHandler.Disable)

	payloadRuleHandler := handlers.NewModelPayloadRuleHandler(db)
	authed.GET("/model-mappings/:id/payload-rules", payloadRuleHandler.List)
	authed.POST("/model-mappings/:id/payload-rules", payloadRuleHandler.Create)
	authed.PUT("/model-mappings/:id/payload-rules/:rule_id", payloadRuleHandler.Update)
	authed.DELETE("/model-mappings/:id/payload-rules/:rule_id", payloadRuleHandler.Delete)

	modelReferenceHandler := handlers.NewModelReferenceHandler(db)
	authed.GET("/model-references/price", modelReferenceHandler.GetPrice)

	logsHandler := handlers.NewAdminLogsHandler(db)
	authed.GET("/logs", logsHandler.List)
	authed.GET("/logs/detail", logsHandler.Detail)
	authed.GET("/logs/stats", logsHandler.Stats)
	authed.GET("/logs/trend", logsHandler.Trend)
	authed.GET("/logs/models", logsHandler.Models)
	authed.GET("/logs/projects", logsHandler.Projects)

	planHandler := handlers.NewPlanHandler(db)
	authed.POST("/plans", planHandler.Create)
	authed.GET("/plans", planHandler.List)
	authed.GET("/plans/:id", planHandler.Get)
	authed.PUT("/plans/:id", planHandler.Update)
	authed.DELETE("/plans/:id", planHandler.Delete)
	authed.POST("/plans/:id/enable", planHandler.Enable)
	authed.POST("/plans/:id/disable", planHandler.Disable)

	settingHandler := handlers.NewSettingHandler(db)
	authed.POST("/settings", settingHandler.Create)
	authed.GET("/settings", settingHandler.List)
	authed.GET("/settings/:key", settingHandler.Get)
	authed.PUT("/settings/:key", settingHandler.Update)
	authed.DELETE("/settings/:key", settingHandler.Delete)

	dashboardHandler := handlers.NewDashboardHandler(db)
	authed.GET("/dashboard/kpi", dashboardHandler.KPI)
	authed.GET("/dashboard/traffic", dashboardHandler.Traffic)
	authed.GET("/dashboard/cost-distribution", dashboardHandler.CostDistribution)
	authed.GET("/dashboard/model-health", dashboardHandler.ModelHealth)
	authed.GET("/dashboard/transactions", dashboardHandler.RecentTransactions)

	if baseHandler != nil && baseHandler.AuthManager != nil {
		tokenRequester := sdkapi.NewManagementTokenRequester(cfg, baseHandler.AuthManager)
		authed.POST("/tokens/anthropic", tokenRequester.RequestAnthropicToken)
		authed.POST("/tokens/gemini", tokenRequester.RequestGeminiCLIToken)
		authed.POST("/tokens/codex", tokenRequester.RequestCodexToken)
		authed.POST("/tokens/antigravity", tokenRequester.RequestAntigravityToken)
		authed.POST("/tokens/qwen", tokenRequester.RequestQwenToken)
		authed.POST("/tokens/iflow", tokenRequester.RequestIFlowToken)
		authed.POST("/tokens/iflow-cookie", tokenRequester.RequestIFlowCookieToken)
		authed.POST("/tokens/get-auth-status", tokenRequester.GetAuthStatus)
		authed.POST("/tokens/oauth-callback", tokenRequester.PostOAuthCallback)
	}
}

// adminAuthMiddleware validates admin JWTs and loads admin context.
func adminAuthMiddleware(db *gorm.DB, jwtCfg config.JWTConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == authHeader {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization format"})
			return
		}
		token = strings.TrimSpace(token)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "empty token"})
			return
		}

		claims, errJWT := security.ParseAdminToken(jwtCfg.Secret, token)
		if errJWT != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}

		var admin models.Admin
		if errFind := db.WithContext(c.Request.Context()).First(&admin, claims.AdminID).Error; errFind != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "admin not found"})
			return
		}
		if !admin.Active {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin disabled"})
			return
		}

		adminPermissions := permissions.ParsePermissions(admin.Permissions)
		c.Set("adminID", admin.ID)
		c.Set("adminUsername", admin.Username)
		c.Set("adminPermissions", adminPermissions)
		c.Set("adminIsSuperAdmin", admin.IsSuperAdmin)
		c.Next()
	}
}
