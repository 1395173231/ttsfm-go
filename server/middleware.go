package server

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"ttsfm-go/ttsfm"
)

// APIKeyConfig API 密钥配置
type APIKeyConfig struct {
	Enabled bool
	Keys    []string
}

// APIKeyMiddleware API 密钥验证中间件
func APIKeyMiddleware(config *APIKeyConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if config == nil || !config.Enabled || len(config.Keys) == 0 {
			c.Next()
			return
		}

		authHeader := c.GetHeader("Authorization")
		var apiKey string

		if strings.HasPrefix(authHeader, "Bearer ") {
			apiKey = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		} else {
			apiKey = strings.TrimSpace(c.GetHeader("X-API-Key"))
		}

		if apiKey == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"message": "API key is required",
					"type":    "authentication_error",
					"code":    "missing_api_key",
				},
			})
			c.Abort()
			return
		}

		valid := false
		for _, key := range config.Keys {
			if apiKey == strings.TrimSpace(key) && apiKey != "" {
				valid = true
				break
			}
		}

		if !valid {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"message": "Invalid API key",
					"type":    "authentication_error",
					"code":    "invalid_api_key",
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// LoggingMiddleware 日志中间件
func LoggingMiddleware(logger ttsfm.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		if query != "" {
			path = path + "?" + query
		}

		if logger != nil {
			logger.Info("[%s] %s %s %d %v",
				c.Request.Method,
				path,
				c.ClientIP(),
				status,
				latency,
			)
		}
	}
}

// CORSMiddleware CORS 中间件
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-API-Key")
		c.Header("Access-Control-Expose-Headers", "Content-Length, X-Audio-Format, X-Audio-Size, X-Chunks-Combined, X-Auto-Combine, X-Powered-By")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// RecoveryMiddleware 恢复中间件
func RecoveryMiddleware(logger ttsfm.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				if logger != nil {
					logger.Error("Panic recovered: %v", err)
				}
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": gin.H{
						"message": "An unexpected error occurred",
						"type":    "internal_error",
						"code":    "internal_error",
					},
				})
				c.Abort()
			}
		}()

		c.Next()
	}
}

// RateLimitMiddleware 简单的速率限制中间件（进程内）
func RateLimitMiddleware(requestsPerSecond int) gin.HandlerFunc {
	limiter := newRateLimiter(requestsPerSecond)

	return func(c *gin.Context) {
		if !limiter.allow() {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"message": "Too many requests, please slow down",
					"type":    "rate_limit_error",
					"code":    "rate_limit_exceeded",
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

type rateLimiter struct {
	mu         sync.Mutex
	tokens     int
	maxTokens  int
	refillRate int
	lastRefill time.Time
}

func newRateLimiter(requestsPerSecond int) *rateLimiter {
	if requestsPerSecond <= 0 {
		requestsPerSecond = 10
	}
	now := time.Now()
	return &rateLimiter{
		tokens:     requestsPerSecond,
		maxTokens:  requestsPerSecond,
		refillRate: requestsPerSecond,
		lastRefill: now,
	}
}

func (r *rateLimiter) allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(r.lastRefill)

	tokensToAdd := int(elapsed.Seconds()) * r.refillRate
	if tokensToAdd > 0 {
		r.tokens = minInt(r.maxTokens, r.tokens+tokensToAdd)
		r.lastRefill = now
	}

	if r.tokens > 0 {
		r.tokens--
		return true
	}

	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}