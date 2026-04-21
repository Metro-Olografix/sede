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

	// SpacesConfigPath points to the YAML file that defines all spaces served
	// by this instance. Empty / missing file triggers the legacy-upgrade path
	// (single space synthesised from APIKey + TelegramToken + TelegramChatId).
	SpacesConfigPath string
	// DefaultSpaceSlug names the space that legacy bare routes (/status,
	// /toggle, /stats, /spaceapi.json, /ui) resolve to.
	DefaultSpaceSlug string

	// Legacy single-space Telegram target. Used only for the one-time upgrade
	// path: when SpacesConfigPath is missing, these seed the default space.
	TelegramToken        string
	TelegramChatId       int64
	TelegramChatThreadId int
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

	if cfg.SpacesConfigPath == "" {
		cfg.SpacesConfigPath = "config/spaces.yaml"
	}

	if cfg.DefaultSpaceSlug == "" {
		cfg.DefaultSpaceSlug = "pescara"
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
