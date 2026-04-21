package database

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/metro-olografix/sede/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const (
	statsDateLayout = "2006-01-02"
	analysisDays    = 30
)

type Repository struct {
	Db *gorm.DB
}

// Space is one physical association location served by this instance.
// The API key is stored as a bcrypt hash; per-space Telegram chat and thread
// IDs route notifications without a global bot configuration. Projects and
// Links hold JSON-encoded arrays used by the per-space SpaceAPI response.
type Space struct {
	ID             uint   `gorm:"primarykey"`
	Slug           string `gorm:"uniqueIndex;not null"`
	Name           string `gorm:"not null"`
	Address        string
	Lat            float64
	Lon            float64
	Timezone       string
	LogoURL        string
	URL            string
	ContactEmail   string
	Message        string
	APIKeyHash     []byte `gorm:"not null"`
	TelegramChatID int64
	TelegramThread int
	Projects       string
	Links          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SedeStatus is an open/closed event for a specific space. `default:0` on
// SpaceID exists solely so that adding the column via SQLite ALTER TABLE on
// an existing single-space DB succeeds; new rows always set SpaceID
// explicitly, and boot-time backfill rewrites any legacy zeros.
type SedeStatus struct {
	ID        uint      `gorm:"primarykey"`
	SpaceID   uint      `gorm:"not null;default:0;index:idx_space_timestamp,priority:1"`
	IsOpen    bool      `gorm:"not null"`
	Timestamp time.Time `gorm:"not null;index:idx_space_timestamp,priority:2"`
}

type DailyStats struct {
	Date        string  `json:"date" validate:"required,datetime=2006-01-02"`
	Probability float64 `json:"probability" validate:"required,min=0,max=1"`
}

type HourlyStat struct {
	Hour        string  `json:"hour"`
	Probability float64 `json:"probability"`
}

type WeeklyStatsDetailed struct {
	Day              string       `json:"day"`
	DailyProbability float64      `json:"dailyProbability"`
	Hourly           []HourlyStat `json:"hourly"`
}

func New(cfg config.Config) (*Repository, error) {
	gormConfig := &gorm.Config{
		Logger:         createLogger(cfg.Debug),
		PrepareStmt:    true,
		TranslateError: true,
	}

	db, err := gorm.Open(sqlite.Open(cfg.DatabasePath), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("database connection failed: %w", err)
	}

	if err := configureConnectionPool(db); err != nil {
		return nil, err
	}

	if err := migrateSchema(db); err != nil {
		return nil, err
	}

	return &Repository{Db: db}, nil
}

func (r *Repository) GetLatestStatus(ctx context.Context, spaceID uint) (SedeStatus, error) {
	var status SedeStatus
	err := r.Db.WithContext(ctx).
		Where("space_id = ?", spaceID).
		Order("timestamp desc").
		First(&status).Error
	return status, err
}

func (r *Repository) CreateStatus(ctx context.Context, status SedeStatus) error {
	return r.Db.WithContext(ctx).Create(&status).Error
}

func (r *Repository) GetStatistics(ctx context.Context, spaceID uint) ([]DailyStats, int64, error) {
	var totalChanges int64
	var dailyStats []DailyStats

	err := r.Db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&SedeStatus{}).Where("space_id = ?", spaceID).Count(&totalChanges).Error; err != nil {
			return err
		}

		return tx.Raw(
			`SELECT strftime(?, timestamp) as date,
					COUNT(*) * 1.0 / ? as probability
			 FROM sede_statuses
			 WHERE space_id = ?
			   AND timestamp >= date('now', ?)
			 GROUP BY date
			 ORDER BY date`,
			statsDateLayout,
			analysisDays,
			spaceID,
			fmt.Sprintf("-%d days", analysisDays),
		).Scan(&dailyStats).Error
	})

	return dailyStats, totalChanges, err
}

