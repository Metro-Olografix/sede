package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/metro-olografix/sede/internal/config"
)

func setupTestDB(t *testing.T) (*Repository, func()) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := config.Config{
		DatabasePath: dbPath,
		Debug:        false,
	}

	repo, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	cleanup := func() {
		if sqlDB, err := repo.Db.DB(); err == nil {
			sqlDB.Close()
		}
		os.Remove(dbPath)
	}

	return repo, cleanup
}

func TestNew(t *testing.T) {
	t.Run("successful database creation", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		cfg := config.Config{
			DatabasePath: dbPath,
			Debug:        true,
		}

		repo, err := New(cfg)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		if repo == nil {
			t.Fatal("Expected repository to be created")
		}

		if repo.Db == nil {
			t.Fatal("Expected database connection to be established")
		}

		// Clean up
		if sqlDB, err := repo.Db.DB(); err == nil {
			sqlDB.Close()
		}
	})

	t.Run("database creation with invalid path", func(t *testing.T) {
		cfg := config.Config{
			DatabasePath: "/invalid/path/test.db",
			Debug:        false,
		}

		_, err := New(cfg)
		if err == nil {
			t.Fatal("Expected error for invalid database path")
		}
	})
}

func TestCreateAndGetLatestStatus(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("create and retrieve status", func(t *testing.T) {
		testTime := time.Now().UTC()
		status := SedeStatus{
			IsOpen:    true,
			Timestamp: testTime,
		}

		err := repo.CreateStatus(ctx, status)
		if err != nil {
			t.Fatalf("Failed to create status: %v", err)
		}

		latest, err := repo.GetLatestStatus(ctx)
		if err != nil {
			t.Fatalf("Failed to get latest status: %v", err)
		}

		if latest.IsOpen != true {
			t.Errorf("Expected IsOpen to be true, got %v", latest.IsOpen)
		}

		if latest.Timestamp.Unix() != testTime.Unix() {
			t.Errorf("Expected timestamp %v, got %v", testTime, latest.Timestamp)
		}
	})

	t.Run("get latest from multiple statuses", func(t *testing.T) {
		// Create older status
		oldTime := time.Now().UTC().Add(-1 * time.Hour)
		oldStatus := SedeStatus{
			IsOpen:    false,
			Timestamp: oldTime,
		}
		repo.CreateStatus(ctx, oldStatus)

		// Create newer status
		newTime := time.Now().UTC()
		newStatus := SedeStatus{
			IsOpen:    true,
			Timestamp: newTime,
		}
		repo.CreateStatus(ctx, newStatus)

		latest, err := repo.GetLatestStatus(ctx)
		if err != nil {
			t.Fatalf("Failed to get latest status: %v", err)
		}

		if latest.IsOpen != true {
			t.Errorf("Expected latest status to be open, got %v", latest.IsOpen)
		}

		if latest.Timestamp.Unix() < newTime.Unix()-1 { // Allow 1 second tolerance
			t.Errorf("Expected latest timestamp to be recent, got %v", latest.Timestamp)
		}
	})
}

func TestGetStatistics(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("statistics with no data", func(t *testing.T) {
		stats, total, err := repo.GetStatistics(ctx)
		if err != nil {
			t.Fatalf("Failed to get statistics: %v", err)
		}

		if total != 0 {
			t.Errorf("Expected total changes to be 0, got %d", total)
		}

		if len(stats) != 0 {
			t.Errorf("Expected no daily stats, got %d", len(stats))
		}
	})

	t.Run("statistics with data", func(t *testing.T) {
		// Create some test data
		now := time.Now().UTC()
		statuses := []SedeStatus{
			{IsOpen: true, Timestamp: now.Add(-24 * time.Hour)},
			{IsOpen: false, Timestamp: now.Add(-23 * time.Hour)},
			{IsOpen: true, Timestamp: now.Add(-1 * time.Hour)},
		}

		for _, status := range statuses {
			err := repo.CreateStatus(ctx, status)
			if err != nil {
				t.Fatalf("Failed to create status: %v", err)
			}
		}

		stats, total, err := repo.GetStatistics(ctx)
		if err != nil {
			t.Fatalf("Failed to get statistics: %v", err)
		}

		if total < 3 {
			t.Errorf("Expected at least 3 total changes, got %d", total)
		}

		if len(stats) == 0 {
			t.Error("Expected some daily statistics")
		}

		// Verify daily stats structure
		for _, stat := range stats {
			if stat.Date == "" {
				t.Error("Expected date to be set")
			}
			if stat.Probability < 0 || stat.Probability > 1 {
				t.Errorf("Expected probability between 0 and 1, got %f", stat.Probability)
			}
		}
	})
}

