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

	"github.com/gin-contrib/secure"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
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
	maxRequestSize    = 1024 * 1024 // 1MB
	rateLimitRequests = 100
	rateLimitDuration = time.Minute
	bcryptCost        = 12
	contextTimeout    = 30 * time.Second
	shutdownTimeout   = 5 * time.Second
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
	db         *gorm.DB
	config     Config
	validate   *validator.Validate
	limiter    *rate.Limiter
	apiKeyHash []byte
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

	// Set connection pool settings
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to configure database: %w", err)
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	// Auto migrate the schema with default values for new columns
	if err := db.AutoMigrate(&SedeStatus{}); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return &App{
		db:         db,
		config:     config,
		validate:   validator.New(),
		limiter:    rate.NewLimiter(rate.Every(rateLimitDuration/rateLimitRequests), rateLimitRequests),
		apiKeyHash: apiKeyHash,
	}, nil
}

// setupRouter configures and returns the Gin router with security middleware
func (app *App) setupRouter() *gin.Engine {
	if !app.config.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()

	// Security middleware
	r.Use(
		gin.Recovery(),
		app.secureMiddleware(),
		app.rateLimitMiddleware(),
		app.corsMiddleware(),
		app.maxBodyMiddleware(maxRequestSize),
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

// Rate limiting middleware
func (app *App) rateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !app.limiter.Allow() {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// CORS middleware with secure configuration
func (app *App) corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if app.isAllowedOrigin(origin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, X-API-KEY")
			c.Header("Access-Control-Max-Age", "86400")
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// Maximum body size middleware
func (app *App) maxBodyMiddleware(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
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

// Helper functions with security considerations
func (app *App) isAllowedOrigin(origin string) bool {
	if len(app.config.AllowedOrigins) == 0 {
		return false
	}

	for _, allowed := range app.config.AllowedOrigins {
		if allowed == "*" {
			return true
		}
		if strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return false
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
		APIKey:         getEnvOrDefault("API_KEY", "change-me"),
		Port:           getEnvOrDefault("PORT", defaultPort),
		Debug:          getEnvOrDefault("DEBUG", "false") == "true",
		AllowedOrigins: strings.Split(getEnvOrDefault("ALLOWED_ORIGINS", ""), ","),
		HashAPIKey:     getEnvOrDefault("HASH_API_KEY", "true") == "true",
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

		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		// Close database connection
		if sqlDB, err := app.db.DB(); err == nil {
			sqlDB.Close()
		}

		if err := srv.Shutdown(ctx); err != nil {
			log.Fatalf("Server forced to shutdown: %v", err)
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
