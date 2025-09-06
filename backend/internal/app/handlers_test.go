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

func setupTestApp(t *testing.T) (*App, func()) {
	// Set gin to test mode
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := config.Config{
		Port:         "8080",
		APIKey:       "test-api-key-123456",
		Debug:        true,
		DatabasePath: dbPath,
		HashAPIKey:   false,
	}

	app, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("Failed to create test app: %v", err)
	}

	cleanup := func() {
		if sqlDB, err := app.repo.Db.DB(); err == nil {
			sqlDB.Close()
		}
		os.Remove(dbPath)
	}

	return app, cleanup
}

func createTestStatus(t *testing.T, app *App, isOpen bool, timestamp time.Time) {
	status := database.SedeStatus{
		IsOpen:    isOpen,
		Timestamp: timestamp,
	}

	err := app.repo.CreateStatus(context.Background(), status)
	if err != nil {
		t.Fatalf("Failed to create test status: %v", err)
	}
}

func TestGetStatus(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()

	router := app.setupRouter()

	t.Run("get status when no status exists", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/status", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("Expected status code %d, got %d", http.StatusInternalServerError, w.Code)
		}
	})

	t.Run("get status when status exists - open", func(t *testing.T) {
		createTestStatus(t, app, true, time.Now().UTC())

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/status", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
		}

		if w.Body.String() != "true" {
			t.Errorf("Expected body 'true', got '%s'", w.Body.String())
		}
	})

	t.Run("get status when status exists - closed", func(t *testing.T) {
		createTestStatus(t, app, false, time.Now().UTC())

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/status", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
		}

		if w.Body.String() != "false" {
			t.Errorf("Expected body 'false', got '%s'", w.Body.String())
		}
	})
}

func TestToggleStatus(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()

	router := app.setupRouter()

	t.Run("toggle without authentication", func(t *testing.T) {
		reqBody := ToggleStatusRequest{
			CardID: "test-card",
			Hash:   "test-hash",
		}
		jsonBody, _ := json.Marshal(reqBody)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/toggle", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status code %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})

	t.Run("toggle with invalid API key", func(t *testing.T) {
		reqBody := ToggleStatusRequest{
			CardID: "test-card",
			Hash:   "test-hash",
		}
		jsonBody, _ := json.Marshal(reqBody)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/toggle", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-KEY", "invalid-key")
		router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status code %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})

	t.Run("toggle with valid API key - no existing status", func(t *testing.T) {
		reqBody := ToggleStatusRequest{}
		jsonBody, _ := json.Marshal(reqBody)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/toggle", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-KEY", "test-api-key-123456")
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
		}

		var response map[string]bool
		json.Unmarshal(w.Body.Bytes(), &response)

		if !response["isOpen"] {
			t.Error("Expected status to be toggled to open (true)")
		}
	})

	t.Run("toggle with cooldown period active", func(t *testing.T) {
		// Create a recent status (within cooldown period)
		createTestStatus(t, app, true, time.Now().UTC().Add(-30*time.Second))

		reqBody := ToggleStatusRequest{}
		jsonBody, _ := json.Marshal(reqBody)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/toggle", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-KEY", "test-api-key-123456")
		router.ServeHTTP(w, req)

		if w.Code != http.StatusTooManyRequests {
			t.Errorf("Expected status code %d, got %d", http.StatusTooManyRequests, w.Code)
		}
	})

	t.Run("toggle with invalid JSON", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/toggle", strings.NewReader("invalid json"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-KEY", "test-api-key-123456")
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status code %d, got %d", http.StatusBadRequest, w.Code)
		}
	})
}

func TestGetStats(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()

	router := app.setupRouter()

	t.Run("get stats with no data", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/stats", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
		}

		var response []WeeklyStatsDetailed
		err := json.Unmarshal(w.Body.Bytes(), &response)
		if err != nil {
			t.Fatalf("Failed to unmarshal response: %v", err)
		}

		if len(response) != 0 {
			t.Errorf("Expected empty response, got %d items", len(response))
		}
	})

	t.Run("get stats with data", func(t *testing.T) {
		// Create some test data
		now := time.Now().UTC()
		createTestStatus(t, app, true, now.Add(-24*time.Hour))
		createTestStatus(t, app, false, now.Add(-12*time.Hour))

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/stats", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
		}

		var response []WeeklyStatsDetailed
		err := json.Unmarshal(w.Body.Bytes(), &response)
		if err != nil {
			t.Fatalf("Failed to unmarshal response: %v", err)
		}

		// Should have some data now
		for _, stat := range response {
			if stat.Day == "" {
				t.Error("Expected day to be set")
			}
			if stat.DailyProbability < 0 || stat.DailyProbability > 1 {
				t.Errorf("Expected daily probability between 0 and 1, got %f", stat.DailyProbability)
			}
		}
	})
}

