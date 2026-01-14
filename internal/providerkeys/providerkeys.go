package providerkeys

import (
	"encoding/json"
	"strings"

	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"gorm.io/datatypes"
)

// Canonical provider identifiers for SDK config generation.
const (
	providerGemini = "gemini"
	providerCodex  = "codex"
	providerClaude = "claude"
	providerOpenAI = "openai-compatibility"
)

var providerAliases = map[string]string{
	"gemini":               providerGemini,
	"codex":                providerCodex,
	"claude":               providerClaude,
	"claude-code":          providerClaude,
	"openai":               providerOpenAI,
	"openai-compatibility": providerOpenAI,
}

type apiKeyEntry struct {
	APIKey   string `json:"api_key"`
	ProxyURL string `json:"proxy_url"`
}

// ApplyToConfig refreshes provider API key and OAuth model mapping sections on the given SDK config.
func ApplyToConfig(cfg *sdkconfig.Config, providerRows []models.ProviderAPIKey, mappingRows []models.ModelMapping) {
	if cfg == nil {
		return
	}

	geminiKeys := make([]sdkconfig.GeminiKey, 0)
	codexKeys := make([]sdkconfig.CodexKey, 0)
	claudeKeys := make([]sdkconfig.ClaudeKey, 0)
	openAIProviders := make([]sdkconfig.OpenAICompatibility, 0)

	for i := range providerRows {
		row := &providerRows[i]
		switch normalizeProvider(row.Provider) {
		case providerGemini:
			entry := sdkconfig.GeminiKey{
				APIKey:   strings.TrimSpace(row.APIKey),
				Priority: row.Priority,
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
				Priority: row.Priority,
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
				Priority: row.Priority,
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
				Priority:      row.Priority,
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
}

func normalizeProvider(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return ""
	}
	if alias, ok := providerAliases[trimmed]; ok {
		return alias
	}
	return trimmed
}

func decodeHeaders(value datatypes.JSON) map[string]string {
	if len(value) == 0 {
		return nil
	}
	out := make(map[string]string)
	if err := json.Unmarshal(value, &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

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

func toOpenAIKeyEntries(entries []apiKeyEntry) []sdkconfig.OpenAICompatibilityAPIKey {
	if len(entries) == 0 {
		return nil
	}
	out := make([]sdkconfig.OpenAICompatibilityAPIKey, 0, len(entries))
	for _, item := range entries {
		out = append(out, sdkconfig.OpenAICompatibilityAPIKey{
			APIKey:   strings.TrimSpace(item.APIKey),
			ProxyURL: strings.TrimSpace(item.ProxyURL),
		})
	}
	return out
}

func applyJSON(value datatypes.JSON, target interface{}) {
	if len(value) == 0 || target == nil {
		return
	}
	_ = json.Unmarshal(value, target)
}

func buildOAuthModelMappings(rows []models.ModelMapping) map[string][]sdkconfig.ModelNameMapping {
	if len(rows) == 0 {
		return nil
	}

	out := make(map[string][]sdkconfig.ModelNameMapping)
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
