package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type Config struct {
	Port              string
	APIKey            string
	Debug             bool
	AllowedOrigins    []string
	AllowedOriginsStr string
	HashAPIKey        bool
	DatabasePath      string
}

func ValidateAndSetDefaults(cfg Config) Config {
	if _, err := strconv.Atoi(cfg.Port); err != nil {
		panic(fmt.Sprintf("invalid port number: %s", cfg.Port))
	}

	if len(cfg.APIKey) < 16 && !cfg.Debug {
		panic("API key must be at least 16 characters in production")
	}

	cfg.AllowedOrigins = parseAndValidateOrigins(cfg.AllowedOriginsStr)

	if cfg.DatabasePath == "" {
		cfg.DatabasePath = "database/sede.db"
	}

	return cfg
}

func parseAndValidateOrigins(origins string) []string {
	if origins == "" {
		return []string{}
	}

	validOrigins := make([]string, 0)
	for _, origin := range strings.Split(origins, ",") {
		u, err := url.Parse(origin)
		if err == nil && u.Scheme != "" && u.Host != "" {
			validOrigins = append(validOrigins, origin)
		}
	}
	return validOrigins
}
