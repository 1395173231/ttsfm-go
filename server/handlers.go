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
	client             SpeechClient
	logger             ttsfm.Logger
	timeout            time.Duration
	autoCombineDefault bool
}

// NewHandler 创建处理器
func NewHandler(client SpeechClient, logger ttsfm.Logger, timeout time.Duration, autoCombineDefault bool) *Handler {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Handler{
		client:             client,
		logger:             logger,
		timeout:            timeout,
		autoCombineDefault: autoCombineDefault,
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
		req.MaxLength = 4096
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
		// 长文本需要分片处理，无法完全流式
		h.handleLongText(c, ctx, &req, voice, format)
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

	// 获取流式响应
	streamResp, err := h.client.GenerateSpeechStream(ctx, req.Input, opts...)
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

func (h *Handler) handleLongText(
	c *gin.Context,
	ctx context.Context,
	req *SpeechRequest,
	voice ttsfm.Voice,
	format ttsfm.AudioFormat,
) {
	h.info("Long text detected (%d chars), auto-combining enabled", len(req.Input))

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

	responses, err := h.client.GenerateSpeechLongText(ctx, req.Input, req.MaxLength, true, opts...)
	if err != nil {
		h.handleError(c, err)
		return
	}
	if len(responses) == 0 {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: ErrorDetail{
				Message: "No valid text chunks found",
				Type:    "processing_error",
				Code:    "no_chunks",
			},
		})
		return
	}

	chunks := make([][]byte, 0, len(responses))
	for _, r := range responses {
		chunks = append(chunks, r.AudioData)
	}

	actualFormat := responses[0].Format
	contentType := responses[0].ContentType

	combinedAudio, err := ttsfm.CombineAudioChunks(chunks, actualFormat)
	if err != nil {
		h.error("Failed to combine audio chunks: %v", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: ErrorDetail{
				Message: "Failed to combine audio chunks",
				Type:    "processing_error",
				Code:    "combine_failed",
			},
		})
		return
	}

	h.info("Successfully combined %d chunks into single audio file", len(responses))

	c.Header("Content-Type", contentType)
	c.Header("Content-Length", strconv.Itoa(len(combinedAudio)))
	c.Header("X-Audio-Format", string(actualFormat))
	c.Header("X-Audio-Size", strconv.Itoa(len(combinedAudio)))
	c.Header("X-Chunks-Combined", strconv.Itoa(len(responses)))
	c.Header("X-Original-Text-Length", strconv.Itoa(len(req.Input)))
	c.Header("X-Auto-Combine", "true")
	c.Header("X-Powered-By", "TTSFM-OpenAI-Compatible")

	c.Data(http.StatusOK, contentType, combinedAudio)
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
