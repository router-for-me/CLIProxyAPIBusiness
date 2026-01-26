package watcher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	sdkcliproxy "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	internalaccess "github.com/router-for-me/CLIProxyAPIBusiness/internal/access"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/modelmapping"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/providerkeys"
	internalsettings "github.com/router-for-me/CLIProxyAPIBusiness/internal/settings"
	log "github.com/sirupsen/logrus"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Default timings and buffer sizes for the watcher loop.
const (
	// defaultPollInterval controls how often DB snapshots are refreshed.
	defaultPollInterval = 2 * time.Second
	// defaultQueryTimeout bounds DB query duration.
	defaultQueryTimeout = 10 * time.Second
	// defaultDispatchBuffer defines the pending update buffer size.
	defaultDispatchBuffer = 2048
)

// authState caches an auth hash and its last update time.
type authState struct {
	hash      string
	updatedAt time.Time
}

// authUpdate describes a pending auth update action.
type authUpdate struct {
	action string
	id     string
	auth   *coreauth.Auth
}

// payloadParamEntry represents a payload rule parameter entry.
type payloadParamEntry struct {
	Path      string `json:"path"`
	RuleType  string `json:"rule_type"`
	ValueType string `json:"value_type"`
	Value     any    `json:"value"`
}

// payloadRuleRow mirrors the DB row used to build payload configs.
type payloadRuleRow struct {
	ID             uint64         `gorm:"column:id"`               // Payload rule ID.
	ModelMappingID uint64         `gorm:"column:model_mapping_id"` // Related model mapping ID.
	Protocol       string         `gorm:"column:protocol"`         // Protocol name.
	Params         datatypes.JSON `gorm:"column:params"`           // Raw rule parameters.
	RuleEnabled    bool           `gorm:"column:is_enabled"`       // Rule enabled flag.
	ModelName      string         `gorm:"column:new_model_name"`   // Mapped model name.
	MappingEnabled bool           `gorm:"column:mapping_enabled"`  // Mapping enabled flag.
}

// updateEncoder captures reflection details for encoding auth updates.
type updateEncoder struct {
	updateType  reflect.Type
	fieldAction int
	fieldID     int
	fieldAuth   int
}

// dbWatcher polls DB/config/auth sources and dispatches auth updates.
type dbWatcher struct {
	db         *gorm.DB
	configPath string
	authDir    string
	reload     func(*sdkconfig.Config)

	pollInterval time.Duration

	// config polling
	cfgMu     sync.RWMutex
	cfg       *sdkconfig.Config
	cfgHash   string
	forceAuth bool

	// auth snapshot
	authMu       sync.RWMutex
	authStates   map[string]authState
	lastAuths    []*coreauth.Auth
	maxUpdatedAt time.Time
	maxUpdatedID uint64

	// settings snapshot (global db config)
	settingsLatestAt  time.Time
	settingsLatestKey string
	hasSettingsLatest bool

	// payload rule snapshot
	payloadLatestAt  time.Time
	payloadLatestID  uint64
	payloadHasLatest bool
	payloadCount     int64
	payloadHasCount  bool
	mappingLatestAt  time.Time
	mappingLatestID  uint64
	mappingHasLatest bool

	// provider key snapshot (stored in ProviderAPIKey + ModelMapping tables)
	providerLatestAt  time.Time
	providerLatestID  uint64
	providerHasLatest bool
	oauthLatestAt     time.Time
	oauthLatestID     uint64
	oauthHasLatest    bool

	// dispatch queue
	queueMu sync.RWMutex
	queue   reflect.Value
	encoder *updateEncoder

	dispatchMu     sync.Mutex
	dispatchCond   *sync.Cond
	pending        map[string]authUpdate
	pendingOrder   []string
	dispatchCtx    context.Context
	dispatchCancel context.CancelFunc
	wg             sync.WaitGroup
}

// NewDatabaseWatcherFactory builds a watcher factory backed by database polling.
func NewDatabaseWatcherFactory(db *gorm.DB) sdkcliproxy.WatcherFactory {
	return func(configPath, authDir string, reload func(*sdkconfig.Config)) (*sdkcliproxy.WatcherWrapper, error) {
		w := &dbWatcher{
			db:           db,
			configPath:   strings.TrimSpace(configPath),
			authDir:      strings.TrimSpace(authDir),
			reload:       reload,
			pollInterval: defaultPollInterval,
			authStates:   make(map[string]authState),
			pending:      make(map[string]authUpdate, defaultDispatchBuffer),
		}
		w.dispatchCond = sync.NewCond(&w.dispatchMu)
		return buildWatcherWrapper(w)
	}
}

// Start launches polling and dispatch goroutines for the watcher.
func (w *dbWatcher) Start(ctx context.Context) error {
	if w == nil || w.db == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	w.dispatchMu.Lock()
	if w.dispatchCancel == nil {
		w.dispatchCtx, w.dispatchCancel = context.WithCancel(context.Background())
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			w.dispatchLoop(w.dispatchCtx)
		}()
	}
	w.dispatchMu.Unlock()

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.run(ctx)
	}()

	log.Infof("db watcher started (poll_interval=%s)", w.pollInterval)
	return nil
}

// Stop cancels background tasks and waits for them to exit.
func (w *dbWatcher) Stop() error {
	if w == nil {
		return nil
	}
	w.dispatchMu.Lock()
	if w.dispatchCancel != nil {
		w.dispatchCancel()
		w.dispatchCancel = nil
	}
	if w.dispatchCond != nil {
		w.dispatchCond.Broadcast()
	}
	w.dispatchMu.Unlock()
	w.wg.Wait()
	return nil
}

