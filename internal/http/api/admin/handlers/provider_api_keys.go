package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	dbutil "github.com/router-for-me/CLIProxyAPIBusiness/internal/db"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Canonical provider identifiers for API key configuration.
const (
	providerGemini = "gemini"
	providerCodex  = "codex"
	providerClaude = "claude"
	providerOpenAI = "openai-compatibility"
)

// providerAliases maps provider inputs to canonical identifiers.
var providerAliases = map[string]string{
	"gemini":                    providerGemini,
	"codex":                     providerCodex,
	"claude":                    providerClaude,
	"claude-code":               providerClaude,
	"openai":                    providerOpenAI,
	"openai-chat-completions":   providerOpenAI,
	"openai-compatibility":      providerOpenAI,
	"openai-chat":               providerOpenAI,
	"openai-chatcompletion":     providerOpenAI,
	"openai-chat-completion":    providerOpenAI,
	"openai-chatcompletions":    providerOpenAI,
	"openai-chat-completion-v1": providerOpenAI,
}

// ProviderAPIKeyHandler manages admin CRUD for provider API keys.
type ProviderAPIKeyHandler struct {
	db         *gorm.DB // Database handle for provider keys.
	configPath string   // Config path for SDK sync.
}

// NewProviderAPIKeyHandler constructs a handler and trims config path input.
func NewProviderAPIKeyHandler(db *gorm.DB, configPath string) *ProviderAPIKeyHandler {
	return &ProviderAPIKeyHandler{
		db:         db,
		configPath: strings.TrimSpace(configPath),
	}
}

// modelAlias defines a model alias entry.
type modelAlias struct {
	Name  string `json:"name"`  // Model name.
	Alias string `json:"alias"` // Alias name.
}

// apiKeyEntry defines a single API key entry for openai-compat providers.
type apiKeyEntry struct {
	APIKey   string `json:"api_key"`   // API key value.
	ProxyURL string `json:"proxy_url"` // Optional proxy URL.
}

// createProviderAPIKeyRequest captures the payload for creating provider keys.
type createProviderAPIKeyRequest struct {
	Provider       string            `json:"provider"`        // Provider identifier.
	Name           *string           `json:"name"`            // Optional provider name.
	APIKey         *string           `json:"api_key"`         // Optional API key.
	Prefix         *string           `json:"prefix"`          // Optional prefix.
	BaseURL        *string           `json:"base_url"`        // Optional base URL.
	ProxyURL       *string           `json:"proxy_url"`       // Optional proxy URL.
	Headers        map[string]string `json:"headers"`         // Request headers.
	Models         []modelAlias      `json:"models"`          // Model aliases.
	ExcludedModels []string          `json:"excluded_models"` // Excluded models.
	APIKeyEntries  []apiKeyEntry     `json:"api_key_entries"` // API key entries.
}

// updateProviderAPIKeyRequest captures optional fields for updates.
type updateProviderAPIKeyRequest struct {
	Provider       *string            `json:"provider"`        // Optional provider.
	Name           *string            `json:"name"`            // Optional provider name.
	APIKey         *string            `json:"api_key"`         // Optional API key.
	Prefix         *string            `json:"prefix"`          // Optional prefix.
	BaseURL        *string            `json:"base_url"`        // Optional base URL.
	ProxyURL       *string            `json:"proxy_url"`       // Optional proxy URL.
	Headers        *map[string]string `json:"headers"`         // Optional headers.
	Models         *[]modelAlias      `json:"models"`          // Optional model aliases.
	ExcludedModels *[]string          `json:"excluded_models"` // Optional excluded models.
	APIKeyEntries  *[]apiKeyEntry     `json:"api_key_entries"` // Optional API key entries.
}

