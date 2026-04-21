package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/metro-olografix/sede/internal/config"
	"golang.org/x/crypto/bcrypt"
)

func writeYAML(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "spaces.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	return p
}

func baseCfg(t *testing.T, dir string) config.Config {
	t.Helper()
	return config.Config{
		Port:             "8080",
		APIKey:           "test-api-key-123456",
		Debug:            true,
		DatabasePath:     filepath.Join(dir, "test.db"),
		DefaultSpaceSlug: "pescara",
	}
}

func TestNewApp_LegacyUpgradePath(t *testing.T) {
	dir := t.TempDir()
	cfg := baseCfg(t, dir)
	cfg.SpacesConfigPath = filepath.Join(dir, "does-not-exist.yaml")

	app, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer closeApp(app)

	if app.defaultSpace == nil {
		t.Fatal("defaultSpace nil")
	}
	if app.defaultSpace.Slug != "pescara" {
		t.Errorf("want slug pescara, got %q", app.defaultSpace.Slug)
	}
	if len(app.spaces) != 1 {
		t.Errorf("want 1 space, got %d", len(app.spaces))
	}
	if err := bcrypt.CompareHashAndPassword(app.defaultSpace.APIKeyHash, []byte(cfg.APIKey)); err != nil {
		t.Errorf("legacy api key not hashed into default space: %v", err)
	}
}

func TestNewApp_LoadsYAMLAndSeedsDB(t *testing.T) {
	dir := t.TempDir()
	yaml := `spaces:
  - slug: pescara
    name: Metro Olografix Pescara
    address: Viale Marconi 278/1
    lat: 42.454657
    lon: 14.224055
    timezone: Europe/Rome
    api_key: pescara-key-1234567890
    telegram:
      chat_id: 111
      thread_id: 1
  - slug: bologna
    name: Metro Olografix Bologna
    address: Via Test 1
    lat: 44.494887
    lon: 11.342616
    timezone: Europe/Rome
    api_key: bologna-key-1234567890
    telegram:
      chat_id: 222
      thread_id: 2
`
	cfg := baseCfg(t, dir)
	cfg.SpacesConfigPath = writeYAML(t, dir, yaml)

	app, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer closeApp(app)

	if len(app.spaces) != 2 {
		t.Fatalf("want 2 spaces, got %d", len(app.spaces))
	}
	if app.defaultSpace == nil || app.defaultSpace.Slug != "pescara" {
		t.Errorf("default space not pescara: %+v", app.defaultSpace)
	}
	bologna := app.spaces["bologna"]
	if bologna == nil {
		t.Fatal("bologna missing")
	}
	if bologna.TelegramChatID != 222 || bologna.TelegramThread != 2 {
		t.Errorf("bologna telegram wrong: chat=%d thread=%d", bologna.TelegramChatID, bologna.TelegramThread)
	}
	if err := bcrypt.CompareHashAndPassword(bologna.APIKeyHash, []byte("bologna-key-1234567890")); err != nil {
		t.Errorf("bologna hash mismatch: %v", err)
	}

	spaces, err := app.repo.ListSpaces(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(spaces) != 2 {
		t.Errorf("want 2 rows in db, got %d", len(spaces))
	}
}

func TestNewApp_UpsertIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	yaml1 := `spaces:
  - slug: pescara
    name: Pescara v1
    lat: 42.454657
    lon: 14.224055
    api_key: pescara-key-1234567890
`
	cfg := baseCfg(t, dir)
	cfg.SpacesConfigPath = writeYAML(t, dir, yaml1)

	app1, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("NewApp#1: %v", err)
	}
	closeApp(app1)

	yaml2 := `spaces:
  - slug: pescara
    name: Pescara v2
    lat: 42.454657
    lon: 14.224055
    api_key: pescara-key-rotated-01
`
	_ = writeYAML(t, dir, yaml2)

	app2, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("NewApp#2: %v", err)
	}
	defer closeApp(app2)

	spaces, err := app2.repo.ListSpaces(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(spaces) != 1 {
		t.Fatalf("want 1 row after rerun, got %d", len(spaces))
	}
	if spaces[0].Name != "Pescara v2" {
		t.Errorf("upsert did not update name: %q", spaces[0].Name)
	}
	if err := bcrypt.CompareHashAndPassword(spaces[0].APIKeyHash, []byte("pescara-key-rotated-01")); err != nil {
		t.Errorf("rotated key not persisted: %v", err)
	}
}

func TestNewApp_RejectsDuplicateSlugs(t *testing.T) {
	dir := t.TempDir()
	yaml := `spaces:
  - slug: pescara
    name: A
    lat: 42.0
    lon: 14.0
    api_key: key-1234567890abcdef
  - slug: pescara
    name: B
    lat: 42.0
    lon: 14.0
    api_key: key-abcdef1234567890
`
	cfg := baseCfg(t, dir)
	cfg.SpacesConfigPath = writeYAML(t, dir, yaml)

	if _, err := NewApp(cfg); err == nil {
		t.Fatal("expected error on duplicate slugs")
	}
}

func TestNewApp_RejectsEmptyYAMLWithoutLegacyEnv(t *testing.T) {
	dir := t.TempDir()
	cfg := baseCfg(t, dir)
	cfg.APIKey = ""
	cfg.SpacesConfigPath = filepath.Join(dir, "missing.yaml")

	if _, err := NewApp(cfg); err == nil {
		t.Fatal("expected error: no YAML and no legacy API_KEY")
	}
}

func TestNewApp_DefaultSlugMissingFromYAML(t *testing.T) {
	dir := t.TempDir()
	yaml := `spaces:
  - slug: bologna
    name: Bologna
    lat: 44.49
    lon: 11.34
    api_key: bologna-key-1234567890
`
	cfg := baseCfg(t, dir)
	cfg.SpacesConfigPath = writeYAML(t, dir, yaml)

	if _, err := NewApp(cfg); err == nil {
		t.Fatal("expected error: default slug pescara not in YAML")
	}
}

func closeApp(app *App) {
	if app == nil || app.repo == nil {
		return
	}
	if sqlDB, err := app.repo.Db.DB(); err == nil {
		sqlDB.Close()
	}
}
