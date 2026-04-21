package app

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/secure"
	"github.com/gin-gonic/gin"
	"github.com/metro-olografix/sede/internal/database"
	"gorm.io/gorm"
)

const spaceContextKey = "space"

func (a *App) setupRouter() *gin.Engine {
	if !a.config.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()

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

	r.Use(
		gin.Recovery(),
		a.secureMiddleware(),
		a.rateLimitMiddleware(),
		cors.New(corsConfig),
	)

	if a.config.Debug {
		r.Use(gin.Logger())
	}

	// Legacy bare routes — resolve to the default space so existing clients
	// (ESP32 button, MCP server, deployed integrations) keep working.
	r.GET("/status", a.resolveDefaultSpace(), a.getStatus)
	r.GET("/stats", a.resolveDefaultSpace(), a.getStats)
	r.GET("/spaceapi.json", a.resolveDefaultSpace(), a.getSpaceAPI)
	r.POST("/toggle", a.resolveDefaultSpace(), a.authMiddleware(), a.toggleStatus)

	sg := r.Group("/s/:slug", a.resolveSpaceFromPath())
	{
		sg.GET("/status", a.getStatus)
		sg.GET("/stats", a.getStats)
		sg.GET("/spaceapi.json", a.getSpaceAPI)
		sg.POST("/toggle", a.authMiddleware(), a.toggleStatus)
	}

	if a.config.Debug {
		r.StaticFS("/ui", http.Dir("./ui"))
		uiHandler := http.StripPrefix("/ui", http.FileServer(http.Dir("./ui")))
		r.GET("/s/:slug/ui/*filepath", a.resolveSpaceFromPath(), func(c *gin.Context) {
			c.Request.URL.Path = "/ui" + c.Param("filepath")
			uiHandler.ServeHTTP(c.Writer, c.Request)
		})
	}

	return r
}

func (a *App) resolveDefaultSpace() gin.HandlerFunc {
	return func(c *gin.Context) {
		if a.defaultSpace == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "default space not configured"})
			return
		}
		c.Set(spaceContextKey, a.defaultSpace)
		c.Next()
	}
}

// resolveSpaceFromPath resolves :slug via the in-memory hot map; the DB is a
// fallback only for rows that arrive after boot (future admin API). A missing
// slug is a flat 404 — we don't distinguish typo vs. truly-absent so the
// endpoint can't be used to enumerate configured spaces.
func (a *App) resolveSpaceFromPath() gin.HandlerFunc {
	return func(c *gin.Context) {
		slug := c.Param("slug")
		if sp, ok := a.spaces[slug]; ok {
			c.Set(spaceContextKey, sp)
			c.Next()
			return
		}
		sp, err := a.repo.GetSpaceBySlug(c.Request.Context(), slug)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "space not found"})
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "space lookup failed"})
			return
		}
		a.spaces[sp.Slug] = sp
		c.Set(spaceContextKey, sp)
		c.Next()
	}
}

func spaceFrom(c *gin.Context) *database.Space {
	v, ok := c.Get(spaceContextKey)
	if !ok {
		return nil
	}
	sp, _ := v.(*database.Space)
	return sp
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
