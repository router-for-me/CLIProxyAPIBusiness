package app

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"time"

	sdkaccess "github.com/router-for-me/CLIProxyAPI/v6/sdk/access"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/access"
	internalauth "github.com/router-for-me/CLIProxyAPIBusiness/internal/auth"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/config"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/db"
	relayhttp "github.com/router-for-me/CLIProxyAPIBusiness/internal/http"
	internalhttp "github.com/router-for-me/CLIProxyAPIBusiness/internal/http/api/admin"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/http/api/front"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/modelregistry"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/quota"
	internalsettings "github.com/router-for-me/CLIProxyAPIBusiness/internal/settings"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/store"
	internalusage "github.com/router-for-me/CLIProxyAPIBusiness/internal/usage"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/watcher"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/webui"

	"github.com/gin-gonic/gin"
	sdkapi "github.com/router-for-me/CLIProxyAPI/v6/sdk/api"
	sdkhandlers "github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v6/sdk/auth"
	sdkcliproxy "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	log "github.com/sirupsen/logrus"
)

// CreateAPIKeyParams holds inputs for API key creation.
type CreateAPIKeyParams struct {
	Name                   string
	Admin                  bool
	BillingRateMicrosPer1K int64
	BillingCurrency        string
}

// Migrate opens the database and runs migrations.
func Migrate(ctx context.Context, cfg config.AppConfig) error {
	configPath := config.ResolveConfigPath(cfg.ConfigPath)
	dsn, err := config.LoadDatabaseDSN(configPath)
	if err != nil {
		return err
	}
	conn, err := db.Open(dsn)
	if err != nil {
		return err
	}
	return db.Migrate(conn)
}

