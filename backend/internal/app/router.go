package app

import (
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/secure"
	"github.com/gin-gonic/gin"
)

func (a *App) setupRouter() *gin.Engine {
	if !a.config.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()

	// CORS Configuration
	corsConfig := cors.Config{
		AllowMethods:     []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "X-API-KEY", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}

	if len(a.config.AllowedOrigins) > 0 {
		corsConfig.AllowOrigins = a.config.AllowedOrigins
	} else if a.config.Debug {
		corsConfig.AllowAllOrigins = true
	}

	// Middleware chain
	r.Use(
		gin.Recovery(),
		a.secureMiddleware(),
		a.rateLimitMiddleware(),
		cors.New(corsConfig),
	)

	if a.config.Debug {
		r.Use(gin.Logger())
	}

	// Public routes
	r.GET("/status", a.getStatus)
	r.GET("/stats", a.getStats)
	r.GET("/spaceapi.json", a.getSpaceAPI)

	// Authenticated routes
	secured := r.Group("/")
	secured.Use(a.authMiddleware())
	{
		secured.POST("/toggle", a.toggleStatus)
	}

	if a.config.Debug {
		r.StaticFS("/ui", http.Dir("./ui"))
	}

	return r
}

func (a *App) secureMiddleware() gin.HandlerFunc {
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

func (a *App) rateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		limiterCtx, err := a.rateLimiter.Get(ctx, c.ClientIP())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "rate limit error"})
			return
		}

		if limiterCtx.Reached {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}
