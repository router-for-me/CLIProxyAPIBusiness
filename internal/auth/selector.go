package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/modelmapping"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/ratelimit"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	modelMappingSelectorRoundRobin = 0
	modelMappingSelectorFillFirst  = 1
	modelMappingSelectorStick      = 2
)

// Selector chooses an auth candidate per model mapping selector rules.
type Selector struct {
	db *gorm.DB

	roundRobinCursor atomic.Uint64

	rateLimiter      *ratelimit.Manager
	resolveRateLimit func(ctx context.Context, db *gorm.DB, userID uint64, provider, model, authKey string) (ratelimit.Decision, error)
}

// NewSelector constructs a selector backed by the application database.
func NewSelector(db *gorm.DB) *Selector {
	return &Selector{
		db:               db,
		rateLimiter:      ratelimit.NewManager(ratelimit.LoadSettingsConfig, time.Now, nil),
		resolveRateLimit: ratelimit.ResolveLimit,
	}
}

// Pick implements coreauth.Selector.
func (s *Selector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*coreauth.Auth) (*coreauth.Auth, error) {
	_ = opts
	if ctx == nil {
		ctx = context.Background()
	}

	now := time.Now()
	available, errAvailable := getAvailableAuths(auths, provider, model, now)
	if errAvailable != nil {
		return nil, errAvailable
	}

	mappingID, selector := s.loadModelMappingSelector(ctx, provider, model)
	var selected *coreauth.Auth
	var errPick error
	switch selector {
	case modelMappingSelectorFillFirst:
		selected = s.pickFillFirst(ctx, available)
	case modelMappingSelectorStick:
		selected, errPick = s.pickStick(ctx, provider, model, mappingID, available)
	default:
		selected = s.pickRoundRobin(available)
	}
	if errPick != nil {
		return nil, errPick
	}
	if errLimit := s.applyRateLimit(ctx, provider, model, selected); errLimit != nil {
		return nil, errLimit
	}
	return selected, nil
}

func (s *Selector) applyRateLimit(ctx context.Context, provider, model string, selected *coreauth.Auth) error {
	if s == nil || s.db == nil || s.rateLimiter == nil || s.resolveRateLimit == nil {
		return nil
	}
	if !shouldApplyRateLimit(ctx) {
		return nil
	}
	userID, okUser := userIDFromContext(ctx)
	if !okUser {
		return nil
	}

	authKey := ""
	if selected != nil {
		authKey = strings.TrimSpace(selected.ID)
	}
	decision, errResolve := s.resolveRateLimit(ctx, s.db, userID, provider, model, authKey)
	if errResolve != nil {
		log.WithError(errResolve).Warn("rate limit: resolve failed")
		return nil
	}
	if decision.Limit <= 0 {
		return nil
	}
	key := ratelimit.KeyForDecision(userID, decision)
	if key == "" {
		return nil
	}
	result, errAllow := s.rateLimiter.Allow(ctx, key, decision.Limit)
	if errAllow != nil {
		log.WithError(errAllow).Warn("rate limit: check failed")
		return nil
	}
	if !result.Allowed {
		resetIn := result.Reset.Sub(time.Now())
		return newRateLimitError(resetIn)
	}
	return nil
}

func (s *Selector) pickRoundRobin(available []*coreauth.Auth) *coreauth.Auth {
	if len(available) == 0 {
		return nil
	}
	if len(available) == 1 {
		return available[0]
	}
	index := s.roundRobinCursor.Add(1) - 1
	return available[index%uint64(len(available))]
}

func (s *Selector) pickFillFirst(ctx context.Context, available []*coreauth.Auth) *coreauth.Auth {
	if len(available) == 0 {
		return nil
	}
	if len(available) == 1 {
		return available[0]
	}
	if s == nil || s.db == nil {
		return available[0]
	}

	keys := make([]string, 0, len(available))
	for _, auth := range available {
		if auth == nil {
			continue
		}
		if key := strings.TrimSpace(auth.ID); key != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return available[0]
	}

	type authRow struct {
		ID  uint64 `gorm:"column:id"`
		Key string `gorm:"column:key"`
	}

	var rows []authRow
	if errFind := s.db.WithContext(ctx).
		Model(&models.Auth{}).
		Select("id", "key").
		Where("key IN ?", keys).
		Find(&rows).Error; errFind != nil {
		return available[0]
	}

	idByKey := make(map[string]uint64, len(rows))
	for _, row := range rows {
		key := strings.TrimSpace(row.Key)
		if key == "" {
			continue
		}
		idByKey[key] = row.ID
	}

	best := available[0]
	bestID := ^uint64(0)
	for _, auth := range available {
		if auth == nil {
			continue
		}
		dbID := idByKey[strings.TrimSpace(auth.ID)]
		if dbID == 0 {
			dbID = ^uint64(0)
		}
		if dbID < bestID {
			bestID = dbID
			best = auth
		}
	}
	if best == nil {
		return available[0]
	}
	return best
}