// SetConfig updates the cached configuration snapshot.
func (w *dbWatcher) SetConfig(cfg *sdkconfig.Config) {
	if w == nil {
		return
	}
	w.cfgMu.Lock()
	w.cfg = cfg
	w.cfgMu.Unlock()
}

// SnapshotAuths returns a deep-cloned list of the current auths.
func (w *dbWatcher) SnapshotAuths() []*coreauth.Auth {
	if w == nil {
		return nil
	}
	w.authMu.RLock()
	defer w.authMu.RUnlock()
	out := make([]*coreauth.Auth, 0, len(w.lastAuths))
	for _, a := range w.lastAuths {
		if a != nil {
			out = append(out, a.Clone())
		}
	}
	return out
}

// SetAuthUpdateQueue configures the channel used for runtime auth updates.
func (w *dbWatcher) SetAuthUpdateQueue(queue reflect.Value) {
	if w == nil {
		return
	}
	if !queue.IsValid() || queue.Kind() != reflect.Chan {
		return
	}
	encoder, errEncoder := buildUpdateEncoder(queue.Type().Elem())
	if errEncoder != nil {
		log.WithError(errEncoder).Warn("db watcher: failed to build update encoder")
		return
	}

	w.queueMu.Lock()
	w.queue = queue
	w.encoder = encoder
	w.queueMu.Unlock()
}

// DispatchRuntimeAuthUpdate opts out of SDK-level dispatching.
func (w *dbWatcher) DispatchRuntimeAuthUpdate(_ reflect.Value) bool {
	// Let the SDK service fall back to its own auth update queue.
	return false
}

// run executes the periodic polling loop until the context is canceled.
func (w *dbWatcher) run(ctx context.Context) {
	w.pollConfig(ctx)
	w.pollProviderKeys(ctx, true)
	w.pollAuth(ctx, true)
	w.pollSettings(ctx, true)
	w.pollPayloadRules(ctx, true)

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.pollConfig(ctx)
			w.pollProviderKeys(ctx, false)
			w.pollAuth(ctx, w.consumeForceAuth())
			w.pollSettings(ctx, false)
			w.pollPayloadRules(ctx, false)
		}
	}
}

