package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/secure"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/ulule/limiter/v3"
	"github.com/ulule/limiter/v3/drivers/store/memory"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	defaultPort       = "8080"
	businessHourStart = 9
	businessHourEnd   = 21
	statsDateFormat   = "2006-01-02"
	daysToAnalyze     = 30

	// Security constants
	rateLimitRequests = 100
	rateLimitDuration = time.Minute
	bcryptCost        = 12
	contextTimeout    = 30 * time.Second
	shutdownTimeout   = 5 * time.Second

	// Database connection settings
	dbConnMaxLifetime = 5 * time.Minute
	dbMaxOpenConns    = 25
	dbMaxIdleConns    = 5
)

var (
	// ErrInvalidInput represents validation errors
	ErrInvalidInput = errors.New("invalid input")
	// ErrUnauthorized represents authentication errors
	ErrUnauthorized = errors.New("unauthorized")
	// ErrDatabaseOperation represents database operation errors
	ErrDatabaseOperation = errors.New("database operation failed")
)

// SedeStatus represents the status of the sede at a given time
type SedeStatus struct {
	ID        uint      `gorm:"primarykey"`
	IsOpen    bool      `gorm:"not null;index"`
	Timestamp time.Time `gorm:"not null;index"`
}

// DailyStats represents daily statistics
type DailyStats struct {
	Date        string  `json:"date" validate:"required,datetime=2006-01-02"`
	Probability float64 `json:"probability" validate:"required,min=0,max=1"`
}

// Stats represents the complete statistics response
type Stats struct {
	TotalChanges int64        `json:"total_changes"`
	LastChange   time.Time    `json:"last_change"`
	CurrentState bool         `json:"current_state"`
	DailyChanges []DailyStats `json:"daily_changes"`
}

// Config holds the application configuration
type Config struct {
	APIKey         string
	Port           string
	Debug          bool
	AllowedOrigins []string
	HashAPIKey     bool
}

// App represents the application instance
type App struct {
	db          *gorm.DB
	config      Config
	validate    *validator.Validate
	limiter     *rate.Limiter
	apiKeyHash  []byte
	rateLimiter *limiter.Limiter
	store       limiter.Store
}

// NewApp creates a new application instance with security configurations
func NewApp(config Config) (*App, error) {
	// Validate configuration
	if err := validateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Hash API key if configured
	var apiKeyHash []byte
	var err error
	if config.HashAPIKey {
		apiKeyHash, err = bcrypt.GenerateFromPassword([]byte(config.APIKey), bcryptCost)
		if err != nil {
			return nil, fmt.Errorf("failed to hash API key: %w", err)
		}
	}

	// Configure GORM logger with security considerations
	gormLogger := logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold:             time.Second,
			LogLevel:                  logger.Silent,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)

	if config.Debug {
		gormLogger.LogMode(logger.Info)
	}

	// Initialize database with secure configuration
	db, err := gorm.Open(sqlite.Open("database/sede.db"), &gorm.Config{
		Logger:      gormLogger,
		QueryFields: true, // Prevent SQL injection by explicitly stating fields
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Configure rate limiter with IP-based limiting
	store := memory.NewStore()
	rateLimiter := limiter.New(store, limiter.Rate{
		Period: rateLimitDuration,
		Limit:  rateLimitRequests,
	})

	// Configure database with proper error handling
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get database instance: %w", err)
	}

	sqlDB.SetMaxOpenConns(dbMaxOpenConns)
	sqlDB.SetMaxIdleConns(dbMaxIdleConns)
	sqlDB.SetConnMaxLifetime(dbConnMaxLifetime)

	// Test database connection
	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Auto migrate the schema with default values for new columns
	if err := db.AutoMigrate(&SedeStatus{}); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return &App{
		db:          db,
		config:      config,
		validate:    validator.New(),
		limiter:     rate.NewLimiter(rate.Every(rateLimitDuration/rateLimitRequests), rateLimitRequests),
		apiKeyHash:  apiKeyHash,
		rateLimiter: rateLimiter,
		store:       store,
	}, nil
}

// setupRouter configures and returns the Gin router with security middleware
func (app *App) setupRouter() *gin.Engine {
	if !app.config.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()

	// Default allowed origins
	defaultOrigins := []string{}

	// CORS configuration
	corsConfig := cors.Config{
		AllowOrigins:     defaultOrigins,
		AllowMethods:     []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "X-API-KEY", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}

	// Add custom origins from config if they exist
	if len(app.config.AllowedOrigins) > 0 && app.config.AllowedOrigins[0] != "" {
		corsConfig.AllowOrigins = append(corsConfig.AllowOrigins, app.config.AllowedOrigins...)
	}

	// Security middleware
	r.Use(
		gin.Recovery(),
		app.secureMiddleware(),
		app.rateLimitMiddleware(),
		cors.New(corsConfig),
	)

	if app.config.Debug {
		r.Use(gin.Logger())
	}

	r.GET("/status", app.getStatus)
	r.GET("/stats", app.getStats)

	// Authenticated routes
	secured := r.Group("/")
	secured.Use(app.authMiddleware())
	{
		secured.POST("/toggle", app.toggleStatus)
	}

	return r
}

// Secure middleware configuration
func (app *App) secureMiddleware() gin.HandlerFunc {
	return secure.New(secure.Config{
		STSSeconds:           31536000,
		STSIncludeSubdomains: true,
		FrameDeny:            true,
		ContentTypeNosniff:   true,
		BrowserXssFilter:     true,
		IENoOpen:             true,
		ReferrerPolicy:       "strict-origin-when-cross-origin",
		SSLProxyHeaders:      map[string]string{"X-Forwarded-Proto": "https"},
	})
}

