package providerkeys

import (
	"encoding/json"
	"testing"

	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"gorm.io/datatypes"
)

func TestApplyToConfig_Gemini(t *testing.T) {
	headers, _ := json.Marshal(map[string]string{"x-test": "1"})
	modelsJSON, _ := json.Marshal([]map[string]string{{"name": "gemini-1.5", "alias": "g"}})

	rows := []models.ProviderAPIKey{
		{
			Provider: "gemini",
			APIKey:   "key",
			Prefix:   "teamA",
			BaseURL:  "https://example.com",
			ProxyURL: "socks5://proxy",
			Headers:  datatypes.JSON(headers),
			Models:   datatypes.JSON(modelsJSON),
		},
	}

	cfg := &sdkconfig.Config{}
	ApplyToConfig(cfg, rows, nil)

	if len(cfg.GeminiKey) != 1 {
		t.Fatalf("expected 1 gemini key, got %d", len(cfg.GeminiKey))
	}
	if cfg.GeminiKey[0].APIKey != "key" {
		t.Fatalf("expected api key=key, got %q", cfg.GeminiKey[0].APIKey)
	}
	if cfg.GeminiKey[0].Prefix != "teamA" {
		t.Fatalf("expected prefix=teamA, got %q", cfg.GeminiKey[0].Prefix)
	}
	if cfg.GeminiKey[0].Headers["x-test"] != "1" {
		t.Fatalf("expected header x-test=1, got %q", cfg.GeminiKey[0].Headers["x-test"])
	}
	if len(cfg.GeminiKey[0].Models) != 1 || cfg.GeminiKey[0].Models[0].Alias != "g" {
		t.Fatalf("expected 1 model alias 'g', got %+v", cfg.GeminiKey[0].Models)
	}
}

func TestApplyToConfig_OAuthModelMappings(t *testing.T) {
	rows := []models.ModelMapping{
		{Provider: "claude", ModelName: "claude-sonnet", NewModelName: "sonnet", Fork: true, IsEnabled: true},
	}

	cfg := &sdkconfig.Config{}
	ApplyToConfig(cfg, nil, rows)

	mappings := cfg.OAuthModelMappings["claude"]
	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mappings))
	}
	if mappings[0].Name != "claude-sonnet" || mappings[0].Alias != "sonnet" || !mappings[0].Fork {
		t.Fatalf("unexpected mapping: %+v", mappings[0])
	}
}
