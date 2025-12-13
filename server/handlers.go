package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"ttsfm-go/ttsfm"
)

// SpeechRequest OpenAI 兼容的语音生成请求
type SpeechRequest struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice"`
	ResponseFormat string  `json:"response_format"`
	Instructions   string  `json:"instructions"`
	Speed          float64 `json:"speed"`

	AutoCombine *bool `json:"auto_combine,omitempty"`
	MaxLength   int   `json:"max_length"`
}

// ErrorResponse 错误响应（OpenAI 风格）
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// SpeechClient 抽象客户端接口（支持流式）
type SpeechClient interface {
	// 流式方法
	GenerateSpeechStream(ctx context.Context, text string, opts ...ttsfm.RequestOption) (*ttsfm.TTSStreamResponse, error)
	// 非流式方法（长文本需要）
	GenerateSpeechLongText(ctx context.Context, text string, maxLength int, preserveWords bool, opts ...ttsfm.RequestOption) ([]*ttsfm.TTSResponse, error)
}

// Handler 处理器
type Handler struct {
	TTSClientOptions   []ttsfm.ClientOption
	logger             ttsfm.Logger
	timeout            time.Duration
	autoCombineDefault bool
}

// NewHandler 创建处理器
func NewHandler(cfg *ServerConfig) *Handler {
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 60 * time.Second
	}

	return &Handler{
		logger:             cfg.Logger,
		timeout:            cfg.RequestTimeout,
		autoCombineDefault: cfg.AutoCombine,
		TTSClientOptions:   cfg.TTSClientOptions,
	}
}

// OpenAISpeech OpenAI 兼容的语音生成接口
// POST /v1/audio/speech
func (h *Handler) OpenAISpeech(c *gin.Context) {
	var req SpeechRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.warn("Failed to parse request: %v", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: ErrorDetail{
				Message: "Invalid JSON data provided",
				Type:    "invalid_request_error",
				Code:    "invalid_json",
			},
		})
		return
	}

	if strings.TrimSpace(req.Voice) == "" {
		req.Voice = "alloy"
	}
	if strings.TrimSpace(req.ResponseFormat) == "" {
		req.ResponseFormat = "mp3"
	}
	if req.MaxLength == 0 {
		req.MaxLength = 2048
	}

	autoCombine := h.autoCombineDefault
	if req.AutoCombine != nil {
		autoCombine = *req.AutoCombine
	}

	if strings.TrimSpace(req.Input) == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: ErrorDetail{
				Message: "Input text is required",
				Type:    "invalid_request_error",
				Code:    "missing_input",
			},
		})
		return
	}

	voice := ttsfm.Voice(req.Voice)
	if !voice.IsValid() {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: ErrorDetail{
				Message: fmt.Sprintf("Invalid voice: %s. Must be one of: %v", req.Voice, ttsfm.ValidVoices),
				Type:    "invalid_request_error",
				Code:    "invalid_voice",
			},
		})
		return
	}

	format := ttsfm.AudioFormat(req.ResponseFormat)
	if !format.IsValid() {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: ErrorDetail{
				Message: fmt.Sprintf("Invalid response_format: %s. Must be one of: %v", req.ResponseFormat, ttsfm.ValidFormats),
				Type:    "invalid_request_error",
				Code:    "invalid_format",
			},
		})
		return
	}

	h.info("OpenAI API: Generating speech: text='%s...', voice=%s, format=%s, auto_combine=%v, max_length=%d",
		truncateString(req.Input, 50), req.Voice, req.ResponseFormat, autoCombine, req.MaxLength)

	ctx := c.Request.Context()

	textLength := len(req.Input)

	if textLength > req.MaxLength && autoCombine {
		// 长文本：分片后按格式流式拼接输出，避免内存峰值并降低等待时间
		h.handleLongTextStream(c, ctx, &req, voice, format)
		return
	}

	if textLength > req.MaxLength && !autoCombine {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: ErrorDetail{
				Message: fmt.Sprintf(
					"Input text is too long (%d characters). Maximum allowed length is %d characters. "+
						"Enable auto_combine to automatically split and combine long text.",
					textLength, req.MaxLength,
				),
				Type: "invalid_request_error",
				Code: "text_too_long",
			},
		})
		return
	}

	// 短文本使用流式处理
	h.handleShortTextStream(c, ctx, &req, voice, format, autoCombine)
}