func (s *Selector) pickStick(ctx context.Context, provider, model string, mappingID uint64, available []*coreauth.Auth) (*coreauth.Auth, error) {
	if len(available) == 0 {
		return nil, &coreauth.Error{Code: "auth_not_found", Message: "no auth candidates"}
	}
	if s.db == nil || mappingID == 0 {
		return s.pickRoundRobin(available), nil
	}

	userID, okUser := userIDFromContext(ctx)
	if !okUser {
		return s.pickRoundRobin(available), nil
	}

	var binding models.UserModelAuthBinding
	errFind := s.db.WithContext(ctx).
		Where("user_id = ? AND model_mapping_id = ?", userID, mappingID).
		Take(&binding).Error
	switch {
	case errFind == nil:
		boundIndex := strings.TrimSpace(binding.AuthIndex)
		if boundIndex != "" {
			for _, auth := range available {
				if auth == nil {
					continue
				}
				if strings.EqualFold(authIndexFor(auth), boundIndex) {
					return auth, nil
				}
			}
		}
	case errors.Is(errFind, gorm.ErrRecordNotFound):
	default:
		return s.pickRoundRobin(available), nil
	}

	selected, selectedIndex, errSelect := s.selectLeastUsedAuth(ctx, userID, provider, model, available)
	if errSelect != nil || selected == nil {
		return s.pickRoundRobin(available), nil
	}
	selectedIndex = strings.TrimSpace(selectedIndex)
	if selectedIndex == "" {
		selectedIndex = strings.TrimSpace(selected.ID)
	}
	if selectedIndex == "" {
		return selected, nil
	}

	now := time.Now().UTC()
	row := models.UserModelAuthBinding{
		UserID:         userID,
		ModelMappingID: mappingID,
		AuthIndex:      selectedIndex,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	_ = s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "model_mapping_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"auth_index",
			"updated_at",
		}),
	}).Create(&row).Error

	return selected, nil
}

func (s *Selector) selectLeastUsedAuth(ctx context.Context, userID uint64, provider, model string, available []*coreauth.Auth) (*coreauth.Auth, string, error) {
	if s == nil || s.db == nil {
		return nil, "", fmt.Errorf("nil db")
	}
	if len(available) == 0 {
		return nil, "", fmt.Errorf("no auth candidates")
	}

	candidates := make([]string, 0, len(available))
	for _, auth := range available {
		if auth == nil {
			continue
		}
		idx := strings.TrimSpace(authIndexFor(auth))
		if idx == "" {
			idx = strings.TrimSpace(auth.ID)
		}
		if idx == "" {
			continue
		}
		candidates = append(candidates, idx)
	}
	if len(candidates) == 0 {
		return available[0], "", nil
	}

	type countRow struct {
		AuthIndex string `gorm:"column:auth_index"`
		Count     int64  `gorm:"column:cnt"`
	}

	var rows []countRow
	if errQuery := s.db.WithContext(ctx).
		Model(&models.Usage{}).
		Select("auth_index, COUNT(*) AS cnt").
		Where("user_id = ? AND provider = ? AND model = ? AND auth_index IN ?", userID, provider, model, candidates).
		Group("auth_index").
		Find(&rows).Error; errQuery != nil {
		return nil, "", errQuery
	}

	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		key := strings.TrimSpace(row.AuthIndex)
		if key == "" {
			continue
		}
		counts[key] = row.Count
	}

	best := available[0]
	bestIndex := strings.TrimSpace(authIndexFor(best))
	if bestIndex == "" {
		bestIndex = strings.TrimSpace(best.ID)
	}
	bestCount := counts[bestIndex]

	for _, auth := range available {
		if auth == nil {
			continue
		}
		idx := strings.TrimSpace(authIndexFor(auth))
		if idx == "" {
			idx = strings.TrimSpace(auth.ID)
		}
		if idx == "" {
			continue
		}
		count := counts[idx]
		if count < bestCount {
			best = auth
			bestIndex = idx
			bestCount = count
		}
	}

	if best == nil {
		return nil, "", fmt.Errorf("no auth selected")
	}
	return best, bestIndex, nil
}