func TestGetSpaceAPI(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()

	router := app.setupRouter()

	t.Run("get spaceapi with no status", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/spaceapi.json", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
		}

		var response SpaceAPIResponse
		err := json.Unmarshal(w.Body.Bytes(), &response)
		if err != nil {
			t.Fatalf("Failed to unmarshal response: %v", err)
		}

		// Verify structure
		if response.Space != "Metro Olografix" {
			t.Errorf("Expected space name 'Metro Olografix', got '%s'", response.Space)
		}

		if response.State.Open != false {
			t.Errorf("Expected open state false, got %v", response.State.Open)
		}

		if response.State.LastChange != 0 {
			t.Errorf("Expected last change 0, got %d", response.State.LastChange)
		}

		// Verify CORS headers
		if w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Error("Expected CORS header to allow all origins")
		}

		if w.Header().Get("Cache-Control") != "no-cache, must-revalidate" {
			t.Error("Expected no-cache header")
		}
	})

	t.Run("get spaceapi with status", func(t *testing.T) {
		testTime := time.Now().UTC()
		createTestStatus(t, app, true, testTime)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/spaceapi.json", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
		}

		var response SpaceAPIResponse
		err := json.Unmarshal(w.Body.Bytes(), &response)
		if err != nil {
			t.Fatalf("Failed to unmarshal response: %v", err)
		}

		if response.State.Open != true {
			t.Errorf("Expected open state true, got %v", response.State.Open)
		}

		if response.State.LastChange != testTime.Unix() {
			t.Errorf("Expected last change %d, got %d", testTime.Unix(), response.State.LastChange)
		}
	})
}

func TestAuthMiddleware(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()

	t.Run("no api key", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("POST", "/test", nil)

		middleware := app.authMiddleware()
		middleware(c)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status code %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})

	t.Run("valid api key", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("POST", "/test", nil)
		c.Request.Header.Set("X-API-KEY", "test-api-key-123456")

		middleware := app.authMiddleware()
		middleware(c)

		// Should not abort (no status set)
		if c.IsAborted() {
			t.Error("Expected request not to be aborted with valid API key")
		}
	})

	t.Run("invalid api key", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("POST", "/test", nil)
		c.Request.Header.Set("X-API-KEY", "invalid-key")

		middleware := app.authMiddleware()
		middleware(c)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status code %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})
}

func TestHashedAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := config.Config{
		Port:         "8080",
		APIKey:       "test-api-key-123456",
		Debug:        true,
		DatabasePath: dbPath,
		HashAPIKey:   true, // Enable hashing
	}

	app, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("Failed to create test app: %v", err)
	}
	defer func() {
		if sqlDB, err := app.repo.Db.DB(); err == nil {
			sqlDB.Close()
		}
	}()

	t.Run("hashed api key authentication", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("POST", "/test", nil)
		c.Request.Header.Set("X-API-KEY", "test-api-key-123456")

		middleware := app.authMiddleware()
		middleware(c)

		// Should not abort with correct key
		if c.IsAborted() {
			t.Error("Expected request not to be aborted with valid hashed API key")
		}
	})

	t.Run("hashed api key with wrong key", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("POST", "/test", nil)
		c.Request.Header.Set("X-API-KEY", "wrong-key")

		middleware := app.authMiddleware()
		middleware(c)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status code %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})
}

func TestUtilityFunctions(t *testing.T) {
	t.Run("abortUnauthorized", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		abortUnauthorized(c)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status code %d, got %d", http.StatusUnauthorized, w.Code)
		}

		var response map[string]string
		json.Unmarshal(w.Body.Bytes(), &response)

		if response["error"] != "Invalid or missing API key" {
			t.Errorf("Expected error message, got '%s'", response["error"])
		}
	})

	t.Run("handleDatabaseError with nil error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		result := handleDatabaseError(c, nil)

		if result != false {
			t.Error("Expected handleDatabaseError to return false for nil error")
		}

		if c.IsAborted() {
			t.Error("Expected request not to be aborted for nil error")
		}
	})

	t.Run("handleDatabaseError with context deadline exceeded", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		result := handleDatabaseError(c, context.DeadlineExceeded)

		if result != true {
			t.Error("Expected handleDatabaseError to return true for error")
		}

		if w.Code != http.StatusGatewayTimeout {
			t.Errorf("Expected status code %d, got %d", http.StatusGatewayTimeout, w.Code)
		}
	})
}
