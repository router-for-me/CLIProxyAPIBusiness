package watcher

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

// stableIDGenerator produces deterministic short IDs with a per-key counter.
type stableIDGenerator struct {
	counters map[string]int
}

// newStableIDGenerator initializes a generator with empty counters.
func newStableIDGenerator() *stableIDGenerator {
	return &stableIDGenerator{counters: make(map[string]int)}
}

// Next hashes the input parts into a stable short token and returns full and short IDs.
func (g *stableIDGenerator) Next(kind string, parts ...string) (string, string) {
	if g == nil {
		return kind + ":000000000000", "000000000000"
	}
	hasher := sha256.New()
	hasher.Write([]byte(kind))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		hasher.Write([]byte{0})
		hasher.Write([]byte(trimmed))
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if len(digest) < 12 {
		digest = fmt.Sprintf("%012s", digest)
	}
	short := digest[:12]
	key := kind + ":" + short
	index := g.counters[key]
	g.counters[key] = index + 1
	if index > 0 {
		short = fmt.Sprintf("%s-%d", short, index)
	}
	return fmt.Sprintf("%s:%s", kind, short), short
}

// synthesizeConfigAuths builds auth records from the in-memory config snapshot.
func synthesizeConfigAuths(cfg *sdkconfig.Config) []*coreauth.Auth {
	if cfg == nil {
		return nil
	}
	now := time.Now().UTC()
	idGen := newStableIDGenerator()
	out := make([]*coreauth.Auth, 0, 32)
	out = append(out, synthesizeGeminiKeys(cfg, now, idGen)...)
	out = append(out, synthesizeClaudeKeys(cfg, now, idGen)...)
	out = append(out, synthesizeCodexKeys(cfg, now, idGen)...)
	out = append(out, synthesizeOpenAICompat(cfg, now, idGen)...)
	out = append(out, synthesizeVertexCompat(cfg, now, idGen)...)
	return out
}