func TestGetWeeklyStats(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("weekly stats with no data", func(t *testing.T) {
		stats, err := repo.GetWeeklyStats(ctx)
		if err != nil {
			t.Fatalf("Failed to get weekly stats: %v", err)
		}

		if len(stats) != 0 {
			t.Errorf("Expected no weekly stats, got %d", len(stats))
		}
	})

	t.Run("weekly stats with data", func(t *testing.T) {
		// Create test data for different days and hours
		now := time.Now().UTC()

		// Monday 10 AM - open
		monday10 := time.Date(now.Year(), now.Month(), now.Day()-int(now.Weekday())+1, 10, 0, 0, 0, time.UTC)
		// Monday 2 PM - closed
		monday14 := time.Date(now.Year(), now.Month(), now.Day()-int(now.Weekday())+1, 14, 0, 0, 0, time.UTC)
		// Tuesday 11 AM - open
		tuesday11 := time.Date(now.Year(), now.Month(), now.Day()-int(now.Weekday())+2, 11, 0, 0, 0, time.UTC)

		statuses := []SedeStatus{
			{IsOpen: true, Timestamp: monday10},
			{IsOpen: false, Timestamp: monday14},
			{IsOpen: true, Timestamp: tuesday11},
		}

		for _, status := range statuses {
			err := repo.CreateStatus(ctx, status)
			if err != nil {
				t.Fatalf("Failed to create status: %v", err)
			}
		}

		stats, err := repo.GetWeeklyStats(ctx)
		if err != nil {
			t.Fatalf("Failed to get weekly stats: %v", err)
		}

		// Should have some weekly stats
		if len(stats) == 0 {
			t.Error("Expected some weekly statistics")
		}

		// Verify structure
		for _, stat := range stats {
			if stat.Day == "" {
				t.Error("Expected day to be set")
			}
			if stat.DailyProbability < 0 || stat.DailyProbability > 1 {
				t.Errorf("Expected daily probability between 0 and 1, got %f", stat.DailyProbability)
			}

			for _, hourly := range stat.Hourly {
				if hourly.Hour == "" {
					t.Error("Expected hour to be set")
				}
				if hourly.Probability < 0 || hourly.Probability > 1 {
					t.Errorf("Expected hourly probability between 0 and 1, got %f", hourly.Probability)
				}
			}
		}
	})
}

func TestSedeStatus(t *testing.T) {
	t.Run("sede status creation", func(t *testing.T) {
		testTime := time.Now().UTC()
		status := SedeStatus{
			IsOpen:    true,
			Timestamp: testTime,
		}

		if status.IsOpen != true {
			t.Errorf("Expected IsOpen to be true, got %v", status.IsOpen)
		}

		if status.Timestamp != testTime {
			t.Errorf("Expected timestamp %v, got %v", testTime, status.Timestamp)
		}
	})
}

func TestDailyStats(t *testing.T) {
	t.Run("daily stats creation", func(t *testing.T) {
		stats := DailyStats{
			Date:        "2023-12-01",
			Probability: 0.75,
		}

		if stats.Date != "2023-12-01" {
			t.Errorf("Expected date '2023-12-01', got '%s'", stats.Date)
		}

		if stats.Probability != 0.75 {
			t.Errorf("Expected probability 0.75, got %f", stats.Probability)
		}
	})
}