// handleShortTextStream 流式处理短文本
func (h *Handler) handleShortTextStream(
	c *gin.Context,
	ctx context.Context,
	req *SpeechRequest,
	voice ttsfm.Voice,
	format ttsfm.AudioFormat,
	autoCombine bool,
) {
	opts := []ttsfm.RequestOption{
		ttsfm.WithVoice(voice),
		ttsfm.WithFormat(format),
		ttsfm.WithMaxLength(req.MaxLength),
	}
	if strings.TrimSpace(req.Instructions) != "" {
		opts = append(opts, ttsfm.WithInstructions(req.Instructions))
	}
	if req.Speed != 0 {
		opts = append(opts, ttsfm.WithSpeed(req.Speed))
	}
	client, err := ttsfm.NewTTSClient(h.TTSClientOptions...)
	if err != nil {
		h.error("Failed to create TTS client: %v", err)
		return
	}
	defer client.Close()
	// 获取流式响应
	streamResp, err := client.GenerateSpeechStream(ctx, req.Input, opts...)
	if err != nil {
		h.handleError(c, err)
		return
	}
	defer streamResp.Close()

	// 设置响应头
	c.Header("Content-Type", streamResp.ContentType)
	c.Header("Transfer-Encoding", "chunked")
	c.Header("X-Audio-Format", string(streamResp.Format))
	c.Header("X-Chunks-Combined", "1")
	c.Header("X-Auto-Combine", fmt.Sprintf("%v", autoCombine))
	c.Header("X-Powered-By", "TTSFM-OpenAI-Compatible")

	// 设置状态码
	c.Status(http.StatusOK)

	// 流式写入响应
	written, err := io.Copy(c.Writer, streamResp.Body)
	if err != nil && !errors.Is(err, io.EOF) && err.Error() != "EOF" {
		// 此时已经开始写入响应，无法返回 JSON 错误
		h.error("Error streaming response: %v (written %d bytes)", err, written)
		return
	}

	h.info("Successfully streamed %d bytes of %s audio", written, streamResp.Format)
}

func (h *Handler) handleLongTextStream(
	c *gin.Context,
	ctx context.Context,
	req *SpeechRequest,
	voice ttsfm.Voice,
	format ttsfm.AudioFormat,
) {
	h.info("Long text detected (%d chars), auto-combining enabled (streaming)", len(req.Input))

	opts := []ttsfm.RequestOption{
		ttsfm.WithVoice(voice),
		ttsfm.WithFormat(format),
		ttsfm.WithMaxLength(req.MaxLength),
	}
	if strings.TrimSpace(req.Instructions) != "" {
		opts = append(opts, ttsfm.WithInstructions(req.Instructions))
	}
	if req.Speed != 0 {
		opts = append(opts, ttsfm.WithSpeed(req.Speed))
	}

	client, err := ttsfm.NewTTSClient(h.TTSClientOptions...)
	if err != nil {
		h.error("Failed to create TTS client: %v", err)
		return
	}
	defer client.Close()

	streamResp, err := client.GenerateSpeechLongTextStreamConcurrent(
		ctx,
		req.Input,
		req.MaxLength,
		true,
		&ttsfm.LongTextStreamConfig{
			MaxConcurrent:   3,
			ChunkBufferSize: 32 * 1024,
		},
		opts...,
	)
	if err != nil {
		h.handleError(c, err)
		return
	}
	defer streamResp.Close()

	chunksTotal := streamResp.Metadata["chunks_total"]
	if strings.TrimSpace(chunksTotal) == "" {
		chunksTotal = "0"
	}

	c.Header("Content-Type", streamResp.ContentType)
	c.Header("Transfer-Encoding", "chunked")
	c.Header("X-Audio-Format", string(streamResp.Format))
	c.Header("X-Chunks-Combined", chunksTotal)
	c.Header("X-Original-Text-Length", strconv.Itoa(len(req.Input)))
	c.Header("X-Auto-Combine", "true")
	c.Header("X-Powered-By", "TTSFM-OpenAI-Compatible")

	c.Status(http.StatusOK)

	written, err := io.Copy(c.Writer, streamResp.Body)
	if err != nil && !errors.Is(err, io.EOF) && err.Error() != "EOF" {
		h.error("Error streaming long text response: %v (written %d bytes)", err, written)
		return
	}

	h.info("Successfully streamed %d bytes of %s audio (chunks=%s)", written, streamResp.Format, chunksTotal)
}

