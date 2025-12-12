package ttsfm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"mime/multipart"
	"strings"
	"sync"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/google/uuid"
)

// Logger 日志接口
type Logger interface {
	Info(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	Debug(msg string, args ...interface{})
}

// DefaultLogger 默认日志实现
type DefaultLogger struct{}

func (l *DefaultLogger) Info(msg string, args ...interface{})  { log.Printf("[INFO] "+msg, args...) }
func (l *DefaultLogger) Warn(msg string, args ...interface{})  { log.Printf("[WARN] "+msg, args...) }
func (l *DefaultLogger) Error(msg string, args ...interface{}) { log.Printf("[ERROR] "+msg, args...) }
func (l *DefaultLogger) Debug(msg string, args ...interface{}) { log.Printf("[DEBUG] "+msg, args...) }

// ClientConfig 客户端配置
type ClientConfig struct {
	BaseURL       string
	APIKey        string
	Timeout       time.Duration
	MaxRetries    int
	VerifySSL     bool
	MaxConcurrent int
	ProxyURL      string
	Logger        Logger
}

// DefaultClientConfig 默认配置
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		BaseURL:       "https://www.openai.fm",
		Timeout:       30 * time.Second,
		MaxRetries:    3,
		VerifySSL:     true,
		MaxConcurrent: 10,
		Logger:        &DefaultLogger{},
	}
}

// TTSStreamResponse 流式响应
type TTSStreamResponse struct {
	Body        io.ReadCloser     // 原始响应体流
	ContentType string            // 内容类型
	Format      AudioFormat       // 音频格式
	Metadata    map[string]string // 元数据
}

// Close 关闭流式响应
func (r *TTSStreamResponse) Close() error {
	if r.Body != nil {
		return r.Body.Close()
	}
	return nil
}

// WriteTo 将流写入 writer（实现 io.WriterTo 接口）
func (r *TTSStreamResponse) WriteTo(w io.Writer) (int64, error) {
	if r.Body == nil {
		return 0, nil
	}
	return io.Copy(w, r.Body)
}

// TTSClient TTS 客户端
type TTSClient struct {
	config     *ClientConfig
	httpClient tls_client.HttpClient
	semaphore  chan struct{}
	logger     Logger
}

// NewTTSClient 创建新的 TTS 客户端
func NewTTSClient(opts ...ClientOption) (*TTSClient, error) {
	config := DefaultClientConfig()

	for _, opt := range opts {
		opt(config)
	}

	if !ValidateURL(config.BaseURL) {
		return nil, NewValidationException(
			fmt.Sprintf("Invalid base URL: %s", config.BaseURL),
			"base_url",
			config.BaseURL,
		)
	}

	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = 10
	}
	if config.Logger == nil {
		config.Logger = &DefaultLogger{}
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}

	timeoutSeconds := int(math.Ceil(config.Timeout.Seconds()))
	if timeoutSeconds <= 0 {
		timeoutSeconds = 1
	}

	jar := tls_client.NewCookieJar()
	tlsOptions := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(timeoutSeconds),
		tls_client.WithClientProfile(profiles.Safari_IOS_18_5),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(jar),
	}

	if !config.VerifySSL {
		tlsOptions = append(tlsOptions, tls_client.WithInsecureSkipVerify())
	}

	if strings.TrimSpace(config.ProxyURL) != "" {
		tlsOptions = append(tlsOptions, tls_client.WithProxyUrl(strings.TrimSpace(config.ProxyURL)))
	}

	httpClient, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), tlsOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to create tls client: %w", err)
	}

	client := &TTSClient{
		config:     config,
		httpClient: httpClient,
		semaphore:  make(chan struct{}, config.MaxConcurrent),
		logger:     config.Logger,
	}

	client.logger.Info("Initialized TTS client with base URL: %s", config.BaseURL)

	return client, nil
}

// ClientOption 客户端选项函数类型
type ClientOption func(*ClientConfig)

// WithBaseURL 设置基础 URL
func WithBaseURL(url string) ClientOption {
	return func(c *ClientConfig) {
		c.BaseURL = url
	}
}

// WithAPIKey 设置 API 密钥
func WithAPIKey(key string) ClientOption {
	return func(c *ClientConfig) {
		c.APIKey = key
	}
}

// WithTimeout 设置超时
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *ClientConfig) {
		c.Timeout = timeout
	}
}

// WithMaxRetries 设置最大重试次数
func WithMaxRetries(retries int) ClientOption {
	return func(c *ClientConfig) {
		c.MaxRetries = retries
	}
}