// GetWeeklyStats fetches daily and hourly probabilities for the given space
// over the last 90 days, grouped by weekday and (9-21 UTC) hour.
func (r *Repository) GetWeeklyStats(ctx context.Context, spaceID uint) ([]WeeklyStatsDetailed, error) {
	var dailyStats []struct {
		Day              string  `json:"day"`
		DailyProbability float64 `json:"dailyProbability"`
	}
	err := r.Db.WithContext(ctx).Raw(`
        SELECT
            CASE strftime('%w', timestamp)
                WHEN '0' THEN 'Sunday'
                WHEN '1' THEN 'Monday'
                WHEN '2' THEN 'Tuesday'
                WHEN '3' THEN 'Wednesday'
                WHEN '4' THEN 'Thursday'
                WHEN '5' THEN 'Friday'
                ELSE 'Saturday' END as day,
            AVG(CASE WHEN is_open THEN 1.0 ELSE 0.0 END) as dailyProbability
        FROM sede_statuses
        WHERE space_id = ?
          AND timestamp >= date('now', '-90 days')
        GROUP BY day
        ORDER BY strftime('%w', timestamp)
    `, spaceID).Scan(&dailyStats).Error
	if err != nil {
		return nil, err
	}

	var hourlyStats []struct {
		Day         string  `json:"day"`
		Hour        string  `json:"hour"`
		Probability float64 `json:"probability"`
	}
	err = r.Db.WithContext(ctx).Raw(`
        SELECT
            CASE strftime('%w', timestamp)
                WHEN '0' THEN 'Sunday'
                WHEN '1' THEN 'Monday'
                WHEN '2' THEN 'Tuesday'
                WHEN '3' THEN 'Wednesday'
                WHEN '4' THEN 'Thursday'
                WHEN '5' THEN 'Friday'
                ELSE 'Saturday' END as day,
            strftime('%H', timestamp) as hour,
            AVG(CASE WHEN is_open THEN 1.0 ELSE 0.0 END) as probability
        FROM sede_statuses
        WHERE space_id = ?
          AND timestamp >= date('now', '-90 days')
          AND CAST(strftime('%H', timestamp) as integer) BETWEEN 9 AND 21
        GROUP BY day, hour
        ORDER BY strftime('%w', timestamp), hour
    `, spaceID).Scan(&hourlyStats).Error
	if err != nil {
		return nil, err
	}

	weeklyMap := make(map[string]*WeeklyStatsDetailed)
	for _, ds := range dailyStats {
		weeklyMap[ds.Day] = &WeeklyStatsDetailed{
			Day:              ds.Day,
			DailyProbability: ds.DailyProbability,
			Hourly:           []HourlyStat{},
		}
	}
	for _, hs := range hourlyStats {
		if stat, ok := weeklyMap[hs.Day]; ok {
			stat.Hourly = append(stat.Hourly, HourlyStat{
				Hour:        hs.Hour,
				Probability: hs.Probability,
			})
		} else {
			weeklyMap[hs.Day] = &WeeklyStatsDetailed{
				Day:    hs.Day,
				Hourly: []HourlyStat{{Hour: hs.Hour, Probability: hs.Probability}},
			}
		}
	}

	daysOrder := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	var result []WeeklyStatsDetailed
	for _, day := range daysOrder {
		if stat, exists := weeklyMap[day]; exists {
			result = append(result, *stat)
		}
	}
	return result, nil
}

// GetSpaceBySlug returns the space with the given slug, or
// gorm.ErrRecordNotFound if none exists.
func (r *Repository) GetSpaceBySlug(ctx context.Context, slug string) (*Space, error) {
	var sp Space
	if err := r.Db.WithContext(ctx).Where("slug = ?", slug).First(&sp).Error; err != nil {
		return nil, err
	}
	return &sp, nil
}

// ListSpaces returns every space in the database, ordered by ID.
func (r *Repository) ListSpaces(ctx context.Context) ([]Space, error) {
	var spaces []Space
	err := r.Db.WithContext(ctx).Order("id asc").Find(&spaces).Error
	return spaces, err
}

// UpsertSpace inserts s if its slug is new, otherwise updates every
// mutable column on the existing row. Returns the persisted row including
// its assigned ID so callers can cache it.
func (r *Repository) UpsertSpace(ctx context.Context, s Space) (*Space, error) {
	err := r.Db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "slug"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"name", "address", "lat", "lon", "timezone",
			"logo_url", "url", "contact_email", "message",
			"api_key_hash", "telegram_chat_id", "telegram_thread",
			"projects", "links", "updated_at",
		}),
	}).Create(&s).Error
	if err != nil {
		return nil, err
	}
	// OnConflict's DoUpdates path does not populate s.ID reliably across
	// drivers, so re-read by slug to guarantee we return the persisted row.
	return r.GetSpaceBySlug(ctx, s.Slug)
}

// BackfillDefaultSpaceID rewrites any sede_statuses rows still carrying the
// migration default (space_id = 0) to point at spaceID. Returns the number
// of rows updated so the caller can log it.
func (r *Repository) BackfillDefaultSpaceID(ctx context.Context, spaceID uint) (int64, error) {
	if spaceID == 0 {
		return 0, fmt.Errorf("refusing to backfill to space_id = 0")
	}
	res := r.Db.WithContext(ctx).
		Model(&SedeStatus{}).
		Where("space_id = ?", 0).
		Update("space_id", spaceID)
	return res.RowsAffected, res.Error
}

func createLogger(debug bool) logger.Interface {
	logLevel := logger.Silent
	if debug {
		logLevel = logger.Info
	}

	return logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold:             time.Second,
			LogLevel:                  logLevel,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)
}

func configureConnectionPool(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get database instance failed: %w", err)
	}

	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return sqlDB.PingContext(ctx)
}

func migrateSchema(db *gorm.DB) error {
	return db.AutoMigrate(&Space{}, &SedeStatus{})
}
