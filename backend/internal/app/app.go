package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
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
	repo         *database.Repository
	config       config.Config
	validate     *validator.Validate
	limiter      *rate.Limiter
	rateLimiter  *limiter.Limiter
	telegram     *notification.Dispatcher
	spaces       map[string]*database.Space
	defaultSpace *database.Space
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
		spaces:   make(map[string]*database.Space),
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

	telegram, err := notification.NewDispatcher(cfg.TelegramToken)
	if err != nil {
		log.Printf("telegram notification not initialized: %s", err.Error())
	}
	app.telegram = telegram

	if err := app.loadAndSeedSpaces(); err != nil {
		return nil, fmt.Errorf("space bootstrap failed: %w", err)
	}

	return app, nil
}

// loadAndSeedSpaces reads spaces.yaml (or synthesises a single space from the
// legacy env vars when the file is missing), upserts every entry into the DB
// with a bcrypt-hashed API key, builds the hot lookup map, and backfills any
// legacy status rows carrying space_id = 0 onto the default space.
func (a *App) loadAndSeedSpaces() error {
	slug := a.config.DefaultSpaceSlug
	if slug == "" {
		slug = "pescara"
	}

	defs, err := config.LoadSpaces(a.config.SpacesConfigPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if a.config.APIKey == "" {
			return fmt.Errorf("no spaces config at %s and no legacy API_KEY to synthesise a default space", a.config.SpacesConfigPath)
		}
		legacy := config.LegacySpaceFromConfig(a.config)
		if legacy.Slug == "" {
			legacy.Slug = slug
		}
		defs = []config.SpaceDef{legacy}
		log.Printf("spaces config not found at %s; synthesising single space %q from legacy env vars", a.config.SpacesConfigPath, legacy.Slug)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, d := range defs {
		hash, err := bcrypt.GenerateFromPassword([]byte(d.APIKey), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash api key for space %q: %w", d.Slug, err)
		}
		projectsJSON, err := json.Marshal(d.Projects)
		if err != nil {
			return fmt.Errorf("encode projects for space %q: %w", d.Slug, err)
		}
		linksJSON, err := json.Marshal(d.Links)
		if err != nil {
			return fmt.Errorf("encode links for space %q: %w", d.Slug, err)
		}

		sp, err := a.repo.UpsertSpace(ctx, database.Space{
			Slug:           d.Slug,
			Name:           d.Name,
			Address:        d.Address,
			Lat:            d.Lat,
			Lon:            d.Lon,
			Timezone:       d.Timezone,
			LogoURL:        d.LogoURL,
			URL:            d.URL,
			ContactEmail:   d.ContactEmail,
			Message:        d.Message,
			APIKeyHash:     hash,
			TelegramChatID: d.TelegramChatID,
			TelegramThread: d.TelegramThread,
			Projects:       string(projectsJSON),
			Links:          string(linksJSON),
		})
		if err != nil {
			return fmt.Errorf("upsert space %q: %w", d.Slug, err)
		}
		a.spaces[sp.Slug] = sp
	}

	ds, ok := a.spaces[slug]
	if !ok {
		return fmt.Errorf("default space slug %q not found in loaded spaces", slug)
	}
	a.defaultSpace = ds

	n, err := a.repo.BackfillDefaultSpaceID(ctx, ds.ID)
	if err != nil {
		return fmt.Errorf("backfill legacy sede_statuses: %w", err)
	}
	if n > 0 {
		log.Printf("backfilled %d legacy sede_statuses rows onto space %q (id=%d)", n, ds.Slug, ds.ID)
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