// WithMaxConcurrent 设置最大并发数
func WithMaxConcurrent(concurrent int) ClientOption {
	return func(c *ClientConfig) {
		c.MaxConcurrent = concurrent
	}
}

// WithProxyURL 设置代理地址（支持 http/https/socks5）
func WithProxyURL(proxyURL string) ClientOption {
	return func(c *ClientConfig) {
		c.ProxyURL = proxyURL
	}
}

// WithLogger 设置日志器
func WithLogger(logger Logger) ClientOption {
	return func(c *ClientConfig) {
		c.Logger = logger
	}
}

// SetProxy 动态设置代理
func (c *TTSClient) SetProxy(proxyURL string) error {
	return c.httpClient.SetProxy(strings.TrimSpace(proxyURL))
}

// ClearProxy 清除代理
func (c *TTSClient) ClearProxy() error {
	return c.httpClient.SetProxy("")
}

// GenerateSpeech 生成语音（保留原有方法以保持兼容性）
func (c *TTSClient) GenerateSpeech(ctx context.Context, text string, opts ...RequestOption) (*TTSResponse, error) {
	streamResp, err := c.GenerateSpeechStream(ctx, text, opts...)
	if err != nil {
		return nil, err
	}
	defer streamResp.Close()

	// 读取全部数据
	audioData, err := io.ReadAll(streamResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read audio data: %w", err)
	}

	return &TTSResponse{
		AudioData:   audioData,
		ContentType: streamResp.ContentType,
		Format:      streamResp.Format,
		Size:        len(audioData),
		Metadata:    streamResp.Metadata,
	}, nil
}

// GenerateSpeechStream 生成语音并返回流式响应
func (c *TTSClient) GenerateSpeechStream(ctx context.Context, text string, opts ...RequestOption) (*TTSStreamResponse, error) {
	sanitizedText, err := SanitizeText(text)
	if err != nil {
		return nil, err
	}

	request, err := NewTTSRequest(sanitizedText, opts...)
	if err != nil {
		return nil, err
	}

	return c.makeStreamRequest(ctx, request)
}

// GenerateSpeechLongText 处理长文本生成语音
func (c *TTSClient) GenerateSpeechLongText(
	ctx context.Context,
	text string,
	maxLength int,
	preserveWords bool,
	opts ...RequestOption,
) ([]*TTSResponse, error) {
	cleanText, err := SanitizeText(text)
	if err != nil {
		return nil, err
	}

	chunks := SplitTextByLength(cleanText, maxLength, preserveWords)
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no valid text chunks found after processing")
	}

	requests := make([]*TTSRequest, len(chunks))
	for i, chunk := range chunks {
		req, err := NewTTSRequest(chunk, append(opts, WithoutLengthValidation())...)
		if err != nil {
			return nil, fmt.Errorf("failed to create request for chunk %d: %w", i, err)
		}
		requests[i] = req
	}

	return c.GenerateSpeechBatch(ctx, requests)
}

// GenerateSpeechBatch 批量生成语音
func (c *TTSClient) GenerateSpeechBatch(ctx context.Context, requests []*TTSRequest) ([]*TTSResponse, error) {
	if len(requests) == 0 {
		return nil, nil
	}

	responses := make([]*TTSResponse, len(requests))
	errs := make([]error, len(requests))

	var wg sync.WaitGroup
	wg.Add(len(requests))

	for i, req := range requests {
		go func(idx int, request *TTSRequest) {
			defer wg.Done()
			resp, err := c.GenerateSpeechFromRequest(ctx, request)
			if err != nil {
				errs[idx] = err
				return
			}
			responses[idx] = resp
		}(i, req)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("request %d failed: %w", i, err)
		}
	}

	return responses, nil
}

// GenerateSpeechFromRequest 从请求对象生成语音
func (c *TTSClient) GenerateSpeechFromRequest(ctx context.Context, request *TTSRequest) (*TTSResponse, error) {
	streamResp, err := c.makeStreamRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	defer streamResp.Close()

	audioData, err := io.ReadAll(streamResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read audio data: %w", err)
	}

	return &TTSResponse{
		AudioData:   audioData,
		ContentType: streamResp.ContentType,
		Format:      streamResp.Format,
		Size:        len(audioData),
		Metadata:    streamResp.Metadata,
	}, nil
}

// GenerateSpeechFromRequestStream 从请求对象生成语音流
func (c *TTSClient) GenerateSpeechFromRequestStream(ctx context.Context, request *TTSRequest) (*TTSStreamResponse, error) {
	return c.makeStreamRequest(ctx, request)
}

