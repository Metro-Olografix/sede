package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/metro-olografix/sede/internal/config"
	"github.com/metro-olografix/sede/internal/database"
)

const (
	pescaraKey = "pescara-key-123456"
	bolognaKey = "bologna-key-123456"
)

func twoSpaceYAML(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := `spaces:
  - slug: pescara
    name: Metro Olografix Pescara
    address: Viale Marconi 278/1
    lat: 42.454657
    lon: 14.224055
    timezone: Europe/Rome
    logo_url: https://example.com/pescara.png
    url: https://pescara.example
    contact:
      email: pescara@example.org
    message: Pescara welcomes you
    api_key: ` + pescaraKey + `
    telegram:
      chat_id: 1001
      thread_id: 11
    projects:
      - https://github.com/Metro-Olografix
    links:
      - name: MOCA
        description: campeggio hacker
        url: https://moca.camp
  - slug: bologna
    name: Metro Olografix Bologna
    address: Via Test 1
    lat: 44.494887
    lon: 11.342616
    timezone: Europe/Rome
    logo_url: https://example.com/bologna.png
    url: https://bologna.example
    contact:
      email: bologna@example.org
    message: Bologna welcomes you
    api_key: ` + bolognaKey + `
    telegram:
      chat_id: 0
      thread_id: 0
`
	p := filepath.Join(dir, "spaces.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	return p
}

func setupTestApp(t *testing.T) (*App, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := config.Config{
		Port:             "8080",
		APIKey:           "ignored-legacy-key-1234",
		Debug:            true,
		DatabasePath:     dbPath,
		SpacesConfigPath: twoSpaceYAML(t),
		DefaultSpaceSlug: "pescara",
	}

	app, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}

	cleanup := func() {
		if sqlDB, err := app.repo.Db.DB(); err == nil {
			sqlDB.Close()
		}
	}
	return app, cleanup
}

func createTestStatusFor(t *testing.T, app *App, spaceID uint, isOpen bool, timestamp time.Time) {
	t.Helper()
	if err := app.repo.CreateStatus(context.Background(), database.SedeStatus{
		SpaceID:   spaceID,
		IsOpen:    isOpen,
		Timestamp: timestamp,
	}); err != nil {
		t.Fatalf("CreateStatus: %v", err)
	}
}

