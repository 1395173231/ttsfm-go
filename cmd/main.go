package main

import (
	"flag"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"ttsfm-go/server"
	"ttsfm-go/ttsfm"
)

func main() {
	host := flag.String("host", "0.0.0.0", "Server host")
	port := flag.Int("port", 8080, "Server port")
	apiKeys := flag.String("api-keys", "", "Comma-separated API keys (optional)")
	enableAuth := flag.Bool("enable-auth", false, "Enable API key authentication")
	enableRateLimit := flag.Bool("enable-rate-limit", false, "Enable rate limiting")
	rateLimit := flag.Int("rate-limit", 10, "Requests per second limit")
	timeout := flag.Duration("timeout", 60*time.Second, "Request timeout")
	baseURL := flag.String("base-url", "https://www.openai.fm", "TTS service base URL")

	flag.Parse()

	// 环境变量覆盖
	if envHost := strings.TrimSpace(os.Getenv("TTSFM_HOST")); envHost != "" {
		*host = envHost
	}
	if envPort := strings.TrimSpace(os.Getenv("TTSFM_PORT")); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil && p > 0 {
			*port = p
		}
	}
	if envKeys := strings.TrimSpace(os.Getenv("TTSFM_API_KEYS")); envKeys != "" {
		*apiKeys = envKeys
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TTSFM_ENABLE_AUTH")), "true") {
		*enableAuth = true
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TTSFM_ENABLE_RATE_LIMIT")), "true") {
		*enableRateLimit = true
	}
	if envRate := strings.TrimSpace(os.Getenv("TTSFM_RATE_LIMIT")); envRate != "" {
		if r, err := strconv.Atoi(envRate); err == nil && r > 0 {
			*rateLimit = r
		}
	}
	if envBaseURL := strings.TrimSpace(os.Getenv("TTSFM_BASE_URL")); envBaseURL != "" {
		*baseURL = envBaseURL
	}

	var keys []string
	if strings.TrimSpace(*apiKeys) != "" {
		parts := strings.Split(*apiKeys, ",")
		keys = make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				keys = append(keys, p)
			}
		}
	}

	logger := &ttsfm.DefaultLogger{}

	cfg := &server.ServerConfig{
		Host:             *host,
		Port:             *port,
		APIKeys:          keys,
		EnableAPIKeyAuth: *enableAuth,

		RequestTimeout:  *timeout,
		ShutdownTimeout: 10 * time.Second,

		EnableCORS:      true,
		EnableRateLimit: *enableRateLimit,
		RateLimitPerSec: *rateLimit,

		Logger: logger,
		TTSClientOptions: []ttsfm.ClientOption{
			ttsfm.WithBaseURL(*baseURL),
			ttsfm.WithTimeout(*timeout),
			ttsfm.WithMaxRetries(3),
			ttsfm.WithLogger(logger),
		},
	}

	srv, err := server.NewServer(cfg)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	if err := srv.StartWithGracefulShutdown(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}