package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// SpaceDef is the resolved, validated description of a single space as loaded
// from spaces.yaml. Secrets referenced via $ENV_VAR are already substituted.
type SpaceDef struct {
	Slug           string
	Name           string
	Address        string
	Lat            float64
	Lon            float64
	Timezone       string
	LogoURL        string
	URL            string
	ContactEmail   string
	Message        string
	APIKey         string
	TelegramChatID int64
	TelegramThread int
	Projects       []string
	Links          []SpaceLink
}

type SpaceLink struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	URL         string `yaml:"url" json:"url"`
}

type spacesFile struct {
	Spaces []spaceEntry `yaml:"spaces"`
}

type spaceEntry struct {
	Slug     string        `yaml:"slug"`
	Name     string        `yaml:"name"`
	Address  string        `yaml:"address"`
	Lat      float64       `yaml:"lat"`
	Lon      float64       `yaml:"lon"`
	Timezone string        `yaml:"timezone"`
	LogoURL  string        `yaml:"logo_url"`
	URL      string        `yaml:"url"`
	Contact  contactEntry  `yaml:"contact"`
	Message  string        `yaml:"message"`
	APIKey   string        `yaml:"api_key"`
	Telegram telegramEntry `yaml:"telegram"`
	Projects []string      `yaml:"projects"`
	Links    []SpaceLink   `yaml:"links"`
}

type contactEntry struct {
	Email string `yaml:"email"`
}

type telegramEntry struct {
	ChatID   int64 `yaml:"chat_id"`
	ThreadID int   `yaml:"thread_id"`
}

// LoadSpaces reads spaces.yaml from path, resolves $ENV_VAR references in
// secret fields, and validates the result. A missing file returns an error
// that wraps os.ErrNotExist, so callers can fall back to the legacy-env path.
func LoadSpaces(path string) ([]SpaceDef, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("spaces config not found at %s: %w", path, err)
		}
		return nil, fmt.Errorf("read spaces config %s: %w", path, err)
	}

	var file spacesFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse spaces config %s: %w", path, err)
	}

	defs := make([]SpaceDef, 0, len(file.Spaces))
	for i, e := range file.Spaces {
		apiKey, err := resolveEnvRef(e.APIKey)
		if err != nil {
			return nil, fmt.Errorf("space[%d] (%q) api_key: %w", i, e.Slug, err)
		}
		defs = append(defs, SpaceDef{
			Slug:           e.Slug,
			Name:           e.Name,
			Address:        e.Address,
			Lat:            e.Lat,
			Lon:            e.Lon,
			Timezone:       e.Timezone,
			LogoURL:        e.LogoURL,
			URL:            e.URL,
			ContactEmail:   e.Contact.Email,
			Message:        e.Message,
			APIKey:         apiKey,
			TelegramChatID: e.Telegram.ChatID,
			TelegramThread: e.Telegram.ThreadID,
			Projects:       e.Projects,
			Links:          e.Links,
		})
	}

	if err := ValidateSpaces(defs); err != nil {
		return nil, err
	}
	return defs, nil
}

// ValidateSpaces enforces required fields, unique slugs, and sane lat/lon.
func ValidateSpaces(defs []SpaceDef) error {
	if len(defs) == 0 {
		return errors.New("no spaces defined")
	}
	seen := make(map[string]struct{}, len(defs))
	for i, d := range defs {
		if d.Slug == "" {
			return fmt.Errorf("space[%d]: slug is required", i)
		}
		if d.Name == "" {
			return fmt.Errorf("space[%d] (%q): name is required", i, d.Slug)
		}
		if d.APIKey == "" {
			return fmt.Errorf("space[%d] (%q): api_key is required", i, d.Slug)
		}
		if d.Lat < -90 || d.Lat > 90 {
			return fmt.Errorf("space[%d] (%q): lat %f out of range [-90, 90]", i, d.Slug, d.Lat)
		}
		if d.Lon < -180 || d.Lon > 180 {
			return fmt.Errorf("space[%d] (%q): lon %f out of range [-180, 180]", i, d.Slug, d.Lon)
		}
		if _, dup := seen[d.Slug]; dup {
			return fmt.Errorf("duplicate slug %q", d.Slug)
		}
		seen[d.Slug] = struct{}{}
	}
	return nil
}

// LegacySpaceFromConfig synthesises the single-space definition used when no
// spaces.yaml is present. It reuses the legacy API_KEY + TELEGRAM_* env vars
// and the previously-hardcoded Metro Olografix Pescara SpaceAPI metadata, so
// an existing single-space deployment upgrades with zero config changes.
func LegacySpaceFromConfig(cfg Config) SpaceDef {
	return SpaceDef{
		Slug:           cfg.DefaultSpaceSlug,
		Name:           "Metro Olografix",
		Address:        "Viale Marconi 278/1, 65126 Pescara, Italy",
		Lat:            42.454657,
		Lon:            14.224055,
		Timezone:       "Europe/Rome",
		LogoURL:        "https://olografix.org/images/metro-dark.png",
		URL:            "https://olografix.org",
		ContactEmail:   "info@olografix.org",
		Message:        "We meet every Monday evening from 9:00 PM",
		APIKey:         cfg.APIKey,
		TelegramChatID: cfg.TelegramChatId,
		TelegramThread: cfg.TelegramChatThreadId,
		Projects:       []string{"https://github.com/Metro-Olografix"},
		Links: []SpaceLink{
			{Name: "MOCA - Metro Olografix Camp", Description: "Il più antico campeggio hacker in Italia", URL: "https://moca.camp"},
			{Name: "Wikipedia", Description: "Metro Olografix Wikipedia page", URL: "https://it.wikipedia.org/wiki/Metro_Olografix"},
		},
	}
}

// resolveEnvRef expands a single $ENV_VAR reference. Literal values pass
// through unchanged. A reference to an unset variable is an error, so missing
// secrets fail loud at boot instead of silently sending empty keys.
func resolveEnvRef(v string) (string, error) {
	if !strings.HasPrefix(v, "$") {
		return v, nil
	}
	name := strings.TrimPrefix(v, "$")
	if name == "" {
		return "", errors.New(`"$" with no variable name`)
	}
	val, ok := os.LookupEnv(name)
	if !ok {
		return "", fmt.Errorf("environment variable %s is not set", name)
	}
	return val, nil
}
