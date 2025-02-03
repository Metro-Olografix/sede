package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/metro-olografix/sede/internal/app"
	"github.com/metro-olografix/sede/internal/config"
)

const (
	defaultPort = "8080"
)

func main() {
	var cfg config.Config

	flag.StringVar(&cfg.Port, "port", getEnvOrDefault("PORT", defaultPort), "Server port")
	flag.StringVar(&cfg.APIKey, "api-key", getEnvOrDefault("API_KEY", "change-me"), "API key for authentication")
	flag.BoolVar(&cfg.Debug, "debug", getEnvAsBool("DEBUG", false), "Enable debug mode")
	flag.StringVar(&cfg.AllowedOriginsStr, "allowed-origins", getEnvOrDefault("ALLOWED_ORIGINS", "*"), "Comma-separated list of allowed origins")
	flag.BoolVar(&cfg.HashAPIKey, "hash-api-key", getEnvAsBool("HASH_API_KEY", true), "Hash API key")
	flag.Parse()

	cfg = config.ValidateAndSetDefaults(cfg)

	application, err := app.NewApp(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize application: %v", err)
	}

	srv := application.CreateServer()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		<-quit
		log.Println("Shutting down server...")
		application.Shutdown(srv)
	}()

	log.Printf("Server starting on :%s", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}

	wg.Wait()
}
func getEnvOrDefault(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvAsBool(key string, defaultValue bool) bool {
	val := getEnvOrDefault(key, "")
	if val == "" {
		return defaultValue
	}
	return strings.ToLower(val) == "true"
}