// RunServer boots the API relay server with database-backed components.
func RunServer(ctx context.Context, cfg config.AppConfig, defaultPort int) error {
	configPath := config.ResolveConfigPath(cfg.ConfigPath)
	dsn, err := config.LoadDatabaseDSN(configPath)
	if err != nil {
		return err
	}
	webBundle, errLoad := webui.Load()
	if errLoad != nil {
		return errLoad
	}
	conn, err := db.Open(dsn)
	if err != nil {
		return err
	}
	if errMigrate := db.Migrate(conn); errMigrate != nil {
		return errMigrate
	}

	initialized, errInit := HasAdminInitialized(conn)
	if errInit != nil {
		return errInit
	}
	var initState atomic.Bool
	initState.Store(initialized)
	modelStore := modelregistry.NewStore()
	sdkcliproxy.SetGlobalModelRegistryHook(modelregistry.NewHook(conn, modelStore))

	access.RegisterDBAPIKeyProvider(conn)

	jwtConfig, _ := config.LoadJWTConfig(configPath)

	authStore := store.NewGormAuthStore(conn)
	sdkAuth.RegisterTokenStore(authStore)

	coreCfg, err := loadCoreConfig(configPath)
	if err != nil {
		return err
	}
	coreCfg.CommercialMode = true
	coreCfg.DisableCooling = true
	coreCfg.RemoteManagement.DisableControlPanel = true
	coreCfg.AuthDir, _ = os.Getwd()
	if coreCfg.Port <= 0 {
		if defaultPort <= 0 {
			defaultPort = 8318
		}
		coreCfg.Port = defaultPort
	}
	if len(coreCfg.Access.Providers) == 0 {
		coreCfg.Access.Providers = []sdkconfig.AccessProvider{
			{
				Name: "db",
				Type: access.ProviderTypeDBAPIKey,
				Config: map[string]any{
					"bypass-path-prefixes": []string{"/healthz", "/v0/management"},
					"header":               "Authorization",
					"scheme":               "Bearer",
					"allow-x-api-key":      true,
				},
			},
		}
	}

	enforcementAccessMgr, errBuildAccessMgr := buildAccessManager(coreCfg)
	if errBuildAccessMgr != nil {
		return errBuildAccessMgr
	}
	serverAccessMgr := sdkaccess.NewManager()

	coreManager := coreauth.NewManager(authStore, internalauth.NewSelector(conn), internalauth.NewStatusCodeHook())
	distFS := webBundle.DistFS
	fileServer := http.FileServer(http.FS(distFS))
	builder := sdkcliproxy.NewBuilder().
		WithConfig(coreCfg).
		WithConfigPath(configPath).
		WithWatcherFactory(watcher.NewDatabaseWatcherFactory(conn)).
		WithRequestAccessManager(serverAccessMgr).
		WithCoreAuthManager(coreManager).
		WithServerOptions(
			sdkapi.WithMiddleware(
				func(c *gin.Context) {
					if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
						return
					}
					if c.Request.URL.Path != "/" {
						return
					}
					if initState.Load() {
						return
					}
					c.Redirect(http.StatusTemporaryRedirect, "/init")
					c.Abort()
				},
				webUIRootMiddleware(webBundle.IndexHTML),
				relayhttp.CLIProxyAuthMiddleware(enforcementAccessMgr, coreCfg.WebsocketAuth),
				relayhttp.CLIProxyModelsMiddleware(conn, modelStore),
			),
			sdkapi.WithRouterConfigurator(func(engine *gin.Engine, baseHandler *sdkhandlers.BaseAPIHandler, cfg *sdkconfig.Config) {
				internalhttp.RegisterAdminRoutes(engine, conn, jwtConfig, configPath, cfg, baseHandler)
				front.RegisterFrontRoutes(engine, conn, jwtConfig, modelStore)
				engine.StaticFS("/assets", webBundle.AssetsFS)
				engine.GET("/v0/init/status", func(c *gin.Context) {
					c.JSON(http.StatusOK, InitStatusResponse{Initialized: initState.Load()})
				})
				engine.GET("/v0/init/prefill", func(c *gin.Context) {
					prefill, errPrefill := initPrefillFromDSN(dsn)
					if errPrefill != nil {
						c.JSON(http.StatusOK, gin.H{"locked": true})
						return
					}
					c.JSON(http.StatusOK, struct {
						Locked bool `json:"locked"`
						initPrefill
					}{Locked: true, initPrefill: prefill})
				})
				engine.POST("/v0/init/setup", func(c *gin.Context) {
					if ok, errInit := HasAdminInitialized(conn); errInit != nil {
						c.JSON(http.StatusInternalServerError, gin.H{"error": "check admin status failed"})
						return
					} else if ok {
						initState.Store(true)
						c.JSON(http.StatusBadRequest, gin.H{"error": "System already initialized"})
						return
					}

					var req InitRequest
					if errBind := c.ShouldBindJSON(&req); errBind != nil {
						c.JSON(http.StatusBadRequest, gin.H{"error": errBind.Error()})
						return
					}

					req.SiteName = strings.TrimSpace(req.SiteName)
					if req.SiteName == "" {
						req.SiteName = internalsettings.DefaultSiteName
					}

					req.AdminUsername = strings.TrimSpace(req.AdminUsername)
					if req.AdminUsername == "" {
						c.JSON(http.StatusBadRequest, gin.H{"error": "Admin username is required"})
						return
					}
					if strings.TrimSpace(req.AdminPassword) == "" {
						c.JSON(http.StatusBadRequest, gin.H{"error": "Admin password is required"})
						return
					}
					if len(req.AdminPassword) < 6 {
						c.JSON(http.StatusBadRequest, gin.H{"error": "Password must be at least 6 characters"})
						return
					}

					if errAdmin := CreateAdminUserWithConn(conn, req.AdminUsername, req.AdminPassword, req.SiteName); errAdmin != nil {
						c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create admin: %v", errAdmin)})
						return
					}
					initState.Store(true)
					c.JSON(http.StatusOK, gin.H{"message": "Initialization successful"})
				})
				engine.NoRoute(func(c *gin.Context) {
					if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
						c.Status(http.StatusNotFound)
						return
					}
					requestPath := c.Request.URL.Path
					if isAPIRoute(requestPath) {
						c.Status(http.StatusNotFound)
						return
					}
					cleanedPath := path.Clean("/" + requestPath)
					filePath := strings.TrimPrefix(cleanedPath, "/")
					if filePath != "" {
						fileInfo, errStat := fs.Stat(distFS, filePath)
						if errStat == nil && !fileInfo.IsDir() {
							fileServer.ServeHTTP(c.Writer, c.Request)
							return
						}
						if requestPath == "/assets" || strings.HasPrefix(requestPath, "/assets/") || strings.Contains(path.Base(filePath), ".") {
							c.Status(http.StatusNotFound)
							return
						}
					}
					if requestPath == "/init" {
						if initState.Load() {
							c.Status(http.StatusNotFound)
							return
						}
						c.Data(http.StatusOK, "text/html; charset=utf-8", webBundle.IndexHTML)
						return
					}
					if !initState.Load() {
						c.Redirect(http.StatusTemporaryRedirect, "/init")
						return
					}
					c.Data(http.StatusOK, "text/html; charset=utf-8", webBundle.IndexHTML)
				})
			}),
		)

	service, err := builder.Build()
	if err != nil {
		return err
	}
	service.RegisterUsagePlugin(internalusage.NewGormUsagePlugin(conn))
	if quotaPoller := quota.NewPoller(conn, coreManager); quotaPoller != nil {
		quotaPoller.Start(ctx)
	}

	serverAccessMgr.SetProviders(nil)

	log.Infof("starting relay with config=%s", cfg.ConfigPath)
	return service.Run(ctx)
}

func loadCoreConfig(configPath string) (*sdkconfig.Config, error) {
	if ConfigExists(configPath) {
		cfg, errLoad := sdkconfig.LoadConfig(configPath)
		if errLoad != nil {
			return nil, fmt.Errorf("load cliproxy config: %w", errLoad)
		}
		return cfg, nil
	}
	cfg, errLoad := sdkconfig.LoadConfigOptional(configPath, true)
	if errLoad != nil {
		return nil, fmt.Errorf("load cliproxy config: %w", errLoad)
	}
	return cfg, nil
}

// nowUTC returns the current UTC time.
func nowUTC() time.Time { return time.Now().UTC() }

// webUIRootMiddleware serves the index HTML at the root path.
func webUIRootMiddleware(indexHTML []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			return
		}
		if c.Request.URL.Path != "/" {
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
		c.Abort()
	}
}

// isAPIRoute reports whether a path targets API endpoints.
func isAPIRoute(requestPath string) bool {
	if requestPath == "/healthz" || strings.HasPrefix(requestPath, "/healthz/") {
		return true
	}
	apiPrefixes := []string{"/v0", "/v1", "/v1beta"}
	for _, prefix := range apiPrefixes {
		if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
			return true
		}
	}
	return false
}

// buildAccessManager builds an access manager from SDK config providers.
func buildAccessManager(cfg *sdkconfig.Config) (*sdkaccess.Manager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	providers, err := sdkaccess.BuildProviders(&cfg.SDKConfig)
	if err != nil {
		return nil, err
	}
	manager := sdkaccess.NewManager()
	manager.SetProviders(providers)
	return manager, nil
}
