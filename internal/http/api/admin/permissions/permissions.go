package permissions

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Definition describes an admin permission.
type Definition struct {
	Key    string `json:"key"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Label  string `json:"label"`
	Module string `json:"module"`
}

// Key builds a permission key from method and path.
func Key(method, path string) string {
	return strings.ToUpper(method) + " " + path
}

// NormalizePermissions trims, de-duplicates, and sorts permissions.
func NormalizePermissions(perms []string) []string {
	if len(perms) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(perms))
	normalized := make([]string, 0, len(perms))
	for _, perm := range perms {
		trimmed := strings.TrimSpace(perm)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	sort.Strings(normalized)
	return normalized
}

// ValidatePermissions validates that all permissions exist in the definition set.
func ValidatePermissions(perms []string) error {
	if len(perms) == 0 {
		return nil
	}
	allowed := definitionMap
	for _, perm := range perms {
		trimmed := strings.TrimSpace(perm)
		if trimmed == "" {
			continue
		}
		if _, ok := allowed[trimmed]; !ok {
			return fmt.Errorf("invalid permission: %s", trimmed)
		}
	}
	return nil
}

// ParsePermissions parses and normalizes permissions from JSON.
func ParsePermissions(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var perms []string
	if err := json.Unmarshal(raw, &perms); err != nil {
		return []string{}
	}
	return NormalizePermissions(perms)
}

// MarshalPermissions serializes normalized permissions to JSON.
func MarshalPermissions(perms []string) ([]byte, error) {
	normalized := NormalizePermissions(perms)
	return json.Marshal(normalized)
}

// HasPermission checks whether the key exists in the permission list.
func HasPermission(perms []string, key string) bool {
	if key == "" {
		return false
	}
	for _, perm := range perms {
		if perm == key {
			return true
		}
	}
	return false
}

// Definitions returns a copy of all permission definitions.
func Definitions() []Definition {
	out := make([]Definition, len(definitions))
	copy(out, definitions)
	return out
}

// DefinitionMap returns a copy of the permission definition map.
func DefinitionMap() map[string]Definition {
	out := make(map[string]Definition, len(definitionMap))
	for key, value := range definitionMap {
		out[key] = value
	}
	return out
}

// newDefinition builds a Definition with a normalized key.
func newDefinition(method, path, label, module string) Definition {
	upperMethod := strings.ToUpper(method)
	return Definition{
		Key:    Key(upperMethod, path),
		Method: upperMethod,
		Path:   path,
		Label:  label,
		Module: module,
	}
}

// definitions is the ordered list of permission definitions.
var definitions = []Definition{
	newDefinition("GET", "/v0/admin/dashboard/kpi", "View KPI", "Dashboard"),
	newDefinition("GET", "/v0/admin/dashboard/traffic", "View Traffic", "Dashboard"),
	newDefinition("GET", "/v0/admin/dashboard/cost-distribution", "View Cost Distribution", "Dashboard"),
	newDefinition("GET", "/v0/admin/dashboard/model-health", "View Model Health", "Dashboard"),
	newDefinition("GET", "/v0/admin/dashboard/transactions", "View Recent Transactions", "Dashboard"),

	newDefinition("POST", "/v0/admin/users", "Create User", "Users"),
	newDefinition("GET", "/v0/admin/users", "List Users", "Users"),
	newDefinition("GET", "/v0/admin/users/:id", "Get User", "Users"),
	newDefinition("PUT", "/v0/admin/users/:id", "Update User", "Users"),
	newDefinition("DELETE", "/v0/admin/users/:id", "Delete User", "Users"),
	newDefinition("POST", "/v0/admin/users/:id/disable", "Disable User", "Users"),
	newDefinition("POST", "/v0/admin/users/:id/enable", "Enable User", "Users"),
	newDefinition("PUT", "/v0/admin/users/:id/password", "Change User Password", "Users"),

	newDefinition("POST", "/v0/admin/user-groups", "Create User Group", "User Groups"),
	newDefinition("GET", "/v0/admin/user-groups", "List User Groups", "User Groups"),
	newDefinition("GET", "/v0/admin/user-groups/:id", "Get User Group", "User Groups"),
	newDefinition("PUT", "/v0/admin/user-groups/:id", "Update User Group", "User Groups"),
	newDefinition("DELETE", "/v0/admin/user-groups/:id", "Delete User Group", "User Groups"),
	newDefinition("POST", "/v0/admin/user-groups/:id/default", "Set Default User Group", "User Groups"),

	newDefinition("POST", "/v0/admin/auth-groups", "Create Auth Group", "Auth Groups"),
	newDefinition("GET", "/v0/admin/auth-groups", "List Auth Groups", "Auth Groups"),
	newDefinition("GET", "/v0/admin/auth-groups/:id", "Get Auth Group", "Auth Groups"),
	newDefinition("PUT", "/v0/admin/auth-groups/:id", "Update Auth Group", "Auth Groups"),
	newDefinition("DELETE", "/v0/admin/auth-groups/:id", "Delete Auth Group", "Auth Groups"),
	newDefinition("POST", "/v0/admin/auth-groups/:id/default", "Set Default Auth Group", "Auth Groups"),

	newDefinition("POST", "/v0/admin/auth-files", "Create Auth File", "Auth Files"),
	newDefinition("POST", "/v0/admin/auth-files/import", "Import Auth Files", "Auth Files"),
	newDefinition("GET", "/v0/admin/auth-files", "List Auth Files", "Auth Files"),
	newDefinition("GET", "/v0/admin/auth-files/:id", "Get Auth File", "Auth Files"),
	newDefinition("PUT", "/v0/admin/auth-files/:id", "Update Auth File", "Auth Files"),
	newDefinition("DELETE", "/v0/admin/auth-files/:id", "Delete Auth File", "Auth Files"),
	newDefinition("POST", "/v0/admin/auth-files/:id/available", "Set Auth File Available", "Auth Files"),
	newDefinition("POST", "/v0/admin/auth-files/:id/unavailable", "Set Auth File Unavailable", "Auth Files"),
	newDefinition("GET", "/v0/admin/auth-files/types", "List Auth File Types", "Auth Files"),

	newDefinition("GET", "/v0/admin/quotas", "List Quotas", "Quota"),

	newDefinition("POST", "/v0/admin/model-mappings", "Create Model Mapping", "Models"),
	newDefinition("GET", "/v0/admin/model-mappings", "List Model Mappings", "Models"),
	newDefinition("GET", "/v0/admin/model-mappings/available-models", "List Available Models", "Models"),
	newDefinition("GET", "/v0/admin/model-references/price", "Get Model Reference Price", "Models"),
	newDefinition("GET", "/v0/admin/model-mappings/:id", "Get Model Mapping", "Models"),
	newDefinition("PUT", "/v0/admin/model-mappings/:id", "Update Model Mapping", "Models"),
	newDefinition("DELETE", "/v0/admin/model-mappings/:id", "Delete Model Mapping", "Models"),
	newDefinition("POST", "/v0/admin/model-mappings/:id/enable", "Enable Model Mapping", "Models"),
	newDefinition("POST", "/v0/admin/model-mappings/:id/disable", "Disable Model Mapping", "Models"),
	newDefinition("GET", "/v0/admin/model-mappings/:id/payload-rules", "List Model Payload Rules", "Models"),
	newDefinition("POST", "/v0/admin/model-mappings/:id/payload-rules", "Create Model Payload Rule", "Models"),
	newDefinition("PUT", "/v0/admin/model-mappings/:id/payload-rules/:rule_id", "Update Model Payload Rule", "Models"),
	newDefinition("DELETE", "/v0/admin/model-mappings/:id/payload-rules/:rule_id", "Delete Model Payload Rule", "Models"),

	newDefinition("POST", "/v0/admin/api-keys", "Create API Key", "API Keys"),
	newDefinition("GET", "/v0/admin/api-keys", "List API Keys", "API Keys"),
	newDefinition("DELETE", "/v0/admin/api-keys/:id", "Revoke API Key", "API Keys"),
	newDefinition("POST", "/v0/admin/users/:id/api-keys", "Create User API Key", "API Keys"),
	newDefinition("GET", "/v0/admin/users/:id/api-keys", "List User API Keys", "API Keys"),

	newDefinition("POST", "/v0/admin/provider-api-keys", "Create Provider API Key", "Provider API Keys"),
	newDefinition("GET", "/v0/admin/provider-api-keys", "List Provider API Keys", "Provider API Keys"),
	newDefinition("PUT", "/v0/admin/provider-api-keys/:id", "Update Provider API Key", "Provider API Keys"),
	newDefinition("DELETE", "/v0/admin/provider-api-keys/:id", "Delete Provider API Key", "Provider API Keys"),

	newDefinition("POST", "/v0/admin/proxies", "Create Proxy", "Proxies"),
	newDefinition("POST", "/v0/admin/proxies/batch", "Batch Create Proxies", "Proxies"),
	newDefinition("GET", "/v0/admin/proxies", "List Proxies", "Proxies"),
	newDefinition("PUT", "/v0/admin/proxies/:id", "Update Proxy", "Proxies"),
	newDefinition("DELETE", "/v0/admin/proxies/:id", "Delete Proxy", "Proxies"),

	newDefinition("POST", "/v0/admin/prepaid-cards", "Create Prepaid Card", "Prepaid Cards"),
	newDefinition("POST", "/v0/admin/prepaid-cards/batch", "Batch Create Prepaid Cards", "Prepaid Cards"),
	newDefinition("GET", "/v0/admin/prepaid-cards", "List Prepaid Cards", "Prepaid Cards"),
	newDefinition("GET", "/v0/admin/prepaid-cards/:id", "Get Prepaid Card", "Prepaid Cards"),
	newDefinition("PUT", "/v0/admin/prepaid-cards/:id", "Update Prepaid Card", "Prepaid Cards"),
	newDefinition("DELETE", "/v0/admin/prepaid-cards/:id", "Delete Prepaid Card", "Prepaid Cards"),

	newDefinition("POST", "/v0/admin/bills", "Create Bill", "Bills"),
	newDefinition("GET", "/v0/admin/bills", "List Bills", "Bills"),
	newDefinition("GET", "/v0/admin/bills/:id", "Get Bill", "Bills"),
	newDefinition("PUT", "/v0/admin/bills/:id", "Update Bill", "Bills"),
	newDefinition("DELETE", "/v0/admin/bills/:id", "Delete Bill", "Bills"),
	newDefinition("POST", "/v0/admin/bills/:id/enable", "Enable Bill", "Bills"),
	newDefinition("POST", "/v0/admin/bills/:id/disable", "Disable Bill", "Bills"),

	newDefinition("POST", "/v0/admin/billing-rules", "Create Billing Rule", "Billing Rules"),
	newDefinition("GET", "/v0/admin/billing-rules", "List Billing Rules", "Billing Rules"),
	newDefinition("GET", "/v0/admin/billing-rules/:id", "Get Billing Rule", "Billing Rules"),
	newDefinition("PUT", "/v0/admin/billing-rules/:id", "Update Billing Rule", "Billing Rules"),
	newDefinition("DELETE", "/v0/admin/billing-rules/:id", "Delete Billing Rule", "Billing Rules"),
	newDefinition("POST", "/v0/admin/billing-rules/:id/enabled", "Set Billing Rule Enabled", "Billing Rules"),
	newDefinition("POST", "/v0/admin/billing-rules/batch-import", "Batch Import Billing Rules", "Billing Rules"),

	newDefinition("GET", "/v0/admin/logs", "List Logs", "Logs"),
	newDefinition("GET", "/v0/admin/logs/detail", "View Log Details", "Logs"),
	newDefinition("GET", "/v0/admin/logs/stats", "View Log Stats", "Logs"),
	newDefinition("GET", "/v0/admin/logs/trend", "View Log Trend", "Logs"),
	newDefinition("GET", "/v0/admin/logs/models", "View Log Models", "Logs"),
	newDefinition("GET", "/v0/admin/logs/projects", "View Log Projects", "Logs"),

	newDefinition("POST", "/v0/admin/settings", "Create Setting", "Settings"),
	newDefinition("GET", "/v0/admin/settings", "List Settings", "Settings"),
	newDefinition("GET", "/v0/admin/settings/:key", "Get Setting", "Settings"),
	newDefinition("PUT", "/v0/admin/settings/:key", "Update Setting", "Settings"),
	newDefinition("DELETE", "/v0/admin/settings/:key", "Delete Setting", "Settings"),

	newDefinition("GET", "/v0/admin/usage", "View Usage", "Usage"),
	newDefinition("GET", "/v0/admin/billing/summary", "View Billing Summary", "Billing"),

	newDefinition("POST", "/v0/admin/admins", "Create Administrator", "Administrators"),
	newDefinition("GET", "/v0/admin/admins", "List Administrators", "Administrators"),
	newDefinition("GET", "/v0/admin/admins/:id", "Get Administrator", "Administrators"),
	newDefinition("PUT", "/v0/admin/admins/:id", "Update Administrator", "Administrators"),
	newDefinition("DELETE", "/v0/admin/admins/:id", "Delete Administrator", "Administrators"),
	newDefinition("POST", "/v0/admin/admins/:id/disable", "Disable Administrator", "Administrators"),
	newDefinition("POST", "/v0/admin/admins/:id/enable", "Enable Administrator", "Administrators"),
	newDefinition("PUT", "/v0/admin/admins/:id/password", "Change Administrator Password", "Administrators"),
	newDefinition("GET", "/v0/admin/permissions", "List Permission Definitions", "Administrators"),

	newDefinition("POST", "/v0/admin/plans", "Create Plan", "Plans"),
	newDefinition("GET", "/v0/admin/plans", "List Plans", "Plans"),
	newDefinition("GET", "/v0/admin/plans/:id", "Get Plan", "Plans"),
	newDefinition("PUT", "/v0/admin/plans/:id", "Update Plan", "Plans"),
	newDefinition("DELETE", "/v0/admin/plans/:id", "Delete Plan", "Plans"),
	newDefinition("POST", "/v0/admin/plans/:id/enable", "Enable Plan", "Plans"),
	newDefinition("POST", "/v0/admin/plans/:id/disable", "Disable Plan", "Plans"),

	newDefinition("POST", "/v0/admin/tokens/anthropic", "Request Anthropic Token", "Auth Tokens"),
	newDefinition("POST", "/v0/admin/tokens/gemini", "Request Gemini Token", "Auth Tokens"),
	newDefinition("POST", "/v0/admin/tokens/codex", "Request Codex Token", "Auth Tokens"),
	newDefinition("POST", "/v0/admin/tokens/antigravity", "Request Antigravity Token", "Auth Tokens"),
	newDefinition("POST", "/v0/admin/tokens/qwen", "Request Qwen Token", "Auth Tokens"),
	newDefinition("POST", "/v0/admin/tokens/iflow", "Request IFlow Token", "Auth Tokens"),
	newDefinition("POST", "/v0/admin/tokens/iflow-cookie", "Request IFlow Cookie Token", "Auth Tokens"),
	newDefinition("POST", "/v0/admin/tokens/get-auth-status", "Check Auth Status", "Auth Tokens"),
	newDefinition("POST", "/v0/admin/tokens/oauth-callback", "Submit OAuth Callback", "Auth Tokens"),
}

// definitionMap provides fast lookup for permission definitions.
var definitionMap = func() map[string]Definition {
	out := make(map[string]Definition, len(definitions))
	for _, def := range definitions {
		out[def.Key] = def
	}
	return out
}()