// Create validates and inserts a provider API key record, then syncs config.
func (h *ProviderAPIKeyHandler) Create(c *gin.Context) {
	var body createProviderAPIKeyRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	provider := normalizeProvider(body.Provider)
	if provider == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider is required"})
		return
	}

	proxyURL := strings.TrimSpace(derefString(body.ProxyURL))
	if proxyURL == "" && autoAssignProxyEnabled() {
		assignedProxyURL, errAssignProxy := pickRandomProxyURL(c.Request.Context(), h.db)
		if errAssignProxy != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "auto assign proxy failed"})
			return
		}
		if assignedProxyURL != "" {
			proxyURL = assignedProxyURL
		}
	}

	now := time.Now().UTC()
	row := models.ProviderAPIKey{
		Provider:  provider,
		Name:      strings.TrimSpace(derefString(body.Name)),
		APIKey:    strings.TrimSpace(derefString(body.APIKey)),
		Prefix:    strings.TrimSpace(derefString(body.Prefix)),
		BaseURL:   strings.TrimSpace(derefString(body.BaseURL)),
		ProxyURL:  proxyURL,
		CreatedAt: now,
		UpdatedAt: now,
	}

	headersJSON, errHeaders := marshalJSON(body.Headers)
	if errHeaders != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid headers"})
		return
	}
	modelsJSON, errModels := marshalJSON(body.Models)
	if errModels != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid models"})
		return
	}
	excludedJSON, errExcluded := marshalJSON(body.ExcludedModels)
	if errExcluded != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid excluded_models"})
		return
	}
	apiKeyEntriesJSON, errKeyEntries := marshalJSON(body.APIKeyEntries)
	if errKeyEntries != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api_key_entries"})
		return
	}

	row.Headers = headersJSON
	row.Models = modelsJSON
	row.ExcludedModels = excludedJSON
	row.APIKeyEntries = apiKeyEntriesJSON

	normalizeProviderFields(&row)
	if errValidate := validateProviderRow(&row); errValidate != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errValidate.Error()})
		return
	}

	if errCreate := h.db.WithContext(c.Request.Context()).Create(&row).Error; errCreate != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create api key failed"})
		return
	}

	if errSync := h.syncSDKConfig(c.Request.Context()); errSync != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "sync config failed"})
		return
	}

	c.JSON(http.StatusCreated, formatProviderRow(&row))
}

// List returns provider API keys or provider options based on query flags.
func (h *ProviderAPIKeyHandler) List(c *gin.Context) {
	optionsQ := strings.TrimSpace(c.Query("options"))
	if optionsQ == "1" || strings.EqualFold(optionsQ, "true") {
		var rows []models.ProviderAPIKey
		if errFind := h.db.WithContext(c.Request.Context()).
			Model(&models.ProviderAPIKey{}).
			Select("provider", "models", "excluded_models").
			Order("provider ASC, id ASC").
			Find(&rows).Error; errFind != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "list api key providers failed"})
			return
		}

		// providerOption represents provider models for option lists.
		type providerOption struct {
			Provider string   `json:"provider"` // Provider identifier.
			Models   []string `json:"models"`   // Model names.
		}

		seenProviders := make(map[string]struct{})
		providers := make([]string, 0, 8)
		modelsByProvider := make(map[string]map[string]string)

		for i := range rows {
			provider := strings.TrimSpace(rows[i].Provider)
			if provider == "" {
				continue
			}
			if _, ok := seenProviders[provider]; !ok {
				seenProviders[provider] = struct{}{}
				providers = append(providers, provider)
			}
			if _, ok := modelsByProvider[provider]; !ok {
				modelsByProvider[provider] = make(map[string]string)
			}

			for _, alias := range decodeModels(rows[i].Models) {
				value := strings.TrimSpace(alias.Alias)
				if value == "" {
					value = strings.TrimSpace(alias.Name)
				}
				if value == "" {
					continue
				}
				key := strings.ToLower(value)
				if _, exists := modelsByProvider[provider][key]; exists {
					continue
				}
				modelsByProvider[provider][key] = value
			}
		}

		out := make([]providerOption, 0, len(providers))
		for _, provider := range providers {
			modelMap := modelsByProvider[provider]
			modelSlice := make([]string, 0, len(modelMap))
			for _, name := range modelMap {
				modelSlice = append(modelSlice, name)
			}
			sort.Slice(modelSlice, func(i, j int) bool {
				return strings.ToLower(modelSlice[i]) < strings.ToLower(modelSlice[j])
			})
			out = append(out, providerOption{
				Provider: provider,
				Models:   modelSlice,
			})
		}

		c.JSON(http.StatusOK, gin.H{"providers": out})
		return
	}

	rawProvider := strings.TrimSpace(c.Query("provider"))
	providerQ := normalizeProvider(rawProvider)
	keywordQ := strings.TrimSpace(c.Query("keyword"))

	if rawProvider != "" && providerQ == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid provider"})
		return
	}

	q := h.db.WithContext(c.Request.Context()).Model(&models.ProviderAPIKey{})
	if providerQ != "" {
		q = q.Where("provider = ?", providerQ)
	}
	if keywordQ != "" {
		pattern := dbutil.NormalizeLikePattern(h.db, "%"+keywordQ+"%")
		q = q.Where(
			dbutil.CaseInsensitiveLikeExpr(h.db, "name")+" OR "+dbutil.CaseInsensitiveLikeExpr(h.db, "api_key"),
			pattern,
			pattern,
		)
	}

	var rows []models.ProviderAPIKey
	if errFind := q.Order("created_at DESC").Find(&rows).Error; errFind != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list api keys failed"})
		return
	}

	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, formatProviderRow(&rows[i]))
	}
	c.JSON(http.StatusOK, gin.H{"api_keys": out})
}

