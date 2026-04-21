package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "spaces.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	return p
}

const validTwoSpaces = `
spaces:
  - slug: pescara
    name: Metro Olografix Pescara
    address: Viale Marconi 278/1, Pescara
    lat: 42.454657
    lon: 14.224055
    timezone: Europe/Rome
    logo_url: https://olografix.org/logo.png
    url: https://olografix.org
    contact:
      email: info@olografix.org
    message: Open on Mondays
    api_key: $PESCARA_API_KEY
    telegram:
      chat_id: -100123
      thread_id: 42
    projects:
      - https://github.com/Metro-Olografix
    links:
      - name: MOCA
        description: hacker camp
        url: https://moca.camp
  - slug: aquila
    name: Metro Olografix L'Aquila
    lat: 44.494887
    lon: 11.342616
    api_key: aquila-plain-key-0000
    telegram:
      chat_id: -100456
`

func TestLoadSpaces_ValidYAML(t *testing.T) {
	t.Setenv("PESCARA_API_KEY", "pescara-secret-from-env")
	path := writeYAML(t, validTwoSpaces)

	defs, err := LoadSpaces(path)
	if err != nil {
		t.Fatalf("LoadSpaces: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 spaces, got %d", len(defs))
	}

	p := defs[0]
	if p.Slug != "pescara" ||
		p.Name != "Metro Olografix Pescara" ||
		p.Address != "Viale Marconi 278/1, Pescara" ||
		p.Lat != 42.454657 ||
		p.Lon != 14.224055 ||
		p.Timezone != "Europe/Rome" ||
		p.LogoURL != "https://olografix.org/logo.png" ||
		p.URL != "https://olografix.org" ||
		p.ContactEmail != "info@olografix.org" ||
		p.Message != "Open on Mondays" ||
		p.APIKey != "pescara-secret-from-env" ||
		p.TelegramChatID != -100123 ||
		p.TelegramThread != 42 {
		t.Errorf("pescara fields not fully populated: %+v", p)
	}
	if len(p.Projects) != 1 || p.Projects[0] != "https://github.com/Metro-Olografix" {
		t.Errorf("projects mismatch: %+v", p.Projects)
	}
	if len(p.Links) != 1 || p.Links[0].Name != "MOCA" || p.Links[0].URL != "https://moca.camp" {
		t.Errorf("links mismatch: %+v", p.Links)
	}

	b := defs[1]
	if b.Slug != "aquila" || b.APIKey != "aquila-plain-key-0000" || b.TelegramChatID != -100456 || b.TelegramThread != 0 {
		t.Errorf("aquila fields wrong: %+v", b)
	}
}

func TestLoadSpaces_EnvInterpolation_MissingEnv(t *testing.T) {
	os.Unsetenv("PESCARA_API_KEY")
	path := writeYAML(t, `
spaces:
  - slug: pescara
    name: P
    lat: 0
    lon: 0
    api_key: $PESCARA_API_KEY
`)

	_, err := LoadSpaces(path)
	if err == nil {
		t.Fatal("expected error when referenced env var is missing")
	}
	if !strings.Contains(err.Error(), "PESCARA_API_KEY") {
		t.Errorf("error should name the missing variable, got: %v", err)
	}
}

func TestLoadSpaces_MissingFile(t *testing.T) {
	_, err := LoadSpaces(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error should wrap os.ErrNotExist, got: %v", err)
	}
}

func TestLoadSpaces_MalformedYAML(t *testing.T) {
	path := writeYAML(t, "spaces: [this is not: valid yaml\n  indent broken")

	_, err := LoadSpaces(path)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse spaces config") {
		t.Errorf("error should mention parse failure, got: %v", err)
	}
}

func TestLoadSpaces_DuplicateSlug(t *testing.T) {
	path := writeYAML(t, `
spaces:
  - slug: pescara
    name: A
    lat: 0
    lon: 0
    api_key: keyAkeyAkeyAkeyA
  - slug: pescara
    name: B
    lat: 1
    lon: 1
    api_key: keyBkeyBkeyBkeyB
`)
	_, err := LoadSpaces(path)
	if err == nil || !strings.Contains(err.Error(), "duplicate slug") {
		t.Fatalf("expected duplicate-slug error, got: %v", err)
	}
}

func TestLoadSpaces_EmptyRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "missing slug",
			yaml: `spaces: [{name: A, lat: 0, lon: 0, api_key: kkkkkkkkkkkkkkkk}]`,
			want: "slug is required",
		},
		{
			name: "missing name",
			yaml: `spaces: [{slug: x, lat: 0, lon: 0, api_key: kkkkkkkkkkkkkkkk}]`,
			want: "name is required",
		},
		{
			name: "missing api_key",
			yaml: `spaces: [{slug: x, name: X, lat: 0, lon: 0}]`,
			want: "api_key is required",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadSpaces(writeYAML(t, c.yaml))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected error containing %q, got: %v", c.want, err)
			}
		})
	}
}

func TestLoadSpaces_InvalidLatLon(t *testing.T) {
	cases := []struct {
		name string
		lat  float64
		lon  float64
		want string
	}{
		{"lat too high", 91, 0, "lat 91"},
		{"lat too low", -91, 0, "lat -91"},
		{"lon too high", 0, 181, "lon 181"},
		{"lon too low", 0, -181, "lon -181"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			yaml := fmt.Sprintf(
				"spaces:\n  - slug: x\n    name: X\n    api_key: kkkkkkkkkkkkkkkk\n    lat: %g\n    lon: %g\n",
				c.lat, c.lon,
			)
			_, err := LoadSpaces(writeYAML(t, yaml))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected %q, got: %v", c.want, err)
			}
		})
	}
}

func TestLoadSpaces_EmptyFileRejected(t *testing.T) {
	_, err := LoadSpaces(writeYAML(t, "spaces: []\n"))
	if err == nil || !strings.Contains(err.Error(), "no spaces") {
		t.Fatalf("expected 'no spaces' error, got: %v", err)
	}
}

func TestLegacySpaceFromConfig(t *testing.T) {
	cfg := Config{
		APIKey:               "legacy-api-key-0000",
		DefaultSpaceSlug:     "pescara",
		TelegramChatId:       -100999,
		TelegramChatThreadId: 7,
	}
	def := LegacySpaceFromConfig(cfg)

	if def.Slug != "pescara" {
		t.Errorf("slug: got %q", def.Slug)
	}
	if def.APIKey != "legacy-api-key-0000" {
		t.Errorf("api_key not propagated from legacy cfg: %q", def.APIKey)
	}
	if def.TelegramChatID != -100999 || def.TelegramThread != 7 {
		t.Errorf("telegram: %+v", def)
	}
	// SpaceAPI metadata hardcoded to Pescara values to match pre-refactor
	// /spaceapi.json response byte-for-byte.
	if def.Name != "Metro Olografix" || def.Lat != 42.454657 || def.Lon != 14.224055 {
		t.Errorf("pescara spaceapi defaults wrong: %+v", def)
	}
	if len(def.Links) != 2 || def.Links[0].URL != "https://moca.camp" {
		t.Errorf("links defaults wrong: %+v", def.Links)
	}
	if err := ValidateSpaces([]SpaceDef{def}); err != nil {
		t.Errorf("legacy-synthesised def should validate: %v", err)
	}
}
