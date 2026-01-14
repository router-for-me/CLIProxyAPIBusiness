package ratelimit

import "fmt"

// KeyForDecision builds a limiter key for the resolved scope.
func KeyForDecision(userID uint64, decision Decision) string {
	if userID == 0 || decision.Limit <= 0 {
		return ""
	}
	switch decision.Scope {
	case ScopeModelMapping:
		if decision.MappingID == 0 {
			return ""
		}
		return fmt.Sprintf("u:%d:m:%d", userID, decision.MappingID)
	case ScopeUser:
		return fmt.Sprintf("u:%d", userID)
	default:
		return ""
	}
}
