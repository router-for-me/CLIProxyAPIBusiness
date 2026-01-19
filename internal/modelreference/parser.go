package modelreference

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"gorm.io/datatypes"
)

type providerPayload struct {
	Name   string                     `json:"name"`
	Models map[string]json.RawMessage `json:"models"`
}

type modelPayload struct {
	Name  string      `json:"name"`
	Cost  *modelCost  `json:"cost"`
	Limit *modelLimit `json:"limit"`
}

type modelCost struct {
	Input           *float64             `json:"input"`
	Output          *float64             `json:"output"`
	CacheRead       *float64             `json:"cache_read"`
	CacheWrite      *float64             `json:"cache_write"`
	ContextOver200k *contextOver200kCost `json:"context_over_200k"`
}

type modelLimit struct {
	Context *int `json:"context"`
	Output  *int `json:"output"`
}

type contextOver200kCost struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

func (c *contextOver200kCost) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}

	var numeric float64
	if err := json.Unmarshal(data, &numeric); err == nil {
		c.Input = &numeric
		return nil
	}

	type raw contextOver200kCost
	var decoded raw
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = contextOver200kCost(decoded)
	return nil
}

type modelKey struct {
	provider string
	model    string
}

// ParseModelsPayload converts the models.dev payload into model references.
func ParseModelsPayload(data []byte) ([]models.ModelReference, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("parse models payload: empty payload")
	}

	var providers map[string]json.RawMessage
	if err := json.Unmarshal(data, &providers); err != nil {
		return nil, fmt.Errorf("parse models payload: decode providers: %w", err)
	}

	refsByKey := make(map[modelKey]models.ModelReference)
	providerIDs := make([]string, 0, len(providers))
	for providerID := range providers {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)

	for _, providerID := range providerIDs {
		providerRaw := providers[providerID]
		if len(providerRaw) == 0 {
			continue
		}

		var provider providerPayload
		if err := json.Unmarshal(providerRaw, &provider); err != nil {
			return nil, fmt.Errorf("parse models payload: decode provider %s: %w", providerID, err)
		}
		if len(provider.Models) == 0 {
			continue
		}

		providerName := strings.TrimSpace(provider.Name)
		if providerName == "" {
			providerName = strings.TrimSpace(providerID)
		}
		if providerName == "" {
			continue
		}

		providerExtra, err := buildProviderExtra(providerRaw)
		if err != nil {
			return nil, err
		}

		modelIDs := make([]string, 0, len(provider.Models))
		for modelID := range provider.Models {
			modelIDs = append(modelIDs, modelID)
		}
		sort.Strings(modelIDs)

		for _, modelID := range modelIDs {
			modelRaw := provider.Models[modelID]
			if len(modelRaw) == 0 {
				continue
			}

			var model modelPayload
			if err := json.Unmarshal(modelRaw, &model); err != nil {
				return nil, fmt.Errorf("parse models payload: decode model %s: %w", modelID, err)
			}

			modelName := strings.TrimSpace(model.Name)
			if modelName == "" {
				modelName = strings.TrimSpace(modelID)
			}
			if modelName == "" {
				continue
			}

			contextLimit := 0
			outputLimit := 0
			if model.Limit != nil {
				if model.Limit.Context != nil {
					contextLimit = *model.Limit.Context
				}
				if model.Limit.Output != nil {
					outputLimit = *model.Limit.Output
				}
			}

			modelExtra, err := buildModelExtra(modelRaw)
			if err != nil {
				return nil, err
			}
			extraPayload := make(map[string]any)
			if len(providerExtra) > 0 {
				extraPayload["provider"] = providerExtra
			}
			if len(modelExtra) > 0 {
				extraPayload["model"] = modelExtra
			}

			extra := datatypes.JSON([]byte("{}"))
			if len(extraPayload) > 0 {
				data, err := json.Marshal(extraPayload)
				if err != nil {
					return nil, fmt.Errorf("parse models payload: encode extra: %w", err)
				}
				extra = datatypes.JSON(data)
			}

			ref := models.ModelReference{
				ProviderName: providerName,
				ModelName:    modelName,
				ContextLimit: contextLimit,
				OutputLimit:  outputLimit,
				Extra:        extra,
			}
			if model.Cost != nil {
				ref.InputPrice = model.Cost.Input
				ref.OutputPrice = model.Cost.Output
				ref.CacheReadPrice = model.Cost.CacheRead
				ref.CacheWritePrice = model.Cost.CacheWrite
				if model.Cost.ContextOver200k != nil {
					ref.ContextOver200kInputPrice = model.Cost.ContextOver200k.Input
					ref.ContextOver200kOutputPrice = model.Cost.ContextOver200k.Output
					ref.ContextOver200kCacheReadPrice = model.Cost.ContextOver200k.CacheRead
					ref.ContextOver200kCacheWritePrice = model.Cost.ContextOver200k.CacheWrite
				}
			}

			key := modelKey{provider: providerName, model: modelName}
			if existing, ok := refsByKey[key]; ok {
				merged, err := mergeModelReference(existing, ref)
				if err != nil {
					return nil, err
				}
				refsByKey[key] = merged
			} else {
				refsByKey[key] = ref
			}
		}
	}

	if len(refsByKey) == 0 {
		return nil, nil
	}

	keys := make([]modelKey, 0, len(refsByKey))
	for key := range refsByKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].provider == keys[j].provider {
			return keys[i].model < keys[j].model
		}
		return keys[i].provider < keys[j].provider
	})

	refs := make([]models.ModelReference, 0, len(keys))
	for _, key := range keys {
		refs = append(refs, refsByKey[key])
	}

	return refs, nil
}

