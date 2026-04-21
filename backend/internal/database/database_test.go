package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/metro-olografix/sede/internal/config"
	"gorm.io/gorm"
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

// seedSpace persists a space with the given slug and returns its ID. Tests
// that insert SedeStatus rows need a space ID so the scoped queries have
// something to find.
func seedSpace(t *testing.T, repo *Repository, slug string) uint {
	t.Helper()
	sp, err := repo.UpsertSpace(context.Background(), Space{
		Slug:       slug,
		Name:       slug,
		APIKeyHash: []byte("fake-hash-for-tests"),
	})
	if err != nil {
		t.Fatalf("seed space %q: %v", slug, err)
	}
	return sp.ID
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

func TestMigrateSchema_CreatesSpacesTable(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	if !repo.Db.Migrator().HasTable(&Space{}) {
		t.Fatal("expected spaces table to exist after migrate")
	}
	if !repo.Db.Migrator().HasIndex(&Space{}, "Slug") {
		t.Error("expected unique index on Space.Slug")
	}
}

func TestMigrateSchema_AddsSpaceIDColumn(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	if !repo.Db.Migrator().HasColumn(&SedeStatus{}, "SpaceID") {
		t.Fatal("expected sede_statuses.space_id column")
	}
	if !repo.Db.Migrator().HasIndex(&SedeStatus{}, "idx_space_timestamp") {
		t.Error("expected composite index idx_space_timestamp on sede_statuses")
	}
}

func TestUpsertSpace_InsertAndUpdate(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	first, err := repo.UpsertSpace(ctx, Space{
		Slug:           "pescara",
		Name:           "Pescara Orig",
		APIKeyHash:     []byte("hash-v1"),
		TelegramChatID: -1,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if first.ID == 0 {
		t.Fatal("expected assigned ID after insert")
	}
	if first.CreatedAt.IsZero() {
		t.Error("expected CreatedAt populated")
	}

	// Upsert again with the same slug — same row, updated fields.
	second, err := repo.UpsertSpace(ctx, Space{
		Slug:           "pescara",
		Name:           "Pescara Renamed",
		APIKeyHash:     []byte("hash-v2"),
		TelegramChatID: -2,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("expected same row ID after upsert, got %d vs %d", first.ID, second.ID)
	}
	if second.Name != "Pescara Renamed" || string(second.APIKeyHash) != "hash-v2" || second.TelegramChatID != -2 {
		t.Errorf("mutable fields not updated: %+v", second)
	}

	var count int64
	repo.Db.Model(&Space{}).Count(&count)
	if count != 1 {
		t.Errorf("expected 1 row after upsert-twice, got %d", count)
	}
}

func TestGetSpaceBySlug(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	seedSpace(t, repo, "pescara")

	sp, err := repo.GetSpaceBySlug(ctx, "pescara")
	if err != nil {
		t.Fatalf("hit: %v", err)
	}
	if sp == nil || sp.Slug != "pescara" {
		t.Errorf("unexpected: %+v", sp)
	}

	_, err = repo.GetSpaceBySlug(ctx, "does-not-exist")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("miss should be ErrRecordNotFound, got %v", err)
	}
}

func TestGetLatestStatus_ScopedPerSpace(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	a := seedSpace(t, repo, "spaceA")
	b := seedSpace(t, repo, "spaceB")

	base := time.Now().UTC()
	mustCreate := func(spaceID uint, open bool, offset time.Duration) {
		if err := repo.CreateStatus(ctx, SedeStatus{SpaceID: spaceID, IsOpen: open, Timestamp: base.Add(offset)}); err != nil {
			t.Fatal(err)
		}
	}

	// spaceA: closed@-2h, open@-1h (latest open)
	mustCreate(a, false, -2*time.Hour)
	mustCreate(a, true, -1*time.Hour)
	// spaceB: open@-3h, closed@-30m (latest closed — and more recent than A)
	mustCreate(b, true, -3*time.Hour)
	mustCreate(b, false, -30*time.Minute)

	latestA, err := repo.GetLatestStatus(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if latestA.IsOpen != true || latestA.SpaceID != a {
		t.Errorf("A latest wrong: %+v", latestA)
	}

	latestB, err := repo.GetLatestStatus(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if latestB.IsOpen != false || latestB.SpaceID != b {
		t.Errorf("B latest wrong: %+v", latestB)
	}
}

func TestGetLatestStatus_EmptySpace(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	id := seedSpace(t, repo, "empty")
	_, err := repo.GetLatestStatus(ctx, id)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected ErrRecordNotFound, got %v", err)
	}
}

func TestGetWeeklyStats_ScopedPerSpace(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	a := seedSpace(t, repo, "spaceA")
	b := seedSpace(t, repo, "spaceB")

	// Seed only spaceA. spaceB stays empty.
	now := time.Now().UTC()
	monday10 := time.Date(now.Year(), now.Month(), now.Day()-int(now.Weekday())+1, 10, 0, 0, 0, time.UTC)
	for _, s := range []SedeStatus{
		{SpaceID: a, IsOpen: true, Timestamp: monday10},
		{SpaceID: a, IsOpen: false, Timestamp: monday10.Add(4 * time.Hour)},
	} {
		if err := repo.CreateStatus(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	statsA, err := repo.GetWeeklyStats(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(statsA) == 0 {
		t.Error("expected non-empty stats for spaceA")
	}

	statsB, err := repo.GetWeeklyStats(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(statsB) != 0 {
		t.Errorf("expected empty stats for spaceB (no rows), got %d", len(statsB))
	}
}

func TestGetStatistics_ScopedPerSpace(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	a := seedSpace(t, repo, "spaceA")
	b := seedSpace(t, repo, "spaceB")

	now := time.Now().UTC()
	for _, s := range []SedeStatus{
		{SpaceID: a, IsOpen: true, Timestamp: now.Add(-24 * time.Hour)},
		{SpaceID: a, IsOpen: false, Timestamp: now.Add(-23 * time.Hour)},
		{SpaceID: a, IsOpen: true, Timestamp: now.Add(-1 * time.Hour)},
		{SpaceID: b, IsOpen: true, Timestamp: now.Add(-1 * time.Hour)},
	} {
		if err := repo.CreateStatus(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	_, totalA, err := repo.GetStatistics(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if totalA != 3 {
		t.Errorf("spaceA totalChanges: want 3, got %d", totalA)
	}

	_, totalB, err := repo.GetStatistics(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if totalB != 1 {
		t.Errorf("spaceB totalChanges: want 1, got %d", totalB)
	}
}

func TestBackfillDefaultSpaceID(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	defaultID := seedSpace(t, repo, "default")

	// Simulate a legacy DB: rows inserted with SpaceID=0 (the migration
	// default), predating the multi-space refactor.
	now := time.Now().UTC()
	for _, ts := range []time.Duration{-3 * time.Hour, -2 * time.Hour, -1 * time.Hour} {
		if err := repo.Db.Create(&SedeStatus{SpaceID: 0, IsOpen: true, Timestamp: now.Add(ts)}).Error; err != nil {
			t.Fatal(err)
		}
	}

	updated, err := repo.BackfillDefaultSpaceID(ctx, defaultID)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 3 {
		t.Errorf("expected 3 rows backfilled, got %d", updated)
	}

	var remaining int64
	repo.Db.Model(&SedeStatus{}).Where("space_id = 0").Count(&remaining)
	if remaining != 0 {
		t.Errorf("expected 0 orphan rows, got %d", remaining)
	}

	var migrated int64
	repo.Db.Model(&SedeStatus{}).Where("space_id = ?", defaultID).Count(&migrated)
	if migrated != 3 {
		t.Errorf("expected 3 rows on default space, got %d", migrated)
	}
}

func TestBackfillDefaultSpaceID_RefusesZero(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := repo.BackfillDefaultSpaceID(context.Background(), 0)
	if err == nil {
		t.Fatal("expected refusal to backfill into space_id=0")
	}
}

func TestSedeStatus(t *testing.T) {
	t.Run("sede status creation", func(t *testing.T) {
		testTime := time.Now().UTC()
		status := SedeStatus{
			SpaceID:   1,
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