// synthesizeGeminiKeys converts Gemini config entries into auth records.
func synthesizeGeminiKeys(cfg *sdkconfig.Config, now time.Time, idGen *stableIDGenerator) []*coreauth.Auth {
	out := make([]*coreauth.Auth, 0, len(cfg.GeminiKey))
	for i := range cfg.GeminiKey {
		entry := cfg.GeminiKey[i]
		key := strings.TrimSpace(entry.APIKey)
		if key == "" {
			continue
		}
		prefix := strings.TrimSpace(entry.Prefix)
		base := strings.TrimSpace(entry.BaseURL)
		proxyURL := strings.TrimSpace(entry.ProxyURL)
		id, token := idGen.Next("gemini:apikey", key, base)
		attrs := map[string]string{
			"source":    fmt.Sprintf("config:gemini[%s]", token),
			"api_key":   key,
			"auth_kind": "apikey",
		}
		if entry.Priority != 0 {
			attrs["priority"] = strconv.Itoa(entry.Priority)
		}
		if base != "" {
			attrs["base_url"] = base
		}
		if hash := hashModelEntries(entry.Models); hash != "" {
			attrs["models_hash"] = hash
		}
		addConfigHeadersToAttrs(entry.Headers, attrs)
		a := &coreauth.Auth{
			ID:         id,
			Provider:   "gemini",
			Label:      "gemini-apikey",
			Prefix:     prefix,
			Status:     coreauth.StatusActive,
			ProxyURL:   proxyURL,
			Attributes: attrs,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		out = append(out, a)
	}
	return out
}

// synthesizeClaudeKeys converts Claude config entries into auth records.
func synthesizeClaudeKeys(cfg *sdkconfig.Config, now time.Time, idGen *stableIDGenerator) []*coreauth.Auth {
	out := make([]*coreauth.Auth, 0, len(cfg.ClaudeKey))
	for i := range cfg.ClaudeKey {
		entry := cfg.ClaudeKey[i]
		key := strings.TrimSpace(entry.APIKey)
		if key == "" {
			continue
		}
		prefix := strings.TrimSpace(entry.Prefix)
		base := strings.TrimSpace(entry.BaseURL)
		proxyURL := strings.TrimSpace(entry.ProxyURL)
		id, token := idGen.Next("claude:apikey", key, base)
		attrs := map[string]string{
			"source":    fmt.Sprintf("config:claude[%s]", token),
			"api_key":   key,
			"auth_kind": "apikey",
		}
		if entry.Priority != 0 {
			attrs["priority"] = strconv.Itoa(entry.Priority)
		}
		if base != "" {
			attrs["base_url"] = base
		}
		if hash := hashModelEntries(entry.Models); hash != "" {
			attrs["models_hash"] = hash
		}
		addConfigHeadersToAttrs(entry.Headers, attrs)
		a := &coreauth.Auth{
			ID:         id,
			Provider:   "claude",
			Label:      "claude-apikey",
			Prefix:     prefix,
			Status:     coreauth.StatusActive,
			ProxyURL:   proxyURL,
			Attributes: attrs,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		out = append(out, a)
	}
	return out
}

// synthesizeCodexKeys converts Codex config entries into auth records.
func synthesizeCodexKeys(cfg *sdkconfig.Config, now time.Time, idGen *stableIDGenerator) []*coreauth.Auth {
	out := make([]*coreauth.Auth, 0, len(cfg.CodexKey))
	for i := range cfg.CodexKey {
		entry := cfg.CodexKey[i]
		key := strings.TrimSpace(entry.APIKey)
		if key == "" {
			continue
		}
		prefix := strings.TrimSpace(entry.Prefix)
		base := strings.TrimSpace(entry.BaseURL)
		proxyURL := strings.TrimSpace(entry.ProxyURL)
		id, token := idGen.Next("codex:apikey", key, base)
		attrs := map[string]string{
			"source":    fmt.Sprintf("config:codex[%s]", token),
			"api_key":   key,
			"auth_kind": "apikey",
		}
		if entry.Priority != 0 {
			attrs["priority"] = strconv.Itoa(entry.Priority)
		}
		if base != "" {
			attrs["base_url"] = base
		}
		if hash := hashModelEntries(entry.Models); hash != "" {
			attrs["models_hash"] = hash
		}
		addConfigHeadersToAttrs(entry.Headers, attrs)
		a := &coreauth.Auth{
			ID:         id,
			Provider:   "codex",
			Label:      "codex-apikey",
			Prefix:     prefix,
			Status:     coreauth.StatusActive,
			ProxyURL:   proxyURL,
			Attributes: attrs,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		out = append(out, a)
	}
	return out
}

// synthesizeOpenAICompat converts OpenAI-compatible config entries into auth records.
func synthesizeOpenAICompat(cfg *sdkconfig.Config, now time.Time, idGen *stableIDGenerator) []*coreauth.Auth {
	out := make([]*coreauth.Auth, 0)
	for i := range cfg.OpenAICompatibility {
		compat := &cfg.OpenAICompatibility[i]
		prefix := strings.TrimSpace(compat.Prefix)
		providerName := strings.ToLower(strings.TrimSpace(compat.Name))
		if providerName == "" {
			providerName = "openai-compatibility"
		}
		base := strings.TrimSpace(compat.BaseURL)

		createdEntries := 0
		for j := range compat.APIKeyEntries {
			entry := &compat.APIKeyEntries[j]
			key := strings.TrimSpace(entry.APIKey)
			proxyURL := strings.TrimSpace(entry.ProxyURL)
			idKind := fmt.Sprintf("openai-compatibility:%s", providerName)
			id, token := idGen.Next(idKind, key, base, proxyURL)
			attrs := map[string]string{
				"source":       fmt.Sprintf("config:%s[%s]", providerName, token),
				"base_url":     base,
				"compat_name":  compat.Name,
				"provider_key": providerName,
				"auth_kind":    "apikey",
			}
			if compat.Priority != 0 {
				attrs["priority"] = strconv.Itoa(compat.Priority)
			}
			if key != "" {
				attrs["api_key"] = key
			}
			if hash := hashOpenAICompatModels(compat.Models); hash != "" {
				attrs["models_hash"] = hash
			}
			addConfigHeadersToAttrs(compat.Headers, attrs)
			a := &coreauth.Auth{
				ID:         id,
				Provider:   providerName,
				Label:      compat.Name,
				Prefix:     prefix,
				Status:     coreauth.StatusActive,
				ProxyURL:   proxyURL,
				Attributes: attrs,
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			out = append(out, a)
			createdEntries++
		}
		if createdEntries == 0 {
			idKind := fmt.Sprintf("openai-compatibility:%s", providerName)
			id, token := idGen.Next(idKind, base)
			attrs := map[string]string{
				"source":       fmt.Sprintf("config:%s[%s]", providerName, token),
				"base_url":     base,
				"compat_name":  compat.Name,
				"provider_key": providerName,
				"auth_kind":    "apikey",
			}
			if compat.Priority != 0 {
				attrs["priority"] = strconv.Itoa(compat.Priority)
			}
			if hash := hashOpenAICompatModels(compat.Models); hash != "" {
				attrs["models_hash"] = hash
			}
			addConfigHeadersToAttrs(compat.Headers, attrs)
			a := &coreauth.Auth{
				ID:         id,
				Provider:   providerName,
				Label:      compat.Name,
				Prefix:     prefix,
				Status:     coreauth.StatusActive,
				Attributes: attrs,
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			out = append(out, a)
		}
	}
	return out
}

// synthesizeVertexCompat converts Vertex-compatible config entries into auth records.
func synthesizeVertexCompat(cfg *sdkconfig.Config, now time.Time, idGen *stableIDGenerator) []*coreauth.Auth {
	out := make([]*coreauth.Auth, 0, len(cfg.VertexCompatAPIKey))
	for i := range cfg.VertexCompatAPIKey {
		entry := &cfg.VertexCompatAPIKey[i]
		providerName := "vertex"
		base := strings.TrimSpace(entry.BaseURL)

		key := strings.TrimSpace(entry.APIKey)
		prefix := strings.TrimSpace(entry.Prefix)
		proxyURL := strings.TrimSpace(entry.ProxyURL)
		id, token := idGen.Next("vertex:apikey", key, base, proxyURL)
		attrs := map[string]string{
			"source":       fmt.Sprintf("config:vertex-apikey[%s]", token),
			"base_url":     base,
			"provider_key": providerName,
			"auth_kind":    "apikey",
		}
		if entry.Priority != 0 {
			attrs["priority"] = strconv.Itoa(entry.Priority)
		}
		if key != "" {
			attrs["api_key"] = key
		}
		if hash := hashModelEntries(entry.Models); hash != "" {
			attrs["models_hash"] = hash
		}
		addConfigHeadersToAttrs(entry.Headers, attrs)
		a := &coreauth.Auth{
			ID:         id,
			Provider:   providerName,
			Label:      "vertex-apikey",
			Prefix:     prefix,
			Status:     coreauth.StatusActive,
			ProxyURL:   proxyURL,
			Attributes: attrs,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		out = append(out, a)
	}
	return out
}

// addConfigHeadersToAttrs adds header key-values to the auth attribute map.
func addConfigHeadersToAttrs(headers map[string]string, attrs map[string]string) {
	if len(headers) == 0 || attrs == nil {
		return
	}
	for hk, hv := range headers {
		key := strings.TrimSpace(hk)
		val := strings.TrimSpace(hv)
		if key == "" || val == "" {
			continue
		}
		attrs["header:"+key] = val
	}
}

// hashAuth computes a stable hash for auth records for change detection.
func hashAuth(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	parts := []string{
		"id=" + strings.TrimSpace(auth.ID),
		"provider=" + strings.TrimSpace(auth.Provider),
		"label=" + strings.TrimSpace(auth.Label),
		"prefix=" + strings.TrimSpace(auth.Prefix),
		"proxy=" + strings.TrimSpace(auth.ProxyURL),
	}
	if auth.Attributes != nil {
		keys := make([]string, 0, len(auth.Attributes))
		for k := range auth.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, "attr:"+k+"="+auth.Attributes[k])
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

// modelEntry describes model mapping entries with name/alias accessors.
type modelEntry interface {
	GetName() string
	GetAlias() string
}

// hashModelEntries generates a stable hash of model name/alias pairs.
func hashModelEntries[T modelEntry](models []T) string {
	if len(models) == 0 {
		return ""
	}
	items := make([]string, 0, len(models))
	for _, m := range models {
		name := strings.ToLower(strings.TrimSpace(m.GetName()))
		alias := strings.ToLower(strings.TrimSpace(m.GetAlias()))
		if name == "" && alias == "" {
			continue
		}
		items = append(items, name+"::"+alias)
	}
	if len(items) == 0 {
		return ""
	}
	sort.Strings(items)
	sum := sha256.Sum256([]byte(strings.Join(items, "|")))
	return hex.EncodeToString(sum[:])
}

// hashOpenAICompatModels generates a stable hash for OpenAI-compat model entries.
func hashOpenAICompatModels(models []sdkconfig.OpenAICompatibilityModel) string {
	if len(models) == 0 {
		return ""
	}
	items := make([]string, 0, len(models))
	for _, m := range models {
		name := strings.ToLower(strings.TrimSpace(m.Name))
		alias := strings.ToLower(strings.TrimSpace(m.Alias))
		if name == "" && alias == "" {
			continue
		}
		items = append(items, name+"::"+alias)
	}
	if len(items) == 0 {
		return ""
	}
	sort.Strings(items)
	sum := sha256.Sum256([]byte(strings.Join(items, "|")))
	return hex.EncodeToString(sum[:])
}