func (s *Selector) loadModelMappingSelector(_ context.Context, provider, model string) (uint64, int) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" || s == nil {
		return 0, modelMappingSelectorRoundRobin
	}

	mappingID, selector, ok := modelmapping.LookupSelector(provider, model)
	if !ok {
		return 0, modelMappingSelectorRoundRobin
	}
	return mappingID, normalizeModelMappingSelector(selector)
}

func normalizeModelMappingSelector(value int) int {
	switch value {
	case modelMappingSelectorRoundRobin, modelMappingSelectorFillFirst, modelMappingSelectorStick:
		return value
	default:
		return modelMappingSelectorRoundRobin
	}
}

func shouldApplyRateLimit(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	ginCtx, ok := ctx.Value("gin").(*gin.Context)
	if !ok || ginCtx == nil || ginCtx.Request == nil || ginCtx.Request.URL == nil {
		return false
	}
	path := strings.TrimSpace(ginCtx.Request.URL.Path)
	if path == "" {
		return false
	}
	if strings.HasPrefix(path, "/v1/models") {
		return false
	}
	return strings.HasPrefix(path, "/v1") || strings.HasPrefix(path, "/v1beta") || strings.HasPrefix(path, "/api")
}

func userIDFromContext(ctx context.Context) (uint64, bool) {
	if ctx == nil {
		return 0, false
	}
	ginCtx, ok := ctx.Value("gin").(*gin.Context)
	if !ok || ginCtx == nil {
		return 0, false
	}
	v, exists := ginCtx.Get("accessMetadata")
	if !exists {
		return 0, false
	}
	meta, ok := v.(map[string]string)
	if !ok {
		return 0, false
	}
	raw := strings.TrimSpace(meta["user_id"])
	if raw == "" {
		return 0, false
	}
	parsed, errParse := strconv.ParseUint(raw, 10, 64)
	if errParse != nil || parsed == 0 {
		return 0, false
	}
	return parsed, true
}

func authIndexFor(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if idx := strings.TrimSpace(auth.Index); idx != "" {
		return idx
	}

	seed := strings.TrimSpace(auth.FileName)
	if seed != "" {
		seed = "file:" + seed
	} else if auth.Attributes != nil {
		if apiKey := strings.TrimSpace(auth.Attributes["api_key"]); apiKey != "" {
			seed = "api_key:" + apiKey
		}
	}
	if seed == "" {
		if id := strings.TrimSpace(auth.ID); id != "" {
			seed = "id:" + id
		} else {
			return ""
		}
	}

	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:8])
}

type blockReason int

const (
	blockReasonNone blockReason = iota
	blockReasonCooldown
	blockReasonDisabled
	blockReasonOther
)

type modelCooldownError struct {
	model    string
	resetIn  time.Duration
	provider string
}

func newModelCooldownError(model, provider string, resetIn time.Duration) *modelCooldownError {
	if resetIn < 0 {
		resetIn = 0
	}
	return &modelCooldownError{
		model:    model,
		provider: provider,
		resetIn:  resetIn,
	}
}

func (e *modelCooldownError) Error() string {
	modelName := e.model
	if modelName == "" {
		modelName = "requested model"
	}
	message := fmt.Sprintf("All credentials for model %s are cooling down", modelName)
	if e.provider != "" {
		message = fmt.Sprintf("%s via provider %s", message, e.provider)
	}
	resetSeconds := int(math.Ceil(e.resetIn.Seconds()))
	if resetSeconds < 0 {
		resetSeconds = 0
	}
	displayDuration := e.resetIn
	if displayDuration > 0 && displayDuration < time.Second {
		displayDuration = time.Second
	} else {
		displayDuration = displayDuration.Round(time.Second)
	}
	errorBody := map[string]any{
		"code":          "model_cooldown",
		"message":       message,
		"model":         e.model,
		"reset_time":    displayDuration.String(),
		"reset_seconds": resetSeconds,
	}
	if e.provider != "" {
		errorBody["provider"] = e.provider
	}
	payload := map[string]any{"error": errorBody}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf(`{"error":{"code":"model_cooldown","message":"%s"}}`, message)
	}
	return string(data)
}

