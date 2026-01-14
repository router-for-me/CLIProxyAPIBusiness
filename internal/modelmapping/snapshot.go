package modelmapping

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
)

type selectorEntry struct {
	id        uint64
	selector  int
	rateLimit int
}

type modelAliasEntry struct {
	id    uint64
	alias string
}

type snapshot struct {
	updatedAt       time.Time
	byProviderNew   map[string]selectorEntry
	byProviderModel map[string]selectorEntry
	byProviderAlias map[string]modelAliasEntry
}

var globalSnapshot atomic.Value

func init() {
	globalSnapshot.Store(snapshot{
		byProviderNew:   make(map[string]selectorEntry),
		byProviderModel: make(map[string]selectorEntry),
		byProviderAlias: make(map[string]modelAliasEntry),
	})
}

// StoreModelMappings replaces the in-memory snapshot for model mapping selectors.
func StoreModelMappings(updatedAt time.Time, rows []models.ModelMapping) {
	nextNew := make(map[string]selectorEntry)
	nextModel := make(map[string]selectorEntry)
	nextAlias := make(map[string]modelAliasEntry)

	for _, row := range rows {
		if !row.IsEnabled {
			continue
		}
		provider := strings.TrimSpace(row.Provider)
		if provider == "" {
			continue
		}
		if alias := strings.TrimSpace(row.NewModelName); alias != "" {
			key := makeKey(provider, alias)
			if prev, ok := nextNew[key]; !ok || row.ID > prev.id {
				nextNew[key] = selectorEntry{id: row.ID, selector: row.Selector, rateLimit: row.RateLimit}
			}
		}
		if name := strings.TrimSpace(row.ModelName); name != "" {
			key := makeKey(provider, name)
			if prev, ok := nextModel[key]; !ok || row.ID > prev.id {
				nextModel[key] = selectorEntry{id: row.ID, selector: row.Selector, rateLimit: row.RateLimit}
			}
		}

		if name := strings.TrimSpace(row.ModelName); name != "" {
			if alias := strings.TrimSpace(row.NewModelName); alias != "" {
				key := makeLowerKey(provider, name)
				if prev, ok := nextAlias[key]; !ok || row.ID > prev.id {
					nextAlias[key] = modelAliasEntry{id: row.ID, alias: alias}
				}
			}
		}
	}

	globalSnapshot.Store(snapshot{
		updatedAt:       updatedAt.UTC(),
		byProviderNew:   nextNew,
		byProviderModel: nextModel,
		byProviderAlias: nextAlias,
	})
}

// LookupSelector returns the selector entry for provider + model using mapped name first.
func LookupSelector(provider, model string) (uint64, int, bool) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return 0, 0, false
	}
	snap := loadSnapshot()
	if entry, ok := snap.byProviderNew[makeKey(provider, model)]; ok {
		return entry.id, entry.selector, true
	}
	if entry, ok := snap.byProviderModel[makeKey(provider, model)]; ok {
		return entry.id, entry.selector, true
	}
	return 0, 0, false
}

// LookupRateLimit returns the rate limit for provider + model using mapped name first.
func LookupRateLimit(provider, model string) (uint64, int, bool) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return 0, 0, false
	}
	snap := loadSnapshot()
	if entry, ok := snap.byProviderNew[makeKey(provider, model)]; ok {
		return entry.id, entry.rateLimit, true
	}
	if entry, ok := snap.byProviderModel[makeKey(provider, model)]; ok {
		return entry.id, entry.rateLimit, true
	}
	return 0, 0, false
}

// LookupMappedModelName returns the client-visible model name (alias) for a provider + original model name.
func LookupMappedModelName(provider, model string) (string, bool) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return "", false
	}
	snap := loadSnapshot()
	entry, ok := snap.byProviderAlias[makeLowerKey(provider, model)]
	if !ok {
		return "", false
	}
	alias := strings.TrimSpace(entry.alias)
	if alias == "" {
		return "", false
	}
	return alias, true
}

func loadSnapshot() snapshot {
	v := globalSnapshot.Load()
	snap, ok := v.(snapshot)
	if !ok {
		return snapshot{
			byProviderNew:   make(map[string]selectorEntry),
			byProviderModel: make(map[string]selectorEntry),
			byProviderAlias: make(map[string]modelAliasEntry),
		}
	}
	if snap.byProviderNew == nil {
		snap.byProviderNew = make(map[string]selectorEntry)
	}
	if snap.byProviderModel == nil {
		snap.byProviderModel = make(map[string]selectorEntry)
	}
	if snap.byProviderAlias == nil {
		snap.byProviderAlias = make(map[string]modelAliasEntry)
	}
	return snap
}

func makeKey(provider, model string) string {
	return provider + "\x00" + model
}

func makeLowerKey(provider, model string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00" + strings.ToLower(strings.TrimSpace(model))
}
