package app

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/metro-olografix/sede/internal/database"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	apiKeyMinLength = 16
	cooldownPeriod  = time.Minute
	contextTimeout  = 30 * time.Second
)

type StatsResponse struct {
	TotalChanges int64                 `json:"total_changes"`
	LastChange   time.Time             `json:"last_change"`
	CurrentState bool                  `json:"current_state"`
	DailyChanges []database.DailyStats `json:"daily_changes"`
}

// Add new types for hourly breakdowns
type HourlyStat struct {
	Hour        string  `json:"hour"`
	Probability float64 `json:"probability"`
}

type WeeklyStatsDetailed struct {
	Day              string       `json:"day"`
	DailyProbability float64      `json:"dailyProbability"`
	Hourly           []HourlyStat `json:"hourly"`
}

func (a *App) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-API-KEY")
		if apiKey == "" {
			abortUnauthorized(c)
			return
		}

		if a.config.HashAPIKey {
			if err := bcrypt.CompareHashAndPassword(a.apiKeyHash, []byte(apiKey)); err != nil {
				logSecurityEvent("Invalid API key attempt")
				abortUnauthorized(c)
				return
			}
		} else {
			if subtle.ConstantTimeCompare([]byte(apiKey), []byte(a.config.APIKey)) != 1 {
				logSecurityEvent("API key mismatch")
				abortUnauthorized(c)
				return
			}
		}
		c.Next()
	}
}

func (a *App) getStatus(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), contextTimeout)
	defer cancel()

	status, err := a.repo.GetLatestStatus(ctx)
	if handleDatabaseError(c, err) {
		return
	}

	c.String(http.StatusOK, fmt.Sprintf("%v", status.IsOpen))
}

type ToggleStatusRequest struct {
	CardID string `json:"cardId"`
	Hash   string `json:"hash"`
}

func (a *App) toggleStatus(c *gin.Context) {
	var req ToggleStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), contextTimeout)
	defer cancel()

	currentStatus, err := a.repo.GetLatestStatus(ctx)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		handleDatabaseError(c, err)
		return
	}

	if time.Since(currentStatus.Timestamp) < cooldownPeriod {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": fmt.Sprintf("Status can only be changed every %s", cooldownPeriod),
		})
		return
	}

	// Get card name via POST request
	var cardName string
	if req.CardID != "" && req.Hash != "" {
		cardName = a.getCardName(ctx, req.CardID, req.Hash, c)
		if c.IsAborted() {
			return
		}
	}

	// Toggle status
	newStatus := database.SedeStatus{
		IsOpen:    !currentStatus.IsOpen,
		Timestamp: time.Now().UTC(),
	}

	if err := a.repo.CreateStatus(ctx, newStatus); err != nil {
		handleDatabaseError(c, err)
		return
	}

	// Send notification
	if a.telegram.IsInitialized() {
		go func() {
			var msg string
			emoji := "🟢"
			action := "aperta"
			if !newStatus.IsOpen {
				emoji = "🔴"
				action = "chiusa"
			}

			if cardName != "" {
				msg = fmt.Sprintf("%s sede %s da %s", emoji, action, cardName)
			} else {
				msg = fmt.Sprintf("%s sede %s", emoji, action)
			}

			if err := a.telegram.Send(msg); err != nil {
				log.Printf("Failed to send Telegram notification: %v", err)
			}
		}()
	}

	c.JSON(http.StatusOK, gin.H{"isOpen": newStatus.IsOpen})
}

func (a *App) getCardName(ctx context.Context, cardID, hash string, c *gin.Context) string {
	client := &http.Client{Timeout: 10 * time.Second}

	cardID = strings.ReplaceAll(cardID, "-", "")

	payload := map[string]string{
		"cardId": cardID,
		"hash":   hash,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return ""
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://manager.olografix.org/api/card/name", bytes.NewBuffer(payloadBytes))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return ""
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-TOKEN", os.Getenv("SEDE_MANAGER_API_TOKEN"))

	resp, err := client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "Failed to contact card manager"})
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "Card manager returned error"})
		return ""
	}

	nameBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read card name response: %v", err)
		return ""
	}

	cardName := string(nameBytes)
	cardName = strings.Split(cardName, " ")[0]
	cardName = strings.ReplaceAll(cardName, "\"", "")
	return cardName
}

func (a *App) getStats(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), contextTimeout)
	defer cancel()

	weeklyStats, err := a.repo.GetWeeklyStats(ctx)
	if handleDatabaseError(c, err) {
		return
	}

	c.JSON(http.StatusOK, weeklyStats)
}

func abortUnauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error": "Invalid or missing API key",
	})
}

func logSecurityEvent(message string) {
	log.Printf("[SECURITY] %s", message)
}

func handleDatabaseError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}

	log.Printf("Database error: %v", err)

	if errors.Is(err, context.DeadlineExceeded) {
		c.AbortWithStatusJSON(http.StatusGatewayTimeout, gin.H{
			"error": "Database operation timed out",
		})
	} else {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Internal server error",
		})
	}
	return true
}

// Strutture handler per SpaceAPI (ci proviamo)
type SpaceAPIResponse struct {
	API      string                 `json:"api"`
	Space    string                 `json:"space"`
	Logo     string                 `json:"logo"`
	URL      string                 `json:"url"`
	Location map[string]interface{} `json:"location"`
	State    SpaceAPIState          `json:"state"`
	Contact  map[string]string      `json:"contact"`
	Projects []string               `json:"projects"`
	Links    []map[string]string    `json:"links"`
}

type SpaceAPIState struct {
	Open       *bool  `json:"open"`
	Message    string `json:"message"`
}

func (a *App) getSpaceAPI(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), contextTimeout)
	defer cancel()

	status, err := a.repo.GetLatestStatus(ctx)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		handleDatabaseError(c, err)
		return
	}

	var isOpen *bool
	
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		isOpen = &status.IsOpen
		lastChange = status.Timestamp.Unix()
	}

	spaceAPI := SpaceAPIResponse{
		API:   "15",
		Space: "Metro Olografix",
		Logo:  "https://olografix.org/images/metro-dark.png",
		URL:   "https://olografix.org",
		Location: map[string]interface{}{
			"address":  "Viale Marconi 278/1, 65127 Pescara, Italy",
			"lat":      44.989097,
			"lon":      11.426034,
			"timezone": "Europe/Rome",
		},
		State: SpaceAPIState{
			Open:       isOpen,
			Message:    "Ci riuniamo ogni lunedì sera dalle 21:00",
		},
		Contact: map[string]string{
			"email":   "info@olografix.org",
			"twitter": "@MetroOlografix",
		},
		Projects: []string{"https://github.com/Metro-Olografix"},
		Links: []map[string]string{
			{
				"name":        "MOCA - Metro Olografix Camp",
				"description": "Il più grande campeggio hacker in Italia",
				"url":         "https://moca.olografix.org",
			},
			{
				"name":        "Wikipedia",
				"description": "Pagina Wikipedia di Metro Olografix",
				"url":         "https://it.wikipedia.org/wiki/Metro_Olografix",
			},
		},
	}

	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Cache-Control", "no-cache, must-revalidate")
	c.JSON(http.StatusOK, spaceAPI)
}