// Update applies validated updates to a provider API key record.
func (h *ProviderAPIKeyHandler) Update(c *gin.Context) {
	id, errID := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errID != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var row models.ProviderAPIKey
	if errFind := h.db.WithContext(c.Request.Context()).First(&row, "id = ?", id).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "fetch api key failed"})
		return
	}

	var body updateProviderAPIKeyRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	if body.Provider != nil {
		normalized := normalizeProvider(*body.Provider)
		if normalized == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid provider"})
			return
		}
		row.Provider = normalized
	}
	if body.Name != nil {
		row.Name = strings.TrimSpace(*body.Name)
	}
	if body.APIKey != nil {
		row.APIKey = strings.TrimSpace(*body.APIKey)
	}
	if body.Prefix != nil {
		row.Prefix = strings.TrimSpace(*body.Prefix)
	}
	if body.BaseURL != nil {
		row.BaseURL = strings.TrimSpace(*body.BaseURL)
	}
	if body.ProxyURL != nil {
		row.ProxyURL = strings.TrimSpace(*body.ProxyURL)
	}
	if body.Headers != nil {
		headersJSON, errHeaders := marshalJSON(*body.Headers)
		if errHeaders != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid headers"})
			return
		}
		row.Headers = headersJSON
	}
	if body.Models != nil {
		modelsJSON, errModels := marshalJSON(*body.Models)
		if errModels != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid models"})
			return
		}
		row.Models = modelsJSON
	}
	if body.ExcludedModels != nil {
		excludedJSON, errExcluded := marshalJSON(*body.ExcludedModels)
		if errExcluded != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid excluded_models"})
			return
		}
		row.ExcludedModels = excludedJSON
	}
	if body.APIKeyEntries != nil {
		apiKeyEntriesJSON, errEntries := marshalJSON(*body.APIKeyEntries)
		if errEntries != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api_key_entries"})
			return
		}
		row.APIKeyEntries = apiKeyEntriesJSON
	}

	normalizeProviderFields(&row)
	if errValidate := validateProviderRow(&row); errValidate != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errValidate.Error()})
		return
	}

	row.UpdatedAt = time.Now().UTC()
	if errSave := h.db.WithContext(c.Request.Context()).Save(&row).Error; errSave != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update api key failed"})
		return
	}

	if errSync := h.syncSDKConfig(c.Request.Context()); errSync != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "sync config failed"})
		return
	}

	c.JSON(http.StatusOK, formatProviderRow(&row))
}

// Delete removes a provider API key record and syncs config.
func (h *ProviderAPIKeyHandler) Delete(c *gin.Context) {
	id, errID := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errID != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if errDelete := h.db.WithContext(c.Request.Context()).Delete(&models.ProviderAPIKey{}, "id = ?", id).Error; errDelete != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "delete api key failed"})
		return
	}

	if errSync := h.syncSDKConfig(c.Request.Context()); errSync != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "sync config failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// syncSDKConfig rebuilds SDK config based on DB records and saves it.
func (h *ProviderAPIKeyHandler) syncSDKConfig(ctx context.Context) error {
	if h == nil || h.db == nil {
		return errors.New("missing db")
	}
	configPath := strings.TrimSpace(h.configPath)
	if configPath == "" {
		return nil
	}
	if _, errStat := os.Stat(configPath); errStat != nil {
		if os.IsNotExist(errStat) {
			return nil
		}
		return errStat
	}

	var rows []models.ProviderAPIKey
	if errFind := h.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; errFind != nil {
		return errFind
	}

	var mappingRows []models.ModelMapping
	if errFindMappings := h.db.WithContext(ctx).
		Model(&models.ModelMapping{}).
		Where("is_enabled = ?", true).
		Order("provider ASC, new_model_name ASC, model_name ASC").
		Find(&mappingRows).Error; errFindMappings != nil {
		return errFindMappings
	}

	cfg, errLoad := sdkconfig.LoadConfig(configPath)
	if errLoad != nil {
		return errLoad
	}

	geminiKeys := make([]sdkconfig.GeminiKey, 0)
	codexKeys := make([]sdkconfig.CodexKey, 0)
	claudeKeys := make([]sdkconfig.ClaudeKey, 0)
	openAIProviders := make([]sdkconfig.OpenAICompatibility, 0)

	for i := range rows {
		row := &rows[i]
		switch normalizeProvider(row.Provider) {
		case providerGemini:
			entry := sdkconfig.GeminiKey{
				APIKey:   strings.TrimSpace(row.APIKey),
				Prefix:   strings.TrimSpace(row.Prefix),
				BaseURL:  strings.TrimSpace(row.BaseURL),
				ProxyURL: strings.TrimSpace(row.ProxyURL),
				Headers:  decodeHeaders(row.Headers),
			}
			applyJSON(row.Models, &entry.Models)
			entry.ExcludedModels = decodeExcludedModels(row.ExcludedModels)
			if entry.APIKey != "" {
				geminiKeys = append(geminiKeys, entry)
			}
		case providerCodex:
			entry := sdkconfig.CodexKey{
				APIKey:   strings.TrimSpace(row.APIKey),
				Prefix:   strings.TrimSpace(row.Prefix),
				BaseURL:  strings.TrimSpace(row.BaseURL),
				ProxyURL: strings.TrimSpace(row.ProxyURL),
				Headers:  decodeHeaders(row.Headers),
			}
			applyJSON(row.Models, &entry.Models)
			entry.ExcludedModels = decodeExcludedModels(row.ExcludedModels)
			if entry.APIKey != "" {
				codexKeys = append(codexKeys, entry)
			}
		case providerClaude:
			entry := sdkconfig.ClaudeKey{
				APIKey:   strings.TrimSpace(row.APIKey),
				Prefix:   strings.TrimSpace(row.Prefix),
				BaseURL:  strings.TrimSpace(row.BaseURL),
				ProxyURL: strings.TrimSpace(row.ProxyURL),
				Headers:  decodeHeaders(row.Headers),
			}
			applyJSON(row.Models, &entry.Models)
			entry.ExcludedModels = decodeExcludedModels(row.ExcludedModels)
			if entry.APIKey != "" {
				claudeKeys = append(claudeKeys, entry)
			}
		case providerOpenAI:
			entry := sdkconfig.OpenAICompatibility{
				Name:          strings.TrimSpace(row.Name),
				Prefix:        strings.TrimSpace(row.Prefix),
				BaseURL:       strings.TrimSpace(row.BaseURL),
				APIKeyEntries: toOpenAIKeyEntries(decodeAPIKeyEntries(row.APIKeyEntries)),
				Models:        nil,
				Headers:       decodeHeaders(row.Headers),
			}
			applyJSON(row.Models, &entry.Models)
			if entry.BaseURL != "" && entry.Name != "" {
				openAIProviders = append(openAIProviders, entry)
			}
		}
	}

	cfg.GeminiKey = geminiKeys
	cfg.CodexKey = codexKeys
	cfg.ClaudeKey = claudeKeys
	cfg.OpenAICompatibility = openAIProviders
	cfg.OAuthModelMappings = buildOAuthModelMappings(mappingRows)

	cfg.SanitizeGeminiKeys()
	cfg.SanitizeCodexKeys()
	cfg.SanitizeClaudeKeys()
	cfg.SanitizeOpenAICompatibility()

	return sdkconfig.SaveConfigPreserveComments(configPath, cfg)
}