func (h *Handler) handleError(c *gin.Context, err error) {
	h.error("Request error: %v", err)

	switch e := err.(type) {
	case *ttsfm.ValidationException:
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: ErrorDetail{
				Message: e.Message,
				Type:    "invalid_request_error",
				Code:    "validation_error",
			},
		})

	case *ttsfm.AuthenticationException:
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error: ErrorDetail{
				Message: "Invalid API key",
				Type:    "authentication_error",
				Code:    "invalid_api_key",
			},
		})

	case *ttsfm.RateLimitException:
		c.JSON(http.StatusTooManyRequests, ErrorResponse{
			Error: ErrorDetail{
				Message: "Rate limit exceeded",
				Type:    "rate_limit_error",
				Code:    "rate_limit_exceeded",
			},
		})

	case *ttsfm.NetworkException:
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error: ErrorDetail{
				Message: "TTS service is currently unavailable",
				Type:    "service_unavailable_error",
				Code:    "service_unavailable",
			},
		})

	case *ttsfm.APIException:
		statusCode := e.StatusCode
		if statusCode == 0 {
			statusCode = http.StatusInternalServerError
		}
		c.JSON(statusCode, ErrorResponse{
			Error: ErrorDetail{
				Message: "Text-to-speech generation failed",
				Type:    "api_error",
				Code:    "tts_error",
			},
		})

	default:
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: ErrorDetail{
				Message: "An unexpected error occurred",
				Type:    "internal_error",
				Code:    "internal_error",
			},
		})
	}
}

// HealthCheck 健康检查接口
func (h *Handler) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "ttsfm",
		"version": "1.0.0",
	})
}

// GetVoices 获取可用语音列表
func (h *Handler) GetVoices(c *gin.Context) {
	voices := make([]gin.H, len(ttsfm.ValidVoices))
	for i, v := range ttsfm.ValidVoices {
		voices[i] = gin.H{
			"id":   string(v),
			"name": string(v),
		}
	}

	c.JSON(http.StatusOK, gin.H{"voices": voices})
}

// GetFormats 获取支持的格式列表
func (h *Handler) GetFormats(c *gin.Context) {
	formats := make([]gin.H, len(ttsfm.ValidFormats))
	for i, f := range ttsfm.ValidFormats {
		formats[i] = gin.H{
			"id":           string(f),
			"name":         string(f),
			"content_type": ttsfm.GetContentType(f),
		}
	}

	c.JSON(http.StatusOK, gin.H{"formats": formats})
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (h *Handler) info(msg string, args ...interface{}) {
	if h.logger != nil {
		h.logger.Info(msg, args...)
	}
}

func (h *Handler) warn(msg string, args ...interface{}) {
	if h.logger != nil {
		h.logger.Warn(msg, args...)
	}
}

func (h *Handler) error(msg string, args ...interface{}) {
	if h.logger != nil {
		h.logger.Error(msg, args...)
	}
}
