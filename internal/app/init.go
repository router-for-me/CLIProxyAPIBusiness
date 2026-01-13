package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/config"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/db"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/security"
	internalsettings "github.com/router-for-me/CLIProxyAPIBusiness/internal/settings"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/webui"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

// InitRequest contains parameters for initial system setup.
type InitRequest struct {
	DatabaseType     string `json:"database_type"`
	DatabaseHost     string `json:"database_host"`
	DatabasePort     int    `json:"database_port"`
	DatabaseUser     string `json:"database_user"`
	DatabasePassword string `json:"database_password"`
	DatabaseName     string `json:"database_name"`
	DatabasePath     string `json:"database_path"`
	DatabaseSSLMode  string `json:"database_ssl_mode"`
	SiteName         string `json:"site_name"`
	AdminUsername    string `json:"admin_username" binding:"required"`
	AdminPassword    string `json:"admin_password" binding:"required"`
}

// InitStatusResponse reports whether initialization is complete.
type InitStatusResponse struct {
	Initialized bool `json:"initialized"`
}

// ConfigExists reports whether the config file exists at the path.
func ConfigExists(configPath string) bool {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return false
	}
	return true
}

// defaultSQLitePath is the default SQLite database file name.
const defaultSQLitePath = "cpab.db"

// BuildDSN builds a database DSN from the init request.
func BuildDSN(req InitRequest) (string, error) {
	switch strings.ToLower(strings.TrimSpace(req.DatabaseType)) {
	case "", "postgres":
		sslMode := req.DatabaseSSLMode
		if sslMode == "" {
			sslMode = "disable"
		}
		return fmt.Sprintf(
			"postgres://%s:%s@%s:%d/%s?sslmode=%s",
			req.DatabaseUser,
			req.DatabasePassword,
			req.DatabaseHost,
			req.DatabasePort,
			req.DatabaseName,
			sslMode,
		), nil
	case "sqlite":
		path := strings.TrimSpace(req.DatabasePath)
		if path == "" {
			path = defaultSQLitePath
		}
		return buildSQLiteDSN(path), nil
	default:
		return "", fmt.Errorf("unsupported database type")
	}
}

// buildSQLiteDSN constructs a SQLite DSN with default parameters.
func buildSQLiteDSN(path string) string {
	dsn := strings.TrimSpace(path)
	if dsn == "" {
		dsn = defaultSQLitePath
	}
	if !strings.HasPrefix(strings.ToLower(dsn), "file:") {
		dsn = "file:" + dsn
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + strings.Join([]string{
		"_busy_timeout=5000",
		"_journal_mode=WAL",
		"_foreign_keys=on",
		"_synchronous=NORMAL",
	}, "&")
}

// TestDatabaseConnection validates that the DSN can connect and ping.
func TestDatabaseConnection(dsn string) error {
	conn, err := db.Open(dsn)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql db: %w", err)
	}
	defer func() {
		err = sqlDB.Close()
		if err != nil {
			log.Errorf("sql db close error: %v", err)
		}
	}()
	return sqlDB.Ping()
}

// validateInitRequest normalizes and validates init input data.
func validateInitRequest(req *InitRequest) error {
	dbType := strings.ToLower(strings.TrimSpace(req.DatabaseType))
	if dbType == "" {
		dbType = "postgres"
	}
	req.DatabaseType = dbType

	switch dbType {
	case "postgres":
		if strings.TrimSpace(req.DatabaseHost) == "" {
			return fmt.Errorf("Database host is required")
		}
		if req.DatabasePort <= 0 {
			return fmt.Errorf("Invalid database port")
		}
		if strings.TrimSpace(req.DatabaseUser) == "" {
			return fmt.Errorf("Database username is required")
		}
		if strings.TrimSpace(req.DatabaseName) == "" {
			return fmt.Errorf("Database name is required")
		}
		if strings.TrimSpace(req.DatabasePassword) == "" {
			return fmt.Errorf("Database password is required")
		}
	case "sqlite":
		if strings.TrimSpace(req.DatabasePath) == "" {
			req.DatabasePath = defaultSQLitePath
		}
	default:
		return fmt.Errorf("Unsupported database type")
	}
	req.SiteName = strings.TrimSpace(req.SiteName)
	if req.SiteName == "" {
		req.SiteName = internalsettings.DefaultSiteName
	}
	return nil
}

