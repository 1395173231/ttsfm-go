package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"ttsfm-go/ttsfm"
)

// ServerConfig 服务器配置
type ServerConfig struct {
	Host             string
	Port             int
	APIKeys          []string
	EnableAPIKeyAuth bool

	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration

	EnableCORS      bool
	EnableRateLimit bool
	RateLimitPerSec int

	Logger           ttsfm.Logger
	TTSClientOptions []ttsfm.ClientOption
}

// DefaultServerConfig 默认服务器配置
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		Host:            "0.0.0.0",
		Port:            8080,
		RequestTimeout:  60 * time.Second,
		ShutdownTimeout: 10 * time.Second,
		EnableCORS:      true,
		EnableRateLimit: false,
		RateLimitPerSec: 10,
		Logger:          &ttsfm.DefaultLogger{},
	}
}

// Server HTTP 服务器
type Server struct {
	config *ServerConfig
	engine *gin.Engine

	httpServer *http.Server
	ttsClient  *ttsfm.TTSClient
	handler    *Handler
	logger     ttsfm.Logger
}

// NewServer 创建服务器
func NewServer(config *ServerConfig) (*Server, error) {
	if config == nil {
		config = DefaultServerConfig()
	}
	if config.Logger == nil {
		config.Logger = &ttsfm.DefaultLogger{}
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 60 * time.Second
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = 10 * time.Second
	}

	ttsClient, err := ttsfm.NewTTSClient(config.TTSClientOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to create TTS client: %w", err)
	}

	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()

	srv := &Server{
		config:   config,
		engine:   engine,
		ttsClient: ttsClient,
		handler:  NewHandler(ttsClient, config.Logger, config.RequestTimeout),
		logger:   config.Logger,
	}

	srv.setupMiddleware()
	srv.setupRoutes()

	return srv, nil
}

func (s *Server) setupMiddleware() {
	s.engine.Use(RecoveryMiddleware(s.logger))
	s.engine.Use(LoggingMiddleware(s.logger))

	if s.config.EnableCORS {
		s.engine.Use(CORSMiddleware())
	}
	if s.config.EnableRateLimit {
		s.engine.Use(RateLimitMiddleware(s.config.RateLimitPerSec))
	}
}

func (s *Server) setupRoutes() {
	s.engine.GET("/health", s.handler.HealthCheck)
	s.engine.GET("/", s.handler.HealthCheck)

	api := s.engine.Group("")

	if s.config.EnableAPIKeyAuth && len(s.config.APIKeys) > 0 {
		api.Use(APIKeyMiddleware(&APIKeyConfig{
			Enabled: true,
			Keys:    s.config.APIKeys,
		}))
	}

	v1 := api.Group("/v1")
	{
		audio := v1.Group("/audio")
		{
			audio.POST("/speech", s.handler.OpenAISpeech)
		}

		v1.GET("/voices", s.handler.GetVoices)
		v1.GET("/formats", s.handler.GetFormats)
	}

	// 兼容入口（非 OpenAI 标准，但方便自用）
	api.POST("/api/speech", s.handler.OpenAISpeech)
}

// Start 启动服务器（阻塞）
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.engine,
	}

	s.logger.Info("Starting TTSFM server on %s", addr)
	return s.httpServer.ListenAndServe()
}

// StartWithGracefulShutdown 启动并在 SIGINT/SIGTERM 时优雅关闭
func (s *Server) StartWithGracefulShutdown() error {
	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.engine,
	}

	go func() {
		s.logger.Info("Starting TTSFM server on %s", addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	s.logger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		s.logger.Error("Server forced to shutdown: %v", err)
		return err
	}

	if s.ttsClient != nil {
		if err := s.ttsClient.Close(); err != nil {
			s.logger.Error("Failed to close TTS client: %v", err)
		}
	}

	s.logger.Info("Server stopped")
	return nil
}

// Stop 外部触发停止
func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			return err
		}
	}
	if s.ttsClient != nil {
		return s.ttsClient.Close()
	}
	return nil
}

// Engine 返回 Gin 引擎（测试用）
func (s *Server) Engine() *gin.Engine {
	return s.engine
}