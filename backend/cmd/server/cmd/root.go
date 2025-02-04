package cmd

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/metro-olografix/sede/internal/app"
	"github.com/metro-olografix/sede/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfg     config.Config
	rootCmd = &cobra.Command{
		Use:   "sede",
		Short: "Metro Olografix HQ (^^)",
		Run:   runServer,
	}
)

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfg.Port, "port", "8080", "Server port")
	rootCmd.PersistentFlags().StringVar(&cfg.APIKey, "api-key", "change-me", "API key for authentication")
	rootCmd.PersistentFlags().BoolVar(&cfg.Debug, "debug", false, "Enable debug mode")
	rootCmd.PersistentFlags().StringVar(&cfg.AllowedOriginsStr, "allowed-origins", "*", "Comma-separated list of allowed origins")
	rootCmd.PersistentFlags().BoolVar(&cfg.HashAPIKey, "hash-api-key", true, "Hash API key")

	rootCmd.PersistentFlags().StringVar(&cfg.TelegramToken, "telegram-token", "", "Telegram bot token")
	rootCmd.PersistentFlags().Int64Var(&cfg.TelegramChatId, "telegram-chat-id", 0, "Telegram chat ID")
	rootCmd.PersistentFlags().IntVar(&cfg.TelegramChatThreadId, "telegram-chat-thread-id", 0, "Telegram chat thread ID")

	// Bind flags to viper
	viper.BindPFlag("port", rootCmd.PersistentFlags().Lookup("port"))
	viper.BindPFlag("api_key", rootCmd.PersistentFlags().Lookup("api-key"))
	viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))
	viper.BindPFlag("allowed_origins", rootCmd.PersistentFlags().Lookup("allowed-origins"))
	viper.BindPFlag("hash_api_key", rootCmd.PersistentFlags().Lookup("hash-api-key"))
	viper.BindPFlag("telegram_token", rootCmd.PersistentFlags().Lookup("telegram-token"))
	viper.BindPFlag("telegram_chat_id", rootCmd.PersistentFlags().Lookup("telegram-chat-id"))
	viper.BindPFlag("telegram_chat_thread_id", rootCmd.PersistentFlags().Lookup("telegram-chat-thread-id"))
}

func initConfig() {
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	// Update cfg with viper values
	cfg.Port = viper.GetString("port")
	cfg.APIKey = viper.GetString("api_key")
	cfg.Debug = viper.GetBool("debug")
	cfg.AllowedOriginsStr = viper.GetString("allowed_origins")
	cfg.HashAPIKey = viper.GetBool("hash_api_key")
	cfg.TelegramToken = viper.GetString("telegram_token")
	cfg.TelegramChatId = viper.GetInt64("telegram_chat_id")
	cfg.TelegramChatThreadId = viper.GetInt("telegram_chat_thread_id")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("Error executing command: %v", err)
	}
}

func runServer(cmd *cobra.Command, args []string) {
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