func (e *modelCooldownError) StatusCode() int {
	return http.StatusTooManyRequests
}

func (e *modelCooldownError) Headers() http.Header {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	resetSeconds := int(math.Ceil(e.resetIn.Seconds()))
	if resetSeconds < 0 {
		resetSeconds = 0
	}
	headers.Set("Retry-After", strconv.Itoa(resetSeconds))
	return headers
}

type rateLimitError struct {
	resetIn time.Duration
}

func newRateLimitError(resetIn time.Duration) *rateLimitError {
	if resetIn < 0 {
		resetIn = 0
	}
	return &rateLimitError{resetIn: resetIn}
}

func (e *rateLimitError) Error() string {
	return `{"error":"rate limit exceeded"}`
}

func (e *rateLimitError) StatusCode() int {
	return http.StatusTooManyRequests
}

func (e *rateLimitError) Headers() http.Header {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	resetSeconds := int(math.Ceil(e.resetIn.Seconds()))
	if resetSeconds < 0 {
		resetSeconds = 0
	}
	headers.Set("Retry-After", strconv.Itoa(resetSeconds))
	return headers
}

func collectAvailable(auths []*coreauth.Auth, model string, now time.Time) (available []*coreauth.Auth, cooldownCount int, earliest time.Time) {
	available = make([]*coreauth.Auth, 0, len(auths))
	for i := 0; i < len(auths); i++ {
		candidate := auths[i]
		blocked, reason, next := isAuthBlockedForModel(candidate, model, now)
		if !blocked {
			available = append(available, candidate)
			continue
		}
		if reason == blockReasonCooldown {
			cooldownCount++
			if !next.IsZero() && (earliest.IsZero() || next.Before(earliest)) {
				earliest = next
			}
		}
	}
	if len(available) > 1 {
		sort.Slice(available, func(i, j int) bool { return available[i].ID < available[j].ID })
	}
	return available, cooldownCount, earliest
}

func getAvailableAuths(auths []*coreauth.Auth, provider, model string, now time.Time) ([]*coreauth.Auth, error) {
	if len(auths) == 0 {
		return nil, &coreauth.Error{Code: "auth_not_found", Message: "no auth candidates"}
	}

	available, cooldownCount, earliest := collectAvailable(auths, model, now)
	if len(available) == 0 {
		if cooldownCount == len(auths) && !earliest.IsZero() {
			resetIn := earliest.Sub(now)
			if resetIn < 0 {
				resetIn = 0
			}
			return nil, newModelCooldownError(model, provider, resetIn)
		}
		return nil, &coreauth.Error{Code: "auth_unavailable", Message: "no auth available"}
	}

	return available, nil
}

func isAuthBlockedForModel(auth *coreauth.Auth, model string, now time.Time) (bool, blockReason, time.Time) {
	if auth == nil {
		return true, blockReasonOther, time.Time{}
	}
	if auth.Disabled || auth.Status == coreauth.StatusDisabled {
		return true, blockReasonDisabled, time.Time{}
	}
	if model != "" {
		if len(auth.ModelStates) > 0 {
			if state, ok := auth.ModelStates[model]; ok && state != nil {
				if state.Status == coreauth.StatusDisabled {
					return true, blockReasonDisabled, time.Time{}
				}
				if state.Unavailable {
					if state.NextRetryAfter.IsZero() {
						return false, blockReasonNone, time.Time{}
					}
					if state.NextRetryAfter.After(now) {
						next := state.NextRetryAfter
						if !state.Quota.NextRecoverAt.IsZero() && state.Quota.NextRecoverAt.After(now) {
							next = state.Quota.NextRecoverAt
						}
						if next.Before(now) {
							next = now
						}
						if state.Quota.Exceeded {
							return true, blockReasonCooldown, next
						}
						return true, blockReasonOther, next
					}
				}
				return false, blockReasonNone, time.Time{}
			}
		}
		return false, blockReasonNone, time.Time{}
	}
	if auth.Unavailable && auth.NextRetryAfter.After(now) {
		next := auth.NextRetryAfter
		if !auth.Quota.NextRecoverAt.IsZero() && auth.Quota.NextRecoverAt.After(now) {
			next = auth.Quota.NextRecoverAt
		}
		if next.Before(now) {
			next = now
		}
		if auth.Quota.Exceeded {
			return true, blockReasonCooldown, next
		}
		return true, blockReasonOther, next
	}
	return false, blockReasonNone, time.Time{}
}
