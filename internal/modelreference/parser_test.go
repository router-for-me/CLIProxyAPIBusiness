package modelreference

import (
	"bytes"
	"testing"

	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
)

func findReference(refs []models.ModelReference, providerName, modelName string) *models.ModelReference {
	for i := range refs {
		if refs[i].ProviderName == providerName && refs[i].ModelName == modelName {
			return &refs[i]
		}
	}
	return nil
}

func TestParseModelsPayload_FallbacksAndExtras(t *testing.T) {
	payload := []byte(`{"provider-x":{"name":"Provider X","api":"https://api.example","models":{"model-a":{"name":"Model A","id":"model-a","cost":{"input":0.1,"output":0.2,"cache_read":0.01,"cache_write":0.02,"context_over_200k":{"input":1.5,"output":2.5,"cache_read":0.15,"cache_write":0.25}},"limit":{"context":8000,"output":2048},"family":"demo"},"model-b":{"cost":{"input":0.3},"limit":{"context":0}}}}}`)

	refs, err := ParseModelsPayload(payload)
	if err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}

	refA := findReference(refs, "Provider X", "Model A")
	if refA == nil {
		t.Fatalf("expected Model A reference")
	}
	if refA.ContextLimit != 8000 || refA.OutputLimit != 2048 {
		t.Fatalf("unexpected limits: context=%d output=%d", refA.ContextLimit, refA.OutputLimit)
	}
	if refA.InputPrice == nil || *refA.InputPrice != 0.1 {
		t.Fatalf("unexpected input price")
	}
	if refA.OutputPrice == nil || *refA.OutputPrice != 0.2 {
		t.Fatalf("unexpected output price")
	}
	if refA.CacheReadPrice == nil || *refA.CacheReadPrice != 0.01 {
		t.Fatalf("unexpected cache read price")
	}
	if refA.CacheWritePrice == nil || *refA.CacheWritePrice != 0.02 {
		t.Fatalf("unexpected cache write price")
	}
	if refA.ContextOver200kInputPrice == nil || *refA.ContextOver200kInputPrice != 1.5 {
		t.Fatalf("unexpected context over 200k input price")
	}
	if refA.ContextOver200kOutputPrice == nil || *refA.ContextOver200kOutputPrice != 2.5 {
		t.Fatalf("unexpected context over 200k output price")
	}
	if refA.ContextOver200kCacheReadPrice == nil || *refA.ContextOver200kCacheReadPrice != 0.15 {
		t.Fatalf("unexpected context over 200k cache read price")
	}
	if refA.ContextOver200kCacheWritePrice == nil || *refA.ContextOver200kCacheWritePrice != 0.25 {
		t.Fatalf("unexpected context over 200k cache write price")
	}
	if bytes.Contains(refA.Extra, []byte("model-a")) || bytes.Contains(refA.Extra, []byte("provider-x")) {
		t.Fatalf("expected provider/model ids to be excluded from extra")
	}

	refB := findReference(refs, "Provider X", "model-b")
	if refB == nil {
		t.Fatalf("expected fallback name for model-b")
	}
	if refB.InputPrice == nil || *refB.InputPrice != 0.3 {
		t.Fatalf("unexpected input price for model-b")
	}
}

func TestParseModelsPayload_MergesDuplicateDisplayNames(t *testing.T) {
	payload := []byte(`{"provider-x":{"name":"Provider X","models":{"model-a":{"name":"Model A","cost":{"input":0.1}},"model-b":{"name":"Model A","cost":{"output":0.2},"limit":{"context":4096}}}}}`)

	refs, err := ParseModelsPayload(payload)
	if err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}

	ref := refs[0]
	if ref.ProviderName != "Provider X" || ref.ModelName != "Model A" {
		t.Fatalf("unexpected reference: %s/%s", ref.ProviderName, ref.ModelName)
	}
	if ref.InputPrice == nil || *ref.InputPrice != 0.1 {
		t.Fatalf("unexpected input price")
	}
	if ref.OutputPrice == nil || *ref.OutputPrice != 0.2 {
		t.Fatalf("unexpected output price")
	}
	if ref.ContextLimit != 4096 {
		t.Fatalf("unexpected context limit")
	}
}