// configFile maps YAML fields for the generated config file.
type configFile struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	DatabaseDSN    string `yaml:"database-dsn"`
	Debug          bool   `yaml:"debug"`
	CommercialMode bool   `yaml:"commercial-mode"`
	LoggingToFile  bool   `yaml:"logging-to-file"`
	JWT            jwtCfg `yaml:"jwt"`
	TLS            tlsCfg `yaml:"tls"`
}

// jwtCfg holds JWT settings for the generated config file.
type jwtCfg struct {
	Secret string `yaml:"secret"`
	Expiry string `yaml:"expiry"`
}

// tlsCfg holds TLS settings for the generated config file.
type tlsCfg struct {
	Enable bool   `yaml:"enable"`
	Cert   string `yaml:"cert"`
	Key    string `yaml:"key"`
}

// generateJWTSecret creates a random JWT secret string.
func generateJWTSecret() string {
	secret, err := security.GenerateRandomString(32)
	if err != nil {
		return "change-me-to-a-secure-random-string"
	}
	return secret
}

// WriteConfigFile writes the initial config file to disk.
func WriteConfigFile(configPath string, dsn string, port int) error {
	cfg := configFile{
		Host:           "",
		Port:           port,
		DatabaseDSN:    dsn,
		Debug:          false,
		CommercialMode: false,
		LoggingToFile:  false,
		JWT: jwtCfg{
			Secret: generateJWTSecret(),
			Expiry: "720h",
		},
		TLS: tlsCfg{
			Enable: false,
		},
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	dir := filepath.Dir(configPath)
	if errMkdir := os.MkdirAll(dir, 0755); errMkdir != nil {
		return fmt.Errorf("create config dir: %w", errMkdir)
	}

	if errWrite := os.WriteFile(configPath, data, 0600); errWrite != nil {
		return fmt.Errorf("write config file: %w", errWrite)
	}

	return nil
}

// CreateAdminUser creates the first admin user and seeds the site name.
func CreateAdminUser(dsn string, username, password, siteName string) error {
	conn, err := db.Open(dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	if errMigrate := db.Migrate(conn); errMigrate != nil {
		return fmt.Errorf("migrate database: %w", errMigrate)
	}
	return CreateAdminUserWithConn(conn, username, password, siteName)
}

// CreateAdminUserWithConn creates the first admin user and seeds the site name.
func CreateAdminUserWithConn(conn *gorm.DB, username, password, siteName string) error {
	if conn == nil {
		return fmt.Errorf("open database: nil connection")
	}

	hashedPassword, errHash := security.HashPassword(password)
	if errHash != nil {
		return fmt.Errorf("hash password: %w", errHash)
	}

	now := time.Now().UTC()
	admin := models.Admin{
		Username:     username,
		Password:     hashedPassword,
		Active:       true,
		IsSuperAdmin: true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if errCreate := conn.Create(&admin).Error; errCreate != nil {
		return fmt.Errorf("create admin: %w", errCreate)
	}

	if errSite := upsertSiteNameSetting(conn, siteName); errSite != nil {
		return errSite
	}

	return nil
}

// upsertSiteNameSetting stores the SITE_NAME setting in the database.
func upsertSiteNameSetting(conn *gorm.DB, siteName string) error {
	normalized := strings.TrimSpace(siteName)
	if normalized == "" {
		normalized = internalsettings.DefaultSiteName
	}
	payload, errMarshal := json.Marshal(normalized)
	if errMarshal != nil {
		return fmt.Errorf("db: marshal SITE_NAME setting: %w", errMarshal)
	}
	value := json.RawMessage(payload)

	now := time.Now().UTC()
	res := conn.Model(&models.Setting{}).Where("key = ?", internalsettings.SiteNameKey).
		Updates(map[string]any{
			"value":      value,
			"updated_at": now,
		})
	if res.Error != nil {
		return fmt.Errorf("db: update SITE_NAME setting: %w", res.Error)
	}
	if res.RowsAffected > 0 {
		return nil
	}

	setting := models.Setting{
		Key:       internalsettings.SiteNameKey,
		Value:     value,
		UpdatedAt: now,
	}
	if errCreate := conn.Create(&setting).Error; errCreate != nil {
		return fmt.Errorf("db: create SITE_NAME setting: %w", errCreate)
	}
	return nil
}

// ErrInitCompleted signals that initialization finished and the server should restart.
var ErrInitCompleted = fmt.Errorf("init completed")

// corsMiddleware enables permissive CORS for the init server.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-API-Key")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// RunInitServer starts the initialization server when config is missing.
func RunInitServer(ctx context.Context, cfg config.AppConfig, port int) error {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(corsMiddleware())

	webBundle, errLoad := webui.Load()
	if errLoad != nil {
		return errLoad
	}

	engine.StaticFS("/assets", webBundle.AssetsFS)

	configPath := config.ResolveConfigPath(cfg.ConfigPath)

	initDone := make(chan struct{})

	engine.GET("/v0/init/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, InitStatusResponse{Initialized: ConfigExists(configPath)})
	})

	engine.POST("/v0/init/setup", func(c *gin.Context) {
		if ConfigExists(configPath) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "System already initialized"})
			return
		}

		var req InitRequest
		if errBind := c.ShouldBindJSON(&req); errBind != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errBind.Error()})
			return
		}

		if errValidate := validateInitRequest(&req); errValidate != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errValidate.Error()})
			return
		}

		if len(req.AdminPassword) < 6 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Password must be at least 6 characters"})
			return
		}

		dsn, errBuild := BuildDSN(req)
		if errBuild != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errBuild.Error()})
			return
		}

		if errTest := TestDatabaseConnection(dsn); errTest != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Database connection failed: %v", errTest)})
			return
		}

		if errWrite := WriteConfigFile(configPath, dsn, port); errWrite != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to write config: %v", errWrite)})
			return
		}

		if errAdmin := CreateAdminUser(dsn, req.AdminUsername, req.AdminPassword, req.SiteName); errAdmin != nil {
			if errRemove := os.Remove(configPath); errRemove != nil {
				log.Errorf("remove config file error: %v", errRemove)
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create admin: %v", errAdmin)})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Initialization successful"})

		go func() {
			time.Sleep(500 * time.Millisecond)
			close(initDone)
		}()
	})

	engine.GET("/init", func(c *gin.Context) {
		if ConfigExists(configPath) {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", webBundle.IndexHTML)
	})

	engine.NoRoute(func(c *gin.Context) {
		if ConfigExists(configPath) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "System initializing, please restart the server"})
			return
		}
		c.Redirect(http.StatusTemporaryRedirect, "/init")
	})

	addr := fmt.Sprintf(":%d", port)
	log.Infof("starting init server on %s (config not found at %s)", addr, configPath)

	srv := &http.Server{
		Addr:    addr,
		Handler: engine,
	}

	go func() {
		select {
		case <-ctx.Done():
		case <-initDone:
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if errShutdown := srv.Shutdown(shutdownCtx); errShutdown != nil {
			log.Errorf("init server shutdown error: %v", errShutdown)
		}
	}()

	if errListen := srv.ListenAndServe(); errListen != nil && errListen != http.ErrServerClosed {
		return errListen
	}

	select {
	case <-initDone:
		return ErrInitCompleted
	default:
		return nil
	}
}