func buildProviderExtra(providerRaw json.RawMessage) (map[string]any, error) {
	var provider map[string]any
	if err := json.Unmarshal(providerRaw, &provider); err != nil {
		return nil, fmt.Errorf("parse models payload: decode provider extra: %w", err)
	}
	delete(provider, "models")
	delete(provider, "name")
	delete(provider, "id")
	if len(provider) == 0 {
		return nil, nil
	}
	return provider, nil
}

func buildModelExtra(modelRaw json.RawMessage) (map[string]any, error) {
	var model map[string]any
	if err := json.Unmarshal(modelRaw, &model); err != nil {
		return nil, fmt.Errorf("parse models payload: decode model extra: %w", err)
	}
	delete(model, "name")
	delete(model, "id")
	if len(model) == 0 {
		return nil, nil
	}
	return model, nil
}

func mergeModelReference(base, incoming models.ModelReference) (models.ModelReference, error) {
	if base.ContextLimit == 0 && incoming.ContextLimit != 0 {
		base.ContextLimit = incoming.ContextLimit
	}
	if base.OutputLimit == 0 && incoming.OutputLimit != 0 {
		base.OutputLimit = incoming.OutputLimit
	}
	if base.InputPrice == nil {
		base.InputPrice = incoming.InputPrice
	}
	if base.OutputPrice == nil {
		base.OutputPrice = incoming.OutputPrice
	}
	if base.CacheReadPrice == nil {
		base.CacheReadPrice = incoming.CacheReadPrice
	}
	if base.CacheWritePrice == nil {
		base.CacheWritePrice = incoming.CacheWritePrice
	}
	if base.ContextOver200kInputPrice == nil {
		base.ContextOver200kInputPrice = incoming.ContextOver200kInputPrice
	}
	if base.ContextOver200kOutputPrice == nil {
		base.ContextOver200kOutputPrice = incoming.ContextOver200kOutputPrice
	}
	if base.ContextOver200kCacheReadPrice == nil {
		base.ContextOver200kCacheReadPrice = incoming.ContextOver200kCacheReadPrice
	}
	if base.ContextOver200kCacheWritePrice == nil {
		base.ContextOver200kCacheWritePrice = incoming.ContextOver200kCacheWritePrice
	}

	mergedExtra, err := mergeExtraJSON(base.Extra, incoming.Extra)
	if err != nil {
		return models.ModelReference{}, err
	}
	base.Extra = mergedExtra

	return base, nil
}

func mergeExtraJSON(base, incoming datatypes.JSON) (datatypes.JSON, error) {
	baseMap := make(map[string]any)
	incomingMap := make(map[string]any)

	if len(base) > 0 {
		if err := json.Unmarshal(base, &baseMap); err != nil {
			return nil, fmt.Errorf("parse models payload: decode extra: %w", err)
		}
	}
	if len(incoming) > 0 {
		if err := json.Unmarshal(incoming, &incomingMap); err != nil {
			return nil, fmt.Errorf("parse models payload: decode extra: %w", err)
		}
	}
	if len(baseMap) == 0 && len(incomingMap) == 0 {
		return datatypes.JSON([]byte("{}")), nil
	}

	mergeMaps(baseMap, incomingMap)
	merged, err := json.Marshal(baseMap)
	if err != nil {
		return nil, fmt.Errorf("parse models payload: encode extra: %w", err)
	}
	return datatypes.JSON(merged), nil
}

func mergeMaps(dst map[string]any, src map[string]any) {
	for key, value := range src {
		existing, ok := dst[key]
		if !ok {
			dst[key] = value
			continue
		}
		existingMap, okExisting := existing.(map[string]any)
		incomingMap, okIncoming := value.(map[string]any)
		if okExisting && okIncoming {
			mergeMaps(existingMap, incomingMap)
		}
	}
}
