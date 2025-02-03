package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
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

func (a *App) toggleStatus(c *gin.Context) {
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

	newStatus := database.SedeStatus{
		IsOpen:    !currentStatus.IsOpen,
		Timestamp: time.Now().UTC(),
	}

	if err := a.repo.CreateStatus(ctx, newStatus); err != nil {
		handleDatabaseError(c, err)
		return
	}

	c.String(http.StatusOK, fmt.Sprintf("%v", newStatus.IsOpen))
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
