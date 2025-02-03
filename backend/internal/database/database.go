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
	"gorm.io/gorm/logger"
)

const (
	statsDateLayout = "2006-01-02"
	analysisDays    = 30
)

type Repository struct {
	Db *gorm.DB
}

type SedeStatus struct {
	ID        uint      `gorm:"primarykey"`
	IsOpen    bool      `gorm:"not null;index"`
	Timestamp time.Time `gorm:"not null;index"`
}

type DailyStats struct {
	Date        string  `json:"date" validate:"required,datetime=2006-01-02"`
	Probability float64 `json:"probability" validate:"required,min=0,max=1"`
}

// New types for weekly statistics
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

func (r *Repository) GetLatestStatus(ctx context.Context) (SedeStatus, error) {
	var status SedeStatus
	err := r.Db.WithContext(ctx).Order("timestamp desc").First(&status).Error
	return status, err
}

func (r *Repository) CreateStatus(ctx context.Context, status SedeStatus) error {
	return r.Db.WithContext(ctx).Create(&status).Error
}

func (r *Repository) GetStatistics(ctx context.Context) ([]DailyStats, int64, error) {
	var totalChanges int64
	var dailyStats []DailyStats

	err := r.Db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&SedeStatus{}).Count(&totalChanges).Error; err != nil {
			return err
		}

		return tx.Raw(
			`SELECT strftime(?, timestamp) as date, 
					COUNT(*) * 1.0 / ? as probability 
			 FROM sede_statuses 
			 WHERE timestamp >= date('now', ?) 
			 GROUP BY date 
			 ORDER BY date`,
			statsDateLayout,
			analysisDays,
			fmt.Sprintf("-%d days", analysisDays),
		).Scan(&dailyStats).Error
	})

	return dailyStats, totalChanges, err
}

// GetWeeklyStats fetches daily and hourly statistics merged by day.
func (r *Repository) GetWeeklyStats(ctx context.Context) ([]WeeklyStatsDetailed, error) {
	// Query overall daily probability
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
        WHERE timestamp >= date('now', '-90 days')
        GROUP BY day
        ORDER BY strftime('%w', timestamp)
    `).Scan(&dailyStats).Error
	if err != nil {
		return nil, err
	}

	// Query hourly breakdown for hours 9am to 9pm
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
        WHERE timestamp >= date('now', '-90 days')
          AND CAST(strftime('%H', timestamp) as integer) BETWEEN 9 AND 21
        GROUP BY day, hour
        ORDER BY strftime('%w', timestamp), hour
    `).Scan(&hourlyStats).Error
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
	return db.AutoMigrate(&SedeStatus{})
}