// buildOAuthModelMappings converts model mappings into SDK config entries.
func buildOAuthModelMappings(rows []models.ModelMapping) map[string][]sdkconfig.ModelNameMapping {
	if len(rows) == 0 {
		return nil
	}

	out := make(map[string][]sdkconfig.ModelNameMapping)
	seen := make(map[string]struct{})

	for i := range rows {
		row := &rows[i]
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
		out[provider] = append(out[provider], sdkconfig.ModelNameMapping{
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

// normalizeProvider normalizes provider input into canonical identifiers.
func normalizeProvider(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return ""
	}
	if alias, ok := providerAliases[trimmed]; ok {
		return alias
	}
	return ""
}

// normalizeProviderFields clears provider-specific fields to avoid conflicts.
func normalizeProviderFields(row *models.ProviderAPIKey) {
	if row == nil {
		return
	}
	switch normalizeProvider(row.Provider) {
	case providerOpenAI:
		row.APIKey = ""
		row.ProxyURL = ""
		row.ExcludedModels = nil
	default:
		row.Name = ""
		row.APIKeyEntries = nil
	}
}

// validateProviderRow enforces required fields per provider type.
func validateProviderRow(row *models.ProviderAPIKey) error {
	if row == nil {
		return errors.New("invalid api key")
	}
	switch normalizeProvider(row.Provider) {
	case providerGemini:
		if strings.TrimSpace(row.APIKey) == "" {
			return errors.New("api_key is required")
		}
	case providerCodex:
		if strings.TrimSpace(row.APIKey) == "" {
			return errors.New("api_key is required")
		}
		if strings.TrimSpace(row.BaseURL) == "" {
			return errors.New("base_url is required")
		}
	case providerClaude:
		if strings.TrimSpace(row.APIKey) == "" {
			return errors.New("api_key is required")
		}
	case providerOpenAI:
		if strings.TrimSpace(row.Name) == "" {
			return errors.New("name is required")
		}
		if strings.TrimSpace(row.BaseURL) == "" {
			return errors.New("base_url is required")
		}
		entries := decodeAPIKeyEntries(row.APIKeyEntries)
		if len(entries) == 0 {
			return errors.New("api_key_entries is required")
		}
	default:
		return errors.New("invalid provider")
	}
	return nil
}

// derefString returns the value or an empty string when nil.
func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// marshalJSON encodes a value into JSON, returning nil for empty inputs.
func marshalJSON(value interface{}) (datatypes.JSON, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if string(data) == "null" {
		return nil, nil
	}
	return datatypes.JSON(data), nil
}

// decodeHeaders decodes headers JSON into a map.
func decodeHeaders(value datatypes.JSON) map[string]string {
	if len(value) == 0 {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal(value, &out); err != nil {
		return nil
	}
	return out
}

// decodeModels decodes model aliases from JSON.
func decodeModels(value datatypes.JSON) []modelAlias {
	if len(value) == 0 {
		return nil
	}
	var out []modelAlias
	if err := json.Unmarshal(value, &out); err != nil {
		return nil
	}
	return out
}

// decodeExcludedModels decodes excluded model names from JSON.
func decodeExcludedModels(value datatypes.JSON) []string {
	if len(value) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(value, &out); err != nil {
		return nil
	}
	return out
}

// decodeAPIKeyEntries decodes API key entries from JSON.
func decodeAPIKeyEntries(value datatypes.JSON) []apiKeyEntry {
	if len(value) == 0 {
		return nil
	}
	var out []apiKeyEntry
	if err := json.Unmarshal(value, &out); err != nil {
		return nil
	}
	return out
}

// applyJSON decodes JSON into the provided target when possible.
func applyJSON(value datatypes.JSON, target interface{}) {
	if len(value) == 0 || target == nil {
		return
	}
	_ = json.Unmarshal(value, target)
}

// toOpenAIKeyEntries maps API key entries into SDK config structs.
func toOpenAIKeyEntries(entries []apiKeyEntry) []sdkconfig.OpenAICompatibilityAPIKey {
	if len(entries) == 0 {
		return nil
	}
	out := make([]sdkconfig.OpenAICompatibilityAPIKey, 0, len(entries))
	for _, item := range entries {
		out = append(out, sdkconfig.OpenAICompatibilityAPIKey{
			APIKey:   item.APIKey,
			ProxyURL: item.ProxyURL,
		})
	}
	return out
}

// formatProviderRow converts a provider API key record into response JSON.
func formatProviderRow(row *models.ProviderAPIKey) gin.H {
	if row == nil {
		return gin.H{}
	}
	return gin.H{
		"id":              row.ID,
		"provider":        row.Provider,
		"name":            row.Name,
		"api_key":         row.APIKey,
		"prefix":          row.Prefix,
		"base_url":        row.BaseURL,
		"proxy_url":       row.ProxyURL,
		"headers":         decodeHeaders(row.Headers),
		"models":          decodeModels(row.Models),
		"excluded_models": decodeExcludedModels(row.ExcludedModels),
		"api_key_entries": decodeAPIKeyEntries(row.APIKeyEntries),
		"created_at":      row.CreatedAt,
		"updated_at":      row.UpdatedAt,
	}
}
