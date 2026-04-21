package app

import (
	"bytes"
	"context"
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
	cooldownPeriod = time.Minute
	contextTimeout = 30 * time.Second
)

type StatsResponse struct {
	TotalChanges int64                 `json:"total_changes"`
	LastChange   time.Time             `json:"last_change"`
	CurrentState bool                  `json:"current_state"`
	DailyChanges []database.DailyStats `json:"daily_changes"`
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

// authMiddleware compares X-API-KEY against the bcrypt hash stored on the
// resolved space. Every space owns its own key so one space's secret cannot
// unlock another's toggle endpoint.
func (a *App) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		sp := spaceFrom(c)
		if sp == nil {
			abortUnauthorized(c)
			return
		}
		apiKey := c.GetHeader("X-API-KEY")
		if apiKey == "" {
			abortUnauthorized(c)
			return
		}
		if err := bcrypt.CompareHashAndPassword(sp.APIKeyHash, []byte(apiKey)); err != nil {
			logSecurityEvent(fmt.Sprintf("invalid API key attempt for space %q", sp.Slug))
			abortUnauthorized(c)
			return
		}
		c.Next()
	}
}

func (a *App) getStatus(c *gin.Context) {
	sp := spaceFrom(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), contextTimeout)
	defer cancel()

	status, err := a.repo.GetLatestStatus(ctx, sp.ID)
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
	sp := spaceFrom(c)

	var req ToggleStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), contextTimeout)
	defer cancel()

	currentStatus, err := a.repo.GetLatestStatus(ctx, sp.ID)
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

	var cardName string
	if req.CardID != "" && req.Hash != "" {
		cardName = a.getCardName(ctx, req.CardID, req.Hash, c)
		if c.IsAborted() {
			return
		}
	}

	newStatus := database.SedeStatus{
		SpaceID:   sp.ID,
		IsOpen:    !currentStatus.IsOpen,
		Timestamp: time.Now().UTC(),
	}

	if err := a.repo.CreateStatus(ctx, newStatus); err != nil {
		handleDatabaseError(c, err)
		return
	}

	if a.telegram.IsInitialized() && sp.TelegramChatID != 0 {
		go func() {
			emoji := "🟢"
			action := "aperta"
			if !newStatus.IsOpen {
				emoji = "🔴"
				action = "chiusa"
			}

			var msg string
			if cardName != "" {
				msg = fmt.Sprintf("%s sede %s %s da %s", emoji, sp.Name, action, cardName)
			} else {
				msg = fmt.Sprintf("%s sede %s %s", emoji, sp.Name, action)
			}

			if err := a.telegram.Send(sp.TelegramChatID, sp.TelegramThread, msg); err != nil {
				log.Printf("Failed to send Telegram notification: %v", err)
			}
		}()
	}

	c.String(http.StatusOK, fmt.Sprintf("%v", newStatus.IsOpen))
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
	sp := spaceFrom(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), contextTimeout)
	defer cancel()

	weeklyStats, err := a.repo.GetWeeklyStats(ctx, sp.ID)
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

type SpaceAPIResponse struct {
	APICompatibility []string          `json:"api_compatibility"`
	Space            string            `json:"space"`
	Logo             string            `json:"logo"`
	URL              string            `json:"url"`
	Location         map[string]any    `json:"location"`
	State            SpaceAPIState     `json:"state"`
	Contact          map[string]string `json:"contact"`
	Projects         []string          `json:"projects"`
	Links            []SpaceAPILink    `json:"links"`
}

type SpaceAPIState struct {
	Open       bool   `json:"open"`
	Message    string `json:"message"`
	LastChange int64  `json:"lastchange"`
}

type SpaceAPILink struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

func (a *App) getSpaceAPI(c *gin.Context) {
	sp := spaceFrom(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), contextTimeout)
	defer cancel()

	status, err := a.repo.GetLatestStatus(ctx, sp.ID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		handleDatabaseError(c, err)
		return
	}

	var isOpen bool
	var lastChange int64
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		isOpen = status.IsOpen
		lastChange = status.Timestamp.Unix()
	}

	var projects []string
	if sp.Projects != "" {
		if err := json.Unmarshal([]byte(sp.Projects), &projects); err != nil {
			log.Printf("space %q: decode projects: %v", sp.Slug, err)
		}
	}
	var links []SpaceAPILink
	if sp.Links != "" {
		if err := json.Unmarshal([]byte(sp.Links), &links); err != nil {
			log.Printf("space %q: decode links: %v", sp.Slug, err)
		}
	}

	resp := SpaceAPIResponse{
		APICompatibility: []string{"15"},
		Space:            sp.Name,
		Logo:             sp.LogoURL,
		URL:              sp.URL,
		Location: map[string]any{
			"address":  sp.Address,
			"lat":      sp.Lat,
			"lon":      sp.Lon,
			"timezone": sp.Timezone,
		},
		State: SpaceAPIState{
			Open:       isOpen,
			LastChange: lastChange,
			Message:    sp.Message,
		},
		Contact: map[string]string{
			"email": sp.ContactEmail,
		},
		Projects: projects,
		Links:    links,
	}

	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Cache-Control", "no-cache, must-revalidate")
	c.JSON(http.StatusOK, resp)
}