// pollConfig reloads the config file when its contents change.
func (w *dbWatcher) pollConfig(ctx context.Context) {
	if w == nil || strings.TrimSpace(w.configPath) == "" {
		return
	}

	data, errRead := os.ReadFile(w.configPath)
	if errRead != nil || len(data) == 0 {
		return
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	w.cfgMu.RLock()
	prevHash := w.cfgHash
	w.cfgMu.RUnlock()
	if prevHash != "" && prevHash == hash {
		return
	}

	cfg, errLoad := sdkconfig.LoadConfig(w.configPath)
	if errLoad != nil {
		log.WithError(errLoad).Warn("db watcher: load config failed")
		return
	}
	ensureDBAccessProvider(cfg)
	cfg.RemoteManagement.DisableControlPanel = true
	cfg.AuthDir, _ = os.Getwd()

	w.cfgMu.Lock()
	w.cfg = cfg
	w.cfgHash = hash
	w.cfgMu.Unlock()

	if w.reload != nil {
		w.reload(cfg)
	}

	w.markForceAuth()
}

// pollProviderKeys rebuilds provider API key config from DB when it changes.
func (w *dbWatcher) pollProviderKeys(ctx context.Context, force bool) {
	if w == nil || w.db == nil {
		return
	}
	qctx, cancel := context.WithTimeout(ctx, defaultQueryTimeout)
	defer cancel()

	type latestRow struct {
		ID        uint64     `gorm:"column:id"`
		UpdatedAt *time.Time `gorm:"column:updated_at"`
	}

	var latestProvider latestRow
	hasProvider := false
	errProvider := w.db.WithContext(qctx).
		Model(&models.ProviderAPIKey{}).
		Select("id", "updated_at").
		Order("updated_at DESC, id DESC").
		Limit(1).
		Take(&latestProvider).Error
	if errProvider != nil {
		if errors.Is(errProvider, context.Canceled) {
			return
		}
		if errors.Is(errProvider, gorm.ErrRecordNotFound) {
			hasProvider = false
		} else {
			log.WithError(errProvider).Warn("db watcher: query provider api keys latest row failed")
			return
		}
	} else {
		hasProvider = true
	}

	var latestMapping latestRow
	hasMapping := false
	errMapping := w.db.WithContext(qctx).
		Model(&models.ModelMapping{}).
		Select("id", "updated_at").
		Order("updated_at DESC, id DESC").
		Limit(1).
		Take(&latestMapping).Error
	if errMapping != nil {
		if errors.Is(errMapping, context.Canceled) {
			return
		}
		if errors.Is(errMapping, gorm.ErrRecordNotFound) {
			hasMapping = false
		} else {
			log.WithError(errMapping).Warn("db watcher: query oauth model mappings latest row failed")
			return
		}
	} else {
		hasMapping = true
	}

	providerAt := time.Time{}
	providerID := uint64(0)
	if hasProvider && latestProvider.UpdatedAt != nil {
		providerAt = latestProvider.UpdatedAt.UTC()
		providerID = latestProvider.ID
	}

	mappingAt := time.Time{}
	mappingID := uint64(0)
	if hasMapping && latestMapping.UpdatedAt != nil {
		mappingAt = latestMapping.UpdatedAt.UTC()
		mappingID = latestMapping.ID
	}

	if !force {
		if w.providerHasLatest == hasProvider &&
			w.oauthHasLatest == hasMapping &&
			(!hasProvider || (providerAt.Equal(w.providerLatestAt) && providerID == w.providerLatestID)) &&
			(!hasMapping || (mappingAt.Equal(w.oauthLatestAt) && mappingID == w.oauthLatestID)) {
			return
		}
	}

	var providerRows []models.ProviderAPIKey
	if errFind := w.db.WithContext(qctx).
		Order("id ASC").
		Find(&providerRows).Error; errFind != nil {
		if errors.Is(errFind, context.Canceled) {
			return
		}
		log.WithError(errFind).Warn("db watcher: query provider api keys failed")
		return
	}

	var mappingRows []models.ModelMapping
	if errFindMappings := w.db.WithContext(qctx).
		Model(&models.ModelMapping{}).
		Where("is_enabled = ?", true).
		Order("provider ASC, new_model_name ASC, model_name ASC").
		Find(&mappingRows).Error; errFindMappings != nil {
		if errors.Is(errFindMappings, context.Canceled) {
			return
		}
		log.WithError(errFindMappings).Warn("db watcher: query oauth model mappings failed")
		return
	}

	w.cfgMu.RLock()
	baseCfg := w.cfg
	w.cfgMu.RUnlock()
	if baseCfg == nil {
		baseCfg = &sdkconfig.Config{}
	}
	next := *baseCfg
	providerkeys.ApplyToConfig(&next, providerRows, mappingRows)

	w.cfgMu.Lock()
	w.cfg = &next
	w.cfgMu.Unlock()

	if w.reload != nil {
		w.reload(&next)
	}
	w.markForceAuth()

	w.providerHasLatest = hasProvider
	w.providerLatestAt = providerAt
	w.providerLatestID = providerID
	w.oauthHasLatest = hasMapping
	w.oauthLatestAt = mappingAt
	w.oauthLatestID = mappingID
}

// ensureDBAccessProvider injects a DB access provider when none are configured.
func ensureDBAccessProvider(cfg *sdkconfig.Config) {
	if cfg == nil {
		return
	}
	if len(cfg.Access.Providers) > 0 {
		return
	}
	cfg.Access.Providers = []sdkconfig.AccessProvider{
		{
			Name: "db",
			Type: internalaccess.ProviderTypeDBAPIKey,
			Config: map[string]any{
				"bypass-path-prefixes": []string{"/healthz", "/v0/management"},
				"header":               "Authorization",
				"scheme":               "Bearer",
				"allow-x-api-key":      true,
			},
		},
	}
}

// markForceAuth flags that auths should be polled immediately.
func (w *dbWatcher) markForceAuth() {
	w.cfgMu.Lock()
	w.forceAuth = true
	w.cfgMu.Unlock()
}

// consumeForceAuth returns and clears the force-auth flag.
func (w *dbWatcher) consumeForceAuth() bool {
	w.cfgMu.Lock()
	force := w.forceAuth
	w.forceAuth = false
	w.cfgMu.Unlock()
	return force
}

// pollAuth refreshes auth snapshot and enqueues updates when needed.
func (w *dbWatcher) pollAuth(ctx context.Context, force bool) {
	if w == nil || w.db == nil {
		return
	}
	qctx, cancel := context.WithTimeout(ctx, defaultQueryTimeout)
	defer cancel()

	// latestRow captures the newest auth record timestamp for change detection.
	type latestRow struct {
		ID        uint64     `gorm:"column:id"`         // Latest auth ID.
		UpdatedAt *time.Time `gorm:"column:updated_at"` // Latest auth update timestamp.
	}
	var latest latestRow
	hasLatest := false
	errLatest := w.db.WithContext(qctx).
		Model(&models.Auth{}).
		Select("id", "updated_at").
		Order("updated_at DESC, id DESC").
		Limit(1).
		Take(&latest).Error
	if errLatest != nil {
		if errors.Is(errLatest, context.Canceled) {
			return
		}
		if errors.Is(errLatest, gorm.ErrRecordNotFound) {
			hasLatest = false
		} else {
			log.WithError(errLatest).Warn("db watcher: query auth_records latest row failed")
			return
		}
	} else {
		hasLatest = true
	}

	w.authMu.RLock()
	prevMax := w.maxUpdatedAt
	prevMaxID := w.maxUpdatedID
	prevStates := w.authStates
	w.authMu.RUnlock()

	maxUpdatedAt := time.Time{}
	maxUpdatedID := uint64(0)
	if hasLatest {
		maxUpdatedID = latest.ID
		if latest.UpdatedAt != nil {
			maxUpdatedAt = latest.UpdatedAt.UTC()
		}
	}
	if !force {
		if !hasLatest || latest.UpdatedAt == nil {
			if len(prevStates) == 0 {
				return
			}
		} else if maxUpdatedAt.After(prevMax) {
			// Continue.
		} else if maxUpdatedAt.Equal(prevMax) && maxUpdatedID > prevMaxID {
			// Continue (tie-breaker for same updated_at).
		} else {
			return
		}
	}

	log.Infof("db watcher: auth_records changed, reloading (max_updated_at=%s max_id=%d)", maxUpdatedAt.Format(time.RFC3339Nano), maxUpdatedID)

	var rows []models.Auth
	if errFind := w.db.WithContext(qctx).
		Select("key", "content", "priority", "created_at", "updated_at").
		Where("is_available = ?", true).
		Order("id ASC").
		Find(&rows).Error; errFind != nil {
		if errors.Is(errFind, context.Canceled) {
			return
		}
		log.WithError(errFind).Warn("db watcher: query auth records failed")
		return
	}

	nextStates := make(map[string]authState, len(rows))
	nextAuths := make([]*coreauth.Auth, 0, len(rows))
	nextAuthByID := make(map[string]*coreauth.Auth, len(rows))

	for _, row := range rows {
		key := strings.TrimSpace(row.Key)
		if key == "" || len(row.Content) == 0 {
			continue
		}
		hash := hashBytes(row.Content)
		nextStates[key] = authState{hash: hash, updatedAt: row.UpdatedAt}

		a := synthesizeAuthFromDBRow(w.authDir, key, row.Content, row.Priority, row.CreatedAt, row.UpdatedAt)
		if a == nil || a.ID == "" {
			continue
		}
		nextAuths = append(nextAuths, a)
		nextAuthByID[a.ID] = a
	}

	w.cfgMu.RLock()
	cfgSnapshot := w.cfg
	w.cfgMu.RUnlock()
	configAuths := synthesizeConfigAuths(cfgSnapshot)
	for _, auth := range configAuths {
		if auth == nil || auth.ID == "" {
			continue
		}
		key := auth.ID
		nextStates[key] = authState{hash: hashAuth(auth), updatedAt: time.Time{}}
		nextAuths = append(nextAuths, auth)
		nextAuthByID[key] = auth
	}

	for id, st := range nextStates {
		prev, ok := prevStates[id]
		switch {
		case !ok:
			if auth := nextAuthByID[id]; auth != nil {
				w.enqueueUpdate(authUpdate{action: "add", id: id, auth: auth.Clone()})
			}
		case force || prev.hash != st.hash || !prev.updatedAt.Equal(st.updatedAt):
			if auth := nextAuthByID[id]; auth != nil {
				w.enqueueUpdate(authUpdate{action: "modify", id: id, auth: auth.Clone()})
			}
		}
	}

	for id := range prevStates {
		if _, ok := nextStates[id]; ok {
			continue
		}
		w.enqueueUpdate(authUpdate{action: "delete", id: id})
	}

	w.authMu.Lock()
	w.authStates = nextStates
	w.lastAuths = nextAuths
	w.maxUpdatedAt = maxUpdatedAt
	w.maxUpdatedID = maxUpdatedID
	w.authMu.Unlock()
}

// pollSettings refreshes DB-backed settings and updates the in-memory config snapshot.
func (w *dbWatcher) pollSettings(ctx context.Context, force bool) {
	if w == nil || w.db == nil {
		return
	}
	qctx, cancel := context.WithTimeout(ctx, defaultQueryTimeout)
	defer cancel()

	// latestRow captures the newest setting timestamp for change detection.
	type latestRow struct {
		Key       string     `gorm:"column:key"`        // Latest settings key.
		UpdatedAt *time.Time `gorm:"column:updated_at"` // Latest settings update time.
	}
	var latest latestRow
	hasLatest := false
	errLatest := w.db.WithContext(qctx).
		Model(&models.Setting{}).
		Select("key", "updated_at").
		Order("updated_at DESC NULLS LAST, key DESC").
		Limit(1).
		Take(&latest).Error
	if errLatest != nil {
		if errors.Is(errLatest, context.Canceled) {
			return
		}
		if errors.Is(errLatest, gorm.ErrRecordNotFound) {
			hasLatest = false
		} else {
			log.WithError(errLatest).Warn("db watcher: query settings latest row failed")
			return
		}
	} else {
		hasLatest = true
	}

	latestKey := strings.TrimSpace(latest.Key)
	latestAt := time.Time{}
	if hasLatest && latest.UpdatedAt != nil {
		latestAt = latest.UpdatedAt.UTC()
	}

	if !force {
		if !hasLatest || latest.UpdatedAt == nil {
			if !w.hasSettingsLatest {
				return
			}
		} else if w.hasSettingsLatest && latestAt.Equal(w.settingsLatestAt) && latestKey == w.settingsLatestKey {
			return
		}
	}

	log.Infof("db watcher: settings changed, reloading (latest_updated_at=%s latest_key=%s)", latestAt.Format(time.RFC3339Nano), latestKey)

	var rows []models.Setting
	if errFind := w.db.WithContext(qctx).
		Select("key", "value", "updated_at").
		Order("key ASC").
		Find(&rows).Error; errFind != nil {
		if errors.Is(errFind, context.Canceled) {
			return
		}
		log.WithError(errFind).Warn("db watcher: query settings failed")
		return
	}

	values := make(map[string]json.RawMessage, len(rows))
	maxUpdatedAt := time.Time{}
	maxUpdatedKey := ""
	for _, row := range rows {
		key := strings.TrimSpace(row.Key)
		if key == "" {
			continue
		}
		values[key] = row.Value

		rowUpdatedAt := row.UpdatedAt.UTC()
		if rowUpdatedAt.After(maxUpdatedAt) || (rowUpdatedAt.Equal(maxUpdatedAt) && key > maxUpdatedKey) {
			maxUpdatedAt = rowUpdatedAt
			maxUpdatedKey = key
		}
	}

	internalsettings.StoreDBConfig(maxUpdatedAt, values)

	if !hasLatest || latest.UpdatedAt == nil || latestKey == "" {
		w.settingsLatestAt = time.Time{}
		w.settingsLatestKey = ""
		w.hasSettingsLatest = false
		return
	}
	w.settingsLatestAt = latestAt
	w.settingsLatestKey = latestKey
	w.hasSettingsLatest = true
}

// pollPayloadRules reloads payload rules when changes are detected.
func (w *dbWatcher) pollPayloadRules(ctx context.Context, force bool) {
	if w == nil || w.db == nil {
		return
	}
	qctx, cancel := context.WithTimeout(ctx, defaultQueryTimeout)
	defer cancel()

	// latestRow captures the newest payload rule timestamp for change detection.
	type latestRow struct {
		ID        uint64     `gorm:"column:id"`         // Latest payload rule ID.
		UpdatedAt *time.Time `gorm:"column:updated_at"` // Latest payload rule update time.
	}

	var latestPayload latestRow
	payloadHasLatest := false
	resultPayload := w.db.WithContext(qctx).
		Model(&models.ModelPayloadRule{}).
		Select("id", "updated_at").
		Order("updated_at DESC, id DESC").
		Limit(1).
		Find(&latestPayload)
	if errPayloadFind := resultPayload.Error; errPayloadFind != nil {
		if errors.Is(errPayloadFind, context.Canceled) {
			return
		}
		log.WithError(errPayloadFind).Warn("db watcher: query payload rules latest row failed")
		return
	}
	if resultPayload.RowsAffected > 0 {
		payloadHasLatest = true
	}

	payloadHasCount := false
	payloadCount := int64(0)
	if errCount := w.db.WithContext(qctx).
		Model(&models.ModelPayloadRule{}).
		Count(&payloadCount).Error; errCount != nil {
		if errors.Is(errCount, context.Canceled) {
			return
		}
		log.WithError(errCount).Warn("db watcher: query payload rules count failed")
		return
	}
	payloadHasCount = true

	var latestMapping latestRow
	mappingHasLatest := false
	errMapping := w.db.WithContext(qctx).
		Model(&models.ModelMapping{}).
		Select("id", "updated_at").
		Order("updated_at DESC, id DESC").
		Limit(1).
		Take(&latestMapping).Error
	if errMapping != nil {
		if errors.Is(errMapping, context.Canceled) {
			return
		}
		if errors.Is(errMapping, gorm.ErrRecordNotFound) {
			mappingHasLatest = false
		} else {
			log.WithError(errMapping).Warn("db watcher: query model mappings latest row failed")
			return
		}
	} else {
		mappingHasLatest = true
	}

	payloadAt := time.Time{}
	payloadID := uint64(0)
	if payloadHasLatest && latestPayload.UpdatedAt != nil {
		payloadAt = latestPayload.UpdatedAt.UTC()
		payloadID = latestPayload.ID
	}
	mappingAt := time.Time{}
	mappingID := uint64(0)
	if mappingHasLatest && latestMapping.UpdatedAt != nil {
		mappingAt = latestMapping.UpdatedAt.UTC()
		mappingID = latestMapping.ID
	}

	payloadSame := (!payloadHasLatest && !w.payloadHasLatest) ||
		(payloadHasLatest && w.payloadHasLatest && payloadAt.Equal(w.payloadLatestAt) && payloadID == w.payloadLatestID)
	payloadCountSame := w.payloadHasCount && payloadCount == w.payloadCount
	mappingSame := (!mappingHasLatest && !w.mappingHasLatest) ||
		(mappingHasLatest && w.mappingHasLatest && mappingAt.Equal(w.mappingLatestAt) && mappingID == w.mappingLatestID)

	if !force {
		if payloadSame && payloadCountSame && mappingSame {
			return
		}
	}

	log.Infof(
		"db watcher: payload rules changed, reloading (payload_updated_at=%s payload_count=%d mapping_updated_at=%s)",
		payloadAt.Format(time.RFC3339Nano),
		payloadCount,
		mappingAt.Format(time.RFC3339Nano),
	)

	var mappingRows []models.ModelMapping
	errFindMappings := w.db.WithContext(qctx).
		Model(&models.ModelMapping{}).
		Select("id", "provider", "model_name", "new_model_name", "selector", "rate_limit", "fork", "is_enabled", "user_group_id").
		Find(&mappingRows).Error
	if errFindMappings != nil {
		if errors.Is(errFindMappings, context.Canceled) {
			return
		}
		log.WithError(errFindMappings).Warn("db watcher: query model mappings failed")
	}

	oauthMappingsLoaded := errFindMappings == nil
	oauthModelMappings := buildOAuthModelMappings(mappingRows)
	if oauthMappingsLoaded {
		modelmapping.StoreModelMappings(mappingAt, mappingRows)
	}

	var rows []payloadRuleRow
	if errFind := w.db.WithContext(qctx).
		Table("model_payload_rules").
		Select(`model_payload_rules.id,
			model_payload_rules.model_mapping_id,
			model_payload_rules.protocol,
			model_payload_rules.params,
			model_payload_rules.is_enabled,
			model_mappings.new_model_name,
			model_mappings.is_enabled as mapping_enabled`).
		Joins("JOIN model_mappings ON model_payload_rules.model_mapping_id = model_mappings.id").
		Order("model_payload_rules.id ASC").
		Find(&rows).Error; errFind != nil {
		if errors.Is(errFind, context.Canceled) {
			return
		}
		log.WithError(errFind).Warn("db watcher: query payload rules failed")
		return
	}

	payloadConfig := buildPayloadConfig(rows)

	w.cfgMu.RLock()
	cfg := w.cfg
	w.cfgMu.RUnlock()
	if cfg != nil {
		next := *cfg
		next.Payload = payloadConfig
		if oauthMappingsLoaded {
			next.OAuthModelAlias = oauthModelMappings
			next.SanitizeOAuthModelAlias()
		}
		w.cfgMu.Lock()
		w.cfg = &next
		w.cfgMu.Unlock()
		if w.reload != nil {
			w.reload(&next)
		}
		if oauthMappingsLoaded && !mappingSame {
			w.markForceAuth()
		}
	}

	w.payloadLatestAt = payloadAt
	w.payloadLatestID = payloadID
	w.payloadHasLatest = payloadHasLatest
	w.payloadCount = payloadCount
	w.payloadHasCount = payloadHasCount
	w.mappingLatestAt = mappingAt
	w.mappingLatestID = mappingID
	w.mappingHasLatest = mappingHasLatest
}

// buildPayloadConfig converts payload rule rows into SDK payload configuration.
func buildPayloadConfig(rows []payloadRuleRow) sdkconfig.PayloadConfig {
	defaultRules := make([]sdkconfig.PayloadRule, 0)
	defaultRawRules := make([]sdkconfig.PayloadRule, 0)
	overrideRules := make([]sdkconfig.PayloadRule, 0)
	overrideRawRules := make([]sdkconfig.PayloadRule, 0)

	for _, row := range rows {
		if !row.RuleEnabled || !row.MappingEnabled {
			continue
		}
		modelName := strings.TrimSpace(row.ModelName)
		if modelName == "" {
			continue
		}
		entries, errParse := parsePayloadParamEntries(row.Params)
		if errParse != nil {
			log.WithError(errParse).Warn("db watcher: parse payload params failed")
			continue
		}
		if len(entries) == 0 {
			continue
		}
		defaultParams := make(map[string]any)
		defaultRawParams := make(map[string]any)
		overrideParams := make(map[string]any)
		overrideRawParams := make(map[string]any)
		for _, entry := range entries {
			path := strings.TrimSpace(entry.Path)
			if path == "" {
				continue
			}
			ruleType := strings.ToLower(strings.TrimSpace(entry.RuleType))
			isRaw := strings.EqualFold(strings.TrimSpace(entry.ValueType), "json")
			if isRaw {
				value := entry.Value
				if value == nil {
					value = []byte("null")
				} else if s, ok := value.(string); ok {
					trimmed := strings.TrimSpace(s)
					if json.Valid([]byte(trimmed)) {
						value = trimmed
					} else if raw, errMarshal := json.Marshal(s); errMarshal == nil {
						value = raw
					} else {
						continue
					}
				}
				if ruleType == "override" {
					overrideRawParams[path] = value
					continue
				}
				defaultRawParams[path] = value
				continue
			}
			if ruleType == "override" {
				overrideParams[path] = entry.Value
				continue
			}
			defaultParams[path] = entry.Value
		}

		modelRule := sdkconfig.PayloadModelRule{
			Name:     modelName,
			Protocol: strings.TrimSpace(row.Protocol),
		}
		if len(defaultParams) > 0 {
			defaultRules = append(defaultRules, sdkconfig.PayloadRule{
				Models: []sdkconfig.PayloadModelRule{modelRule},
				Params: defaultParams,
			})
		}
		if len(defaultRawParams) > 0 {
			defaultRawRules = append(defaultRawRules, sdkconfig.PayloadRule{
				Models: []sdkconfig.PayloadModelRule{modelRule},
				Params: defaultRawParams,
			})
		}
		if len(overrideParams) > 0 {
			overrideRules = append(overrideRules, sdkconfig.PayloadRule{
				Models: []sdkconfig.PayloadModelRule{modelRule},
				Params: overrideParams,
			})
		}
		if len(overrideRawParams) > 0 {
			overrideRawRules = append(overrideRawRules, sdkconfig.PayloadRule{
				Models: []sdkconfig.PayloadModelRule{modelRule},
				Params: overrideRawParams,
			})
		}
	}

	return sdkconfig.PayloadConfig{
		Default:     defaultRules,
		DefaultRaw:  defaultRawRules,
		Override:    overrideRules,
		OverrideRaw: overrideRawRules,
	}
}

func buildOAuthModelMappings(rows []models.ModelMapping) map[string][]sdkconfig.OAuthModelAlias {
	if len(rows) == 0 {
		return nil
	}

	out := make(map[string][]sdkconfig.OAuthModelAlias)
	seen := make(map[string]struct{})

	for i := range rows {
		row := &rows[i]
		if !row.IsEnabled {
			continue
		}
		provider := strings.ToLower(strings.TrimSpace(row.Provider))
		name := strings.TrimSpace(row.ModelName)
		alias := strings.TrimSpace(row.NewModelName)
		if provider == "" || name == "" || alias == "" {
			continue
		}
		key := provider + "\x00" + strings.ToLower(name) + "\x00" + strings.ToLower(alias)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out[provider] = append(out[provider], sdkconfig.OAuthModelAlias{
			Name:  name,
			Alias: alias,
			Fork:  row.Fork,
		})
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// parsePayloadParamEntries parses JSON params into a normalized entry list.
func parsePayloadParamEntries(raw datatypes.JSON) ([]payloadParamEntry, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	switch trimmed[0] {
	case '[':
		var entries []payloadParamEntry
		if errUnmarshal := json.Unmarshal(trimmed, &entries); errUnmarshal != nil {
			return nil, errUnmarshal
		}
		return entries, nil
	case '{':
		var obj map[string]any
		if errUnmarshal := json.Unmarshal(trimmed, &obj); errUnmarshal != nil {
			return nil, errUnmarshal
		}
		entries := make([]payloadParamEntry, 0, len(obj))
		for key, value := range obj {
			entry := payloadParamEntry{Path: key, RuleType: "default"}
			if nested, ok := value.(map[string]any); ok {
				if rt, okRule := nested["rule_type"].(string); okRule {
					entry.RuleType = rt
				}
				if vt, okValueType := nested["value_type"].(string); okValueType {
					entry.ValueType = vt
				}
				if v, okValue := nested["value"]; okValue {
					entry.Value = v
				} else {
					entry.Value = value
				}
			} else {
				entry.Value = value
			}
			entries = append(entries, entry)
		}
		return entries, nil
	default:
		return nil, nil
	}
}

// enqueueUpdate stores an auth update for later dispatch.
func (w *dbWatcher) enqueueUpdate(update authUpdate) {
	if w == nil || update.id == "" {
		return
	}
	w.dispatchMu.Lock()
	if _, exists := w.pending[update.id]; !exists {
		w.pendingOrder = append(w.pendingOrder, update.id)
	}
	w.pending[update.id] = update
	if w.dispatchCond != nil {
		w.dispatchCond.Signal()
	}
	w.dispatchMu.Unlock()
}

// dispatchLoop sends queued updates to the configured channel until canceled.
func (w *dbWatcher) dispatchLoop(ctx context.Context) {
	for {
		queue, encoder := w.queueSnapshot()
		if !queue.IsValid() || encoder == nil {
			if ctx.Err() != nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		batch, ok := w.nextBatch(ctx)
		if !ok {
			return
		}
		for _, update := range batch {
			val, okEncode := encodeUpdate(encoder, update)
			if !okEncode {
				continue
			}
			func() {
				defer func() { _ = recover() }()
				queue.Send(val)
			}()
		}
	}
}

// nextBatch waits for pending updates and returns the next batch.
func (w *dbWatcher) nextBatch(ctx context.Context) ([]authUpdate, bool) {
	w.dispatchMu.Lock()
	defer w.dispatchMu.Unlock()
	for len(w.pendingOrder) == 0 {
		if ctx.Err() != nil {
			return nil, false
		}
		w.dispatchCond.Wait()
		if ctx.Err() != nil {
			return nil, false
		}
	}
	out := make([]authUpdate, 0, len(w.pendingOrder))
	for _, id := range w.pendingOrder {
		out = append(out, w.pending[id])
		delete(w.pending, id)
	}
	w.pendingOrder = w.pendingOrder[:0]
	return out, true
}

// queueSnapshot returns the current queue and encoder under lock.
func (w *dbWatcher) queueSnapshot() (reflect.Value, *updateEncoder) {
	w.queueMu.RLock()
	defer w.queueMu.RUnlock()
	return w.queue, w.encoder
}

// buildUpdateEncoder prepares reflection indices for encoding auth updates.
func buildUpdateEncoder(updateType reflect.Type) (*updateEncoder, error) {
	if updateType == nil || updateType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("invalid update type")
	}
	action, ok := updateType.FieldByName("Action")
	if !ok || action.Type.Kind() != reflect.String {
		return nil, fmt.Errorf("missing Action field")
	}
	id, ok := updateType.FieldByName("ID")
	if !ok || id.Type.Kind() != reflect.String {
		return nil, fmt.Errorf("missing ID field")
	}
	auth, ok := updateType.FieldByName("Auth")
	if !ok {
		return nil, fmt.Errorf("missing Auth field")
	}
	if auth.Type.Kind() != reflect.Pointer {
		return nil, fmt.Errorf("invalid Auth field type")
	}
	return &updateEncoder{
		updateType:  updateType,
		fieldAction: action.Index[0],
		fieldID:     id.Index[0],
		fieldAuth:   auth.Index[0],
	}, nil
}

// encodeUpdate converts an authUpdate into the SDK update struct value.
func encodeUpdate(enc *updateEncoder, update authUpdate) (reflect.Value, bool) {
	if enc == nil || enc.updateType == nil || update.id == "" {
		return reflect.Value{}, false
	}
	v := reflect.New(enc.updateType).Elem()
	v.Field(enc.fieldAction).SetString(update.action)
	v.Field(enc.fieldID).SetString(update.id)
	if update.auth != nil {
		v.Field(enc.fieldAuth).Set(reflect.ValueOf(update.auth))
	}
	return v, true
}

// synthesizeAuthFromDBRow builds an auth entry from the stored JSON payload.
func synthesizeAuthFromDBRow(authDir string, key string, payload []byte, priority int, createdAt, updatedAt time.Time) *coreauth.Auth {
	var metadata map[string]any
	if errUnmarshal := json.Unmarshal(payload, &metadata); errUnmarshal != nil {
		return nil
	}
	t, _ := metadata["type"].(string)
	if strings.TrimSpace(t) == "" {
		return nil
	}

	provider := strings.ToLower(strings.TrimSpace(t))
	if provider == "gemini" {
		provider = "gemini-cli"
	}

	label := provider
	if email, _ := metadata["email"].(string); strings.TrimSpace(email) != "" {
		label = strings.TrimSpace(email)
	}

	proxyURL := ""
	if v, ok := metadata["proxy_url"].(string); ok {
		proxyURL = strings.TrimSpace(v)
	}

	prefix := ""
	if rawPrefix, ok := metadata["prefix"].(string); ok {
		trimmed := strings.TrimSpace(rawPrefix)
		trimmed = strings.Trim(trimmed, "/")
		if trimmed != "" && !strings.Contains(trimmed, "/") {
			prefix = trimmed
		}
	}

	attrs := make(map[string]string, 3)
	if p := safeJoinAuthPath(authDir, key); p != "" {
		attrs["source"] = p
		attrs["path"] = p
	}
	if priority != 0 {
		attrs["priority"] = strconv.Itoa(priority)
	}

	return &coreauth.Auth{
		ID:         strings.TrimSpace(key),
		Provider:   provider,
		Label:      label,
		Prefix:     prefix,
		Status:     coreauth.StatusActive,
		Attributes: attrs,
		ProxyURL:   proxyURL,
		Metadata:   metadata,
		CreatedAt:  createdAt.UTC(),
		UpdatedAt:  updatedAt.UTC(),
	}
}

// safeJoinAuthPath joins baseDir and name while preventing path traversal.
func safeJoinAuthPath(baseDir string, name string) string {
	baseDir = strings.TrimSpace(baseDir)
	name = strings.TrimSpace(name)
	if baseDir == "" || name == "" {
		return ""
	}
	if filepath.IsAbs(name) {
		return ""
	}
	cleaned := filepath.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return ""
	}
	full := filepath.Clean(filepath.Join(baseDir, cleaned))
	base := filepath.Clean(baseDir)
	if full != base && !strings.HasPrefix(full, base+string(os.PathSeparator)) {
		return ""
	}
	return full
}

// hashBytes returns the SHA-256 hex digest of the input bytes.
func hashBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// buildWatcherWrapper wires dbWatcher methods into the SDK watcher wrapper.
func buildWatcherWrapper(w *dbWatcher) (*sdkcliproxy.WatcherWrapper, error) {
	wrapper := &sdkcliproxy.WatcherWrapper{}
	if w == nil {
		return wrapper, nil
	}

	target := reflect.ValueOf(wrapper).Elem()

	if err := setUnexportedFuncField(target, "start", reflect.ValueOf(w.Start)); err != nil {
		return nil, err
	}
	if err := setUnexportedFuncField(target, "stop", reflect.ValueOf(w.Stop)); err != nil {
		return nil, err
	}
	if err := setUnexportedFuncField(target, "setConfig", reflect.ValueOf(w.SetConfig)); err != nil {
		return nil, err
	}
	if err := setUnexportedFuncField(target, "snapshotAuths", reflect.ValueOf(w.SnapshotAuths)); err != nil {
		return nil, err
	}

	setQueueField := target.FieldByName("setUpdateQueue")
	if !setQueueField.IsValid() {
		return nil, fmt.Errorf("cliproxy watcher wrapper: missing setUpdateQueue field")
	}
	setQueueFunc := reflect.MakeFunc(setQueueField.Type(), func(args []reflect.Value) []reflect.Value {
		if len(args) == 1 {
			w.SetAuthUpdateQueue(args[0])
		}
		return nil
	})
	setUnexportedValue(setQueueField, setQueueFunc)

	dispatchField := target.FieldByName("dispatchRuntimeUpdate")
	if !dispatchField.IsValid() {
		return nil, fmt.Errorf("cliproxy watcher wrapper: missing dispatchRuntimeUpdate field")
	}
	dispatchFunc := reflect.MakeFunc(dispatchField.Type(), func(args []reflect.Value) []reflect.Value {
		if len(args) == 1 {
			ok := w.DispatchRuntimeAuthUpdate(args[0])
			out := reflect.New(dispatchField.Type().Out(0)).Elem()
			out.SetBool(ok)
			return []reflect.Value{out}
		}
		out := reflect.New(dispatchField.Type().Out(0)).Elem()
		out.SetBool(false)
		return []reflect.Value{out}
	})
	setUnexportedValue(dispatchField, dispatchFunc)

	if err := verifyWatcherWrapper(wrapper); err != nil {
		return nil, err
	}
	return wrapper, nil
}

// verifyWatcherWrapper validates required function fields on the wrapper.
func verifyWatcherWrapper(wrapper *sdkcliproxy.WatcherWrapper) error {
	if wrapper == nil {
		return fmt.Errorf("cliproxy watcher wrapper: nil")
	}
	target := reflect.ValueOf(wrapper).Elem()
	required := []string{"start", "stop", "setConfig", "snapshotAuths", "setUpdateQueue", "dispatchRuntimeUpdate"}
	for _, name := range required {
		field := target.FieldByName(name)
		if !field.IsValid() {
			return fmt.Errorf("cliproxy watcher wrapper: missing %s field", name)
		}
		if field.Kind() != reflect.Func {
			return fmt.Errorf("cliproxy watcher wrapper: %s is not a func field", name)
		}
		val := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
		if val.IsNil() {
			return fmt.Errorf("cliproxy watcher wrapper: %s is nil", name)
		}
	}
	return nil
}

// setUnexportedFuncField assigns a function to an unexported wrapper field.
func setUnexportedFuncField(target reflect.Value, name string, fn reflect.Value) error {
	field := target.FieldByName(name)
	if !field.IsValid() {
		return fmt.Errorf("cliproxy watcher wrapper: missing %s field", name)
	}
	if fn.IsValid() && fn.Type().AssignableTo(field.Type()) {
		setUnexportedValue(field, fn)
		return nil
	}
	if fn.IsValid() && fn.Type().ConvertibleTo(field.Type()) {
		setUnexportedValue(field, fn.Convert(field.Type()))
		return nil
	}
	return fmt.Errorf("cliproxy watcher wrapper: %s type mismatch", name)
}

// setUnexportedValue sets an unexported reflect field via unsafe access.
func setUnexportedValue(field reflect.Value, value reflect.Value) {
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(value)
}