// makeStreamRequest 执行实际的 HTTP 请求并返回流式响应
func (c *TTSClient) makeStreamRequest(ctx context.Context, request *TTSRequest) (*TTSStreamResponse, error) {
	select {
	case c.semaphore <- struct{}{}:
		defer func() { <-c.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	url := BuildURL(c.config.BaseURL, "api/generate")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	formFields := map[string]string{
		"input":           request.Input,
		"voice":           string(request.Voice),
		"generation":      uuid.New().String(),
		"vibe":            "dramatic",
		"response_format": string(request.ResponseFormat),
	}

	if request.Instructions != "" {
		formFields["prompt"] = request.Instructions
	} else {
		formFields["prompt"] = DefaultInstructions
	}

	for key, value := range formFields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("failed to write form field %s: %w", key, err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	contentType := writer.FormDataContentType()

	c.logger.Info("Generating speech for text: '%s...' with voice: %s",
		truncateString(request.Input, 50), request.Voice)

	bodyBytes := body.Bytes()

	var lastErr error
	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := ExponentialBackoff(attempt-1, 1.0, 60.0)
			c.logger.Info("Retrying request after %v (attempt %d)", delay, attempt+1)

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			lastErr = fmt.Errorf("failed to create request: %w", err)
			continue
		}
		if ctx != nil {
			req = req.WithContext(ctx)
		}

		headers := GetRealisticHeaders()
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
		req.Header.Set("Accept", "application/json, audio/*")

		if c.config.APIKey != "" {
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.config.APIKey))
		}

		req.Header[http.HeaderOrderKey] = []string{
			"accept",
			"accept-encoding",
			"accept-language",
			"cache-control",
			"content-type",
			"dnt",
			"pragma",
			"user-agent",
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = NewNetworkException(fmt.Sprintf("Request error: %v", err), attempt)
			c.logger.Warn("Request error, retrying...")
			continue
		}

		if resp.StatusCode == http.StatusOK {
			return c.processStreamResponse(resp, request)
		}

		// 非成功状态码，需要读取响应体获取错误信息
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var errorData map[string]interface{}
		_ = json.Unmarshal(respBody, &errorData)

		exception := CreateExceptionFromResponse(
			resp.StatusCode,
			errorData,
			fmt.Sprintf("TTS request failed with status %d", resp.StatusCode),
		)

		if resp.StatusCode == 400 || resp.StatusCode == 401 ||
			resp.StatusCode == 403 || resp.StatusCode == 404 {
			return nil, exception
		}

		lastErr = exception
		c.logger.Warn("Request failed with status %d, retrying...", resp.StatusCode)
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, NewTTSException("Maximum retries exceeded")
}

// processStreamResponse 处理成功的流式响应
func (c *TTSClient) processStreamResponse(
	resp *http.Response,
	request *TTSRequest,
) (*TTSStreamResponse, error) {
	contentType := resp.Header.Get("Content-Type")
	contentTypeLower := strings.ToLower(contentType)

	var actualFormat AudioFormat
	switch {
	case containsAny(contentTypeLower, "audio/mpeg", "audio/mp3"):
		actualFormat = FormatMP3
	case containsAny(contentTypeLower, "audio/wav"):
		actualFormat = FormatWAV
	case containsAny(contentTypeLower, "audio/opus"):
		actualFormat = FormatOPUS
	case containsAny(contentTypeLower, "audio/aac"):
		actualFormat = FormatAAC
	case containsAny(contentTypeLower, "audio/flac"):
		actualFormat = FormatFLAC
	default:
		actualFormat = FormatMP3
	}

	requestedFormat := request.ResponseFormat
	if actualFormat != requestedFormat {
		if MapsToWAV(string(requestedFormat)) && actualFormat == FormatWAV {
			c.logger.Debug("Format '%s' requested, returning WAV format.", requestedFormat)
		} else {
			c.logger.Warn("Requested format '%s' but received '%s' from service.",
				requestedFormat, actualFormat)
		}
	}
	if !resp.Uncompressed {
		resp.Body = http.DecompressBody(resp)
	}
	streamResp := &TTSStreamResponse{
		Body:        resp.Body,
		ContentType: contentType,
		Format:      actualFormat,
		Metadata: map[string]string{
			"status_code":      fmt.Sprintf("%d", resp.StatusCode),
			"service":          "openai.fm",
			"voice":            string(request.Voice),
			"requested_format": string(requestedFormat),
			"actual_format":    string(actualFormat),
		},
	}

	c.logger.Info("Streaming %s audio from openai.fm using voice '%s'",
		string(actualFormat), request.Voice)

	return streamResp, nil
}

// Close 关闭客户端
func (c *TTSClient) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func containsAny(s string, substrs ...string) bool {
	for _, substr := range substrs {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}
