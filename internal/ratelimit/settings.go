package ratelimit

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	internalsettings "github.com/router-for-me/CLIProxyAPIBusiness/internal/settings"
)

// SettingsConfig captures rate limit settings stored in DB config.
type SettingsConfig struct {
	Limit         int
	RedisEnabled  bool
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	RedisPrefix   string
}

// LoadSettingsConfig loads the current rate limit settings snapshot.
func LoadSettingsConfig() SettingsConfig {
	cfg := SettingsConfig{
		Limit:       internalsettings.DefaultRateLimit,
		RedisPrefix: internalsettings.DefaultRateLimitRedisPrefix,
	}

	if raw, ok := internalsettings.DBConfigValue(internalsettings.RateLimitKey); ok {
		if limit, okParse := parseNonNegativeInt(raw); okParse {
			cfg.Limit = limit
		}
	}
	if raw, ok := internalsettings.DBConfigValue(internalsettings.RateLimitRedisEnabledKey); ok {
		if enabled, okParse := parseBool(raw); okParse {
			cfg.RedisEnabled = enabled
		}
	}
	if raw, ok := internalsettings.DBConfigValue(internalsettings.RateLimitRedisAddrKey); ok {
		if addr, okParse := parseString(raw); okParse {
			cfg.RedisAddr = addr
		}
	}
	if raw, ok := internalsettings.DBConfigValue(internalsettings.RateLimitRedisPasswordKey); ok {
		if password, okParse := parseString(raw); okParse {
			cfg.RedisPassword = password
		}
	}
	if raw, ok := internalsettings.DBConfigValue(internalsettings.RateLimitRedisDBKey); ok {
		if db, okParse := parseNonNegativeInt(raw); okParse {
			cfg.RedisDB = db
		}
	}
	if raw, ok := internalsettings.DBConfigValue(internalsettings.RateLimitRedisPrefixKey); ok {
		if prefix, okParse := parseString(raw); okParse {
			cfg.RedisPrefix = prefix
		}
	}
	cfg.RedisAddr = strings.TrimSpace(cfg.RedisAddr)
	cfg.RedisPassword = strings.TrimSpace(cfg.RedisPassword)
	cfg.RedisPrefix = strings.TrimSpace(cfg.RedisPrefix)
	if cfg.RedisPrefix == "" {
		cfg.RedisPrefix = internalsettings.DefaultRateLimitRedisPrefix
	}
	if cfg.RedisDB < 0 {
		cfg.RedisDB = 0
	}
	if cfg.Limit < 0 {
		cfg.Limit = 0
	}
	return cfg
}

// DefaultSettingsLimit returns the default rate limit configured in settings.
func DefaultSettingsLimit() int {
	cfg := LoadSettingsConfig()
	if cfg.Limit < 0 {
		return 0
	}
	return cfg.Limit
}

func parseBool(raw json.RawMessage) (bool, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return false, false
	}
	var parsedBool bool
	if errUnmarshalBool := json.Unmarshal(raw, &parsedBool); errUnmarshalBool == nil {
		return parsedBool, true
	}
	var parsedString string
	if errUnmarshalString := json.Unmarshal(raw, &parsedString); errUnmarshalString == nil {
		switch strings.ToLower(strings.TrimSpace(parsedString)) {
		case "1", "true", "yes", "y", "on":
			return true, true
		case "0", "false", "no", "n", "off":
			return false, true
		default:
			return false, false
		}
	}
	var parsedFloat float64
	if errUnmarshalFloat := json.Unmarshal(raw, &parsedFloat); errUnmarshalFloat == nil {
		if math.IsNaN(parsedFloat) || math.IsInf(parsedFloat, 0) {
			return false, false
		}
		if parsedFloat == 1 {
			return true, true
		}
		if parsedFloat == 0 {
			return false, true
		}
	}
	return false, false
}

func parseString(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", false
	}
	var parsedString string
	if errUnmarshal := json.Unmarshal(raw, &parsedString); errUnmarshal == nil {
		return strings.TrimSpace(parsedString), true
	}
	return "", false
}

func parseNonNegativeInt(raw json.RawMessage) (int, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0, false
	}
	var parsedInt int
	if errUnmarshalInt := json.Unmarshal(raw, &parsedInt); errUnmarshalInt == nil {
		return parsedInt, parsedInt >= 0
	}
	var parsedString string
	if errUnmarshalString := json.Unmarshal(raw, &parsedString); errUnmarshalString == nil {
		parsed, errParse := strconv.Atoi(strings.TrimSpace(parsedString))
		if errParse != nil {
			return 0, false
		}
		return parsed, parsed >= 0
	}
	var parsedFloat float64
	if errUnmarshalFloat := json.Unmarshal(raw, &parsedFloat); errUnmarshalFloat == nil {
		if math.IsNaN(parsedFloat) || math.IsInf(parsedFloat, 0) {
			return 0, false
		}
		if parsedFloat < 0 || parsedFloat != math.Trunc(parsedFloat) {
			return 0, false
		}
		return int(parsedFloat), true
	}
	return 0, false
}