// Rate limiting middleware with IP-based limiting
func (app *App) rateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		ctx := c.Request.Context()

		limiterCtx, err := app.rateLimiter.Get(ctx, ip)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "rate limit error"})
			c.Abort()
			return
		}

		if limiterCtx.Reached {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// API key authentication middleware with timing attack prevention
func (app *App) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-API-KEY")
		if app.config.HashAPIKey {
			if err := bcrypt.CompareHashAndPassword(app.apiKeyHash, []byte(apiKey)); err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"error": ErrUnauthorized.Error()})
				c.Abort()
				return
			}
		} else {
			// Constant-time comparison to prevent timing attacks
			if subtle.ConstantTimeCompare([]byte(apiKey), []byte(app.config.APIKey)) != 1 {
				c.JSON(http.StatusUnauthorized, gin.H{"error": ErrUnauthorized.Error()})
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

// Handler functions with context timeout and input validation
func (app *App) getStatus(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), contextTimeout)
	defer cancel()

	var lastStatus SedeStatus
	result := app.db.WithContext(ctx).Order("timestamp desc").First(&lastStatus)

	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			c.String(http.StatusOK, "false")
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": ErrDatabaseOperation.Error()})
		return
	}

	c.String(http.StatusOK, fmt.Sprintf("%v", lastStatus.IsOpen))
}

func (app *App) toggleStatus(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), contextTimeout)
	defer cancel()

	var lastStatus SedeStatus
	result := app.db.WithContext(ctx).Order("timestamp desc").First(&lastStatus)

	newStatus := SedeStatus{
		IsOpen:    true,
		Timestamp: time.Now().UTC(), // Always use UTC
	}

	if result.Error == nil {
		newStatus.IsOpen = !lastStatus.IsOpen
	}

	if err := app.db.WithContext(ctx).Create(&newStatus).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": ErrDatabaseOperation.Error()})
		return
	}

	c.String(http.StatusOK, fmt.Sprintf("%v", newStatus.IsOpen))
}

func (app *App) getStats(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), contextTimeout)
	defer cancel()

	var stats Stats
	// Calculate total changes
	if err := app.db.WithContext(ctx).Model(&SedeStatus{}).Count(&stats.TotalChanges).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": ErrDatabaseOperation.Error()})
		return
	}

	// Get last change
	var lastStatus SedeStatus
	if err := app.db.WithContext(ctx).Order("timestamp desc").First(&lastStatus).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusOK, stats)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": ErrDatabaseOperation.Error()})
		return
	}
	stats.LastChange = lastStatus.Timestamp
	stats.CurrentState = lastStatus.IsOpen

	// Calculate daily changes
	var dailyChanges []DailyStats
	if err := app.db.WithContext(ctx).Raw(`
		SELECT 
			strftime(?, timestamp) as date, 
			COUNT(*) * 1.0 / ? as probability 
		FROM sede_status 
		WHERE timestamp >= date('now', ?) 
		GROUP BY date 
		ORDER BY date
	`, statsDateFormat, daysToAnalyze, fmt.Sprintf("-%d days", daysToAnalyze)).Scan(&dailyChanges).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": ErrDatabaseOperation.Error()})
		return
	}
	stats.DailyChanges = dailyChanges

	c.JSON(http.StatusOK, stats)
}

func validateConfig(config Config) error {
	if config.Port == "" {
		return fmt.Errorf("port is required")
	}
	if config.APIKey == "" {
		return fmt.Errorf("API key is required")
	}
	return nil
}

// getEnvOrDefault retrieves the value of the environment variable named by the key.
// If the variable is not present, it returns the default value.
func getEnvOrDefault(key, defaultValue string) string {
	value, exists := os.LookupEnv(key)
	if !exists {
		return defaultValue
	}
	return value
}

func main() {
	// Initialize configuration with secure defaults
	config := Config{
		APIKey: getEnvOrDefault("API_KEY", "change-me"),
		Port:   getEnvOrDefault("PORT", defaultPort),
		Debug:  getEnvOrDefault("DEBUG", "false") == "true",
		// Split only if not empty
		AllowedOrigins: func() []string {
			origins := getEnvOrDefault("ALLOWED_ORIGINS", "")
			if origins == "" {
				return nil
			}
			return strings.Split(origins, ",")
		}(),
		HashAPIKey: getEnvOrDefault("HASH_API_KEY", "true") == "true",
	}

	// Create application instance
	app, err := NewApp(config)
	if err != nil {
		log.Fatalf("Failed to initialize application: %v", err)
	}

	// Setup router
	router := app.setupRouter()

	// Create server with timeouts
	srv := &http.Server{
		Addr:              ":" + config.Port,
		Handler:           router,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB
	}

	// Graceful shutdown setup
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		<-quit
		log.Println("Shutting down server...")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		// Close database connection with timeout
		if sqlDB, err := app.db.DB(); err == nil {
			if err := sqlDB.Close(); err != nil {
				log.Printf("Error closing database connection: %v", err)
			}
		}

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("Server forced to shutdown: %v", err)
		}
	}()

	// Start server
	log.Printf("Server is starting on port %s", config.Port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Failed to start server: %v", err)
	}

	wg.Wait()
	log.Println("Server stopped gracefully")
}
