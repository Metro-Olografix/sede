package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/metro-olografix/sede/internal/config"
	"github.com/metro-olografix/sede/internal/database"
	"github.com/metro-olografix/sede/internal/notification"
	"github.com/ulule/limiter/v3"
	"github.com/ulule/limiter/v3/drivers/store/memory"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
)

type App struct {
	repo        *database.Repository
	config      config.Config
	validate    *validator.Validate
	limiter     *rate.Limiter
	apiKeyHash  []byte
	rateLimiter *limiter.Limiter
	telegram    *notification.Telegram
}

const (
	rateLimitRequests = 100
	rateLimitDuration = time.Minute
	bcryptCost        = 12
	shutdownTimeout   = 5 * time.Second
)

func NewApp(cfg config.Config) (*App, error) {
	app := &App{
		config:   cfg,
		validate: validator.New(),
		limiter:  rate.NewLimiter(rate.Every(rateLimitDuration/rateLimitRequests), rateLimitRequests),
	}

	if err := app.initSecurity(); err != nil {
		return nil, err
	}

	repo, err := database.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("database initialization failed: %w", err)
	}
	app.repo = repo

	app.rateLimiter = limiter.New(memory.NewStore(), limiter.Rate{
		Period: rateLimitDuration,
		Limit:  rateLimitRequests,
	})

	telegram, err := notification.NewTelegram(cfg)
	if err != nil {
		log.Printf("telegram notification not initialized: %s", err.Error())
	}
	app.telegram = telegram

	return app, nil
}

func (a *App) initSecurity() error {
	if a.config.HashAPIKey {
		hash, err := bcrypt.GenerateFromPassword([]byte(a.config.APIKey), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("failed to hash API key: %w", err)
		}
		a.apiKeyHash = hash
	}
	return nil
}

func (a *App) CreateServer() *http.Server {
	return &http.Server{
		Addr:              ":" + a.config.Port,
		Handler:           a.setupRouter(),
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func (a *App) Shutdown(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	if sqlDB, err := a.repo.Db.DB(); err == nil {
		sqlDB.Close()
	}
}
