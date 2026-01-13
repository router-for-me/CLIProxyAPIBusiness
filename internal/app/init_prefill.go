package app

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type initPrefill struct {
	DatabaseType        string `json:"database_type"`
	DatabaseHost        string `json:"database_host"`
	DatabasePort        int    `json:"database_port"`
	DatabaseUser        string `json:"database_user"`
	DatabaseName        string `json:"database_name"`
	DatabaseSSLMode     string `json:"database_ssl_mode"`
	DatabasePath        string `json:"database_path"`
	DatabasePasswordSet bool   `json:"database_password_set"`
}

func initPrefillFromDSN(dsn string) (initPrefill, error) {
	trimmed := strings.TrimSpace(dsn)
	if trimmed == "" {
		return initPrefill{}, fmt.Errorf("empty dsn")
	}

	lowered := strings.ToLower(trimmed)
	if strings.HasPrefix(lowered, "file:") {
		pathPart := trimmed[len("file:"):]
		pathPart, _, _ = strings.Cut(pathPart, "?")
		pathPart = strings.TrimSpace(pathPart)
		return initPrefill{
			DatabaseType:        "sqlite",
			DatabasePath:        pathPart,
			DatabasePasswordSet: false,
		}, nil
	}

	u, errParse := url.Parse(trimmed)
	if errParse != nil {
		return initPrefill{}, fmt.Errorf("parse dsn: %w", errParse)
	}

	switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
	case "postgres", "postgresql":
		port := 5432
		if rawPort := strings.TrimSpace(u.Port()); rawPort != "" {
			parsedPort, errPort := strconv.Atoi(rawPort)
			if errPort != nil {
				return initPrefill{}, fmt.Errorf("parse port: %w", errPort)
			}
			port = parsedPort
		}

		username := ""
		passwordSet := false
		if u.User != nil {
			username = strings.TrimSpace(u.User.Username())
			_, passwordSet = u.User.Password()
		}

		dbName := strings.TrimPrefix(u.Path, "/")
		sslMode := strings.TrimSpace(u.Query().Get("sslmode"))
		if sslMode == "" {
			sslMode = "disable"
		}

		return initPrefill{
			DatabaseType:        "postgres",
			DatabaseHost:        strings.TrimSpace(u.Hostname()),
			DatabasePort:        port,
			DatabaseUser:        username,
			DatabaseName:        strings.TrimSpace(dbName),
			DatabaseSSLMode:     sslMode,
			DatabasePasswordSet: passwordSet,
		}, nil
	default:
		return initPrefill{}, fmt.Errorf("unsupported dsn scheme")
	}
}