func doReq(router *gin.Engine, method, path, key string, body []byte) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	var r *http.Request
	if body != nil {
		r, _ = http.NewRequest(method, path, bytes.NewBuffer(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r, _ = http.NewRequest(method, path, nil)
	}
	if key != "" {
		r.Header.Set("X-API-KEY", key)
	}
	router.ServeHTTP(w, r)
	return w
}

func TestGetStatus_PerSpace(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()
	router := app.setupRouter()

	pescaraID := app.spaces["pescara"].ID
	bolognaID := app.spaces["bologna"].ID
	createTestStatusFor(t, app, pescaraID, true, time.Now().UTC())
	createTestStatusFor(t, app, bolognaID, false, time.Now().UTC())

	for _, tc := range []struct {
		path, want string
	}{
		{"/s/pescara/status", "true"},
		{"/s/bologna/status", "false"},
		{"/status", "true"},
	} {
		w := doReq(router, "GET", tc.path, "", nil)
		if w.Code != http.StatusOK {
			t.Errorf("%s: code %d", tc.path, w.Code)
		}
		if w.Body.String() != tc.want {
			t.Errorf("%s: body %q want %q", tc.path, w.Body.String(), tc.want)
		}
	}
}

func TestResolveSpace_UnknownSlug(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()
	router := app.setupRouter()

	w := doReq(router, "GET", "/s/nope/status", "", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestLegacyAlias_MatchesDefault(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()
	router := app.setupRouter()

	createTestStatusFor(t, app, app.defaultSpace.ID, true, time.Now().UTC())

	for _, p := range []string{"/status", "/stats", "/spaceapi.json"} {
		legacy := doReq(router, "GET", p, "", nil)
		namespaced := doReq(router, "GET", "/s/pescara"+p, "", nil)
		if legacy.Code != namespaced.Code {
			t.Errorf("%s: legacy %d vs namespaced %d", p, legacy.Code, namespaced.Code)
		}
		if legacy.Body.String() != namespaced.Body.String() {
			t.Errorf("%s: body mismatch\nlegacy:     %s\nnamespaced: %s", p, legacy.Body.String(), namespaced.Body.String())
		}
	}
}

func TestAuth_KeysAreNotInterchangeable(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()
	router := app.setupRouter()

	body, _ := json.Marshal(ToggleStatusRequest{})

	for _, tc := range []struct {
		name, path, key string
		wantCode        int
	}{
		{"pescara correct", "/s/pescara/toggle", pescaraKey, http.StatusOK},
		{"pescara wrong (bologna key)", "/s/pescara/toggle", bolognaKey, http.StatusUnauthorized},
		{"bologna correct", "/s/bologna/toggle", bolognaKey, http.StatusOK},
		{"bologna wrong (pescara key)", "/s/bologna/toggle", pescaraKey, http.StatusUnauthorized},
		{"missing key", "/s/pescara/toggle", "", http.StatusUnauthorized},
		{"legacy with default key", "/toggle", pescaraKey, http.StatusTooManyRequests}, // cooldown from earlier pescara toggle
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(router, "POST", tc.path, tc.key, body)
			if w.Code != tc.wantCode {
				t.Errorf("code %d want %d, body=%s", w.Code, tc.wantCode, w.Body.String())
			}
		})
	}
}

func TestToggleStatus_FlipsOnlyTargetSpace(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()
	router := app.setupRouter()

	body, _ := json.Marshal(ToggleStatusRequest{})
	w := doReq(router, "POST", "/s/pescara/toggle", pescaraKey, body)
	if w.Code != http.StatusOK {
		t.Fatalf("pescara toggle failed: %d %s", w.Code, w.Body.String())
	}

	pescara, err := app.repo.GetLatestStatus(context.Background(), app.spaces["pescara"].ID)
	if err != nil {
		t.Fatalf("get pescara: %v", err)
	}
	if !pescara.IsOpen {
		t.Error("pescara should be open after toggle")
	}

	if _, err := app.repo.GetLatestStatus(context.Background(), app.spaces["bologna"].ID); err == nil {
		t.Error("bologna should have no rows after pescara-only toggle")
	}
}

func TestToggleStatus_CooldownIsPerSpace(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()
	router := app.setupRouter()

	body, _ := json.Marshal(ToggleStatusRequest{})

	if w := doReq(router, "POST", "/s/pescara/toggle", pescaraKey, body); w.Code != http.StatusOK {
		t.Fatalf("first pescara toggle: %d", w.Code)
	}
	if w := doReq(router, "POST", "/s/pescara/toggle", pescaraKey, body); w.Code != http.StatusTooManyRequests {
		t.Errorf("second pescara toggle should 429, got %d", w.Code)
	}
	if w := doReq(router, "POST", "/s/bologna/toggle", bolognaKey, body); w.Code != http.StatusOK {
		t.Errorf("bologna toggle should not be rate-limited by pescara: %d", w.Code)
	}
}

func TestToggleStatus_InvalidJSON(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()
	router := app.setupRouter()

	w := httptest.NewRecorder()
	r, _ := http.NewRequest("POST", "/s/pescara/toggle", strings.NewReader("invalid"))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-API-KEY", pescaraKey)
	router.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestGetSpaceAPI_PerSpaceMetadata(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()
	router := app.setupRouter()

	testTime := time.Now().UTC().Truncate(time.Second)
	createTestStatusFor(t, app, app.spaces["pescara"].ID, true, testTime)

	w := doReq(router, "GET", "/s/pescara/spaceapi.json", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d", w.Code)
	}
	var resp SpaceAPIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Space != "Metro Olografix Pescara" {
		t.Errorf("space: %q", resp.Space)
	}
	if resp.Location["address"] != "Viale Marconi 278/1" {
		t.Errorf("address: %v", resp.Location["address"])
	}
	if !resp.State.Open {
		t.Error("expected open state")
	}
	if resp.State.LastChange != testTime.Unix() {
		t.Errorf("lastchange: got %d want %d", resp.State.LastChange, testTime.Unix())
	}
	if resp.Contact["email"] != "pescara@example.org" {
		t.Errorf("contact: %v", resp.Contact)
	}
	if len(resp.Links) != 1 || resp.Links[0].URL != "https://moca.camp" {
		t.Errorf("links: %+v", resp.Links)
	}

	w2 := doReq(router, "GET", "/s/bologna/spaceapi.json", "", nil)
	var resp2 SpaceAPIResponse
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.Space != "Metro Olografix Bologna" {
		t.Errorf("bologna space: %q", resp2.Space)
	}
	if resp2.State.Open {
		t.Error("bologna should not report open (no rows)")
	}
	if resp2.State.LastChange != 0 {
		t.Errorf("bologna lastchange: %d", resp2.State.LastChange)
	}
}

func TestGetStats_EmptySpace(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()
	router := app.setupRouter()

	w := doReq(router, "GET", "/s/bologna/stats", "", nil)
	if w.Code != http.StatusOK {
		t.Errorf("code %d", w.Code)
	}
	var resp []WeeklyStatsDetailed
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp) != 0 {
		t.Errorf("want empty, got %d", len(resp))
	}
	if body := strings.TrimSpace(w.Body.String()); body != "[]" {
		t.Errorf("want JSON body []; got %q (UI breaks on null)", body)
	}
}

func TestUtilityFunctions(t *testing.T) {
	t.Run("abortUnauthorized", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		abortUnauthorized(c)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("code %d", w.Code)
		}
	})

	t.Run("handleDatabaseError nil", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		if handleDatabaseError(c, nil) {
			t.Error("expected false for nil")
		}
	})

	t.Run("handleDatabaseError deadline", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		if !handleDatabaseError(c, context.DeadlineExceeded) {
			t.Error("expected true")
		}
		if w.Code != http.StatusGatewayTimeout {
			t.Errorf("code %d", w.Code)
		}
	})
}
