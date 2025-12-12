package ttsfm

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
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

	"github.com/andybalholm/brotli"
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

// GenerateSpeech 生成语音
func (c *TTSClient) GenerateSpeech(ctx context.Context, text string, opts ...RequestOption) (*TTSResponse, error) {
	sanitizedText, err := SanitizeText(text)
	if err != nil {
		return nil, err
	}

	request, err := NewTTSRequest(sanitizedText, opts...)
	if err != nil {
		return nil, err
	}

	return c.makeRequest(ctx, request)
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
			resp, err := c.makeRequest(ctx, request)
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
	return c.makeRequest(ctx, request)
}

// makeRequest 执行实际的 HTTP 请求（tls-client）
func (c *TTSClient) makeRequest(ctx context.Context, request *TTSRequest) (*TTSResponse, error) {
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
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
		req.Header.Set("Accept", "application/json, audio/*")

		if c.config.APIKey != "" {
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.config.APIKey))
		}

		// 尽量固定 header 顺序（更接近浏览器）
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

		respBody, err := io.ReadAll(http.DecompressBody(resp))
		resp.Body.Close()
		if err != nil {
			lastErr = NewNetworkException(fmt.Sprintf("Failed to read response: %v", err), attempt)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			return c.processResponse(resp, respBody, request)
		}

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

func readAndDecompress(resp *http.Response) ([]byte, error) {
	encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))

	var reader io.Reader = resp.Body
	var closeFn func() error

	switch encoding {
	case "gzip":
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip reader error: %w", err)
		}
		closeFn = gr.Close
		reader = gr
	case "deflate":
		dr := flate.NewReader(resp.Body)
		closeFn = dr.Close
		reader = dr
	case "br":
		reader = brotli.NewReader(resp.Body)
	case "", "identity":
	default:
	}

	data, err := io.ReadAll(reader)
	if closeFn != nil {
		_ = closeFn()
	}
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}
	return data, nil
}

// processResponse 处理成功的响应
func (c *TTSClient) processResponse(
	resp *http.Response,
	body []byte,
	request *TTSRequest,
) (*TTSResponse, error) {
	contentType := resp.Header.Get("Content-Type")
	contentTypeLower := strings.ToLower(contentType)

	if len(body) == 0 {
		return nil, NewAPIException("Received empty audio data from openai.fm", resp.StatusCode)
	}

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

	estimatedDuration := EstimateAudioDuration(request.Input, 150)

	requestedFormat := request.ResponseFormat
	if actualFormat != requestedFormat {
		if MapsToWAV(string(requestedFormat)) && actualFormat == FormatWAV {
			c.logger.Debug("Format '%s' requested, returning WAV format.", requestedFormat)
		} else {
			c.logger.Warn("Requested format '%s' but received '%s' from service.",
				requestedFormat, actualFormat)
		}
	}

	ttsResponse := &TTSResponse{
		AudioData:   body,
		ContentType: contentType,
		Format:      actualFormat,
		Size:        len(body),
		Duration:    estimatedDuration,
		Metadata: map[string]string{
			"status_code":      fmt.Sprintf("%d", resp.StatusCode),
			"service":          "openai.fm",
			"voice":            string(request.Voice),
			"requested_format": string(requestedFormat),
			"actual_format":    string(actualFormat),
		},
	}

	c.logger.Info("Successfully generated %s of %s audio from openai.fm using voice '%s'",
		FormatFileSize(len(body)), string(actualFormat), request.Voice)

	return ttsResponse, nil
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
