package ttsfm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
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

const defaultLongTextStreamMaxConcurrent = 3
const defaultLongTextStreamChunkBufferSize = 32 * 1024

// LongTextStreamConfig 长文本流式配置
type LongTextStreamConfig struct {
	// MaxConcurrent 单个长文本请求的最大并发数（默认 3）
	MaxConcurrent int
	// ChunkBufferSize 单个 chunk 的预读/拷贝缓冲大小（默认 32KB）
	ChunkBufferSize int
}

// DefaultLongTextStreamConfig 默认配置
func DefaultLongTextStreamConfig() *LongTextStreamConfig {
	return &LongTextStreamConfig{
		MaxConcurrent:   defaultLongTextStreamMaxConcurrent,
		ChunkBufferSize: defaultLongTextStreamChunkBufferSize,
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

	clientProfileList := []profiles.ClientProfile{
		profiles.Safari_IOS_18_0,
		profiles.Chrome_133,
		profiles.Safari_IOS_17_0,
		profiles.Chrome_131,
		profiles.Firefox_135,
		profiles.Safari_Ipad_15_6,
	}
	profile := clientProfileList[rand.Intn(len(clientProfileList))]
	tlsOptions := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(timeoutSeconds),
		tls_client.WithClientProfile(profile),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(jar),
		tls_client.WithForceHttp1(),
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

// GenerateSpeechLongTextStream 将长文本切分后按 chunk 顺序拉取上游音频流并拼接为一个连续流。
// 相比 GenerateSpeechLongText/GenerateSpeechBatch：
// - 不会把所有音频一次性读入内存（避免 io.ReadAll + [][]byte）
// - 能更早向下游输出首段数据，降低等待时间
func (c *TTSClient) GenerateSpeechLongTextStream(
	ctx context.Context,
	text string,
	maxLength int,
	preserveWords bool,
	opts ...RequestOption,
) (*TTSStreamResponse, error) {
	cleanText, err := SanitizeText(text)
	if err != nil {
		return nil, err
	}

	chunks := SplitTextByLength(cleanText, maxLength, preserveWords)
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no valid text chunks found after processing")
	}

	firstReq, err := NewTTSRequest(chunks[0], append(opts, WithoutLengthValidation())...)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for chunk 0: %w", err)
	}

	firstResp, err := c.GenerateSpeechFromRequestStream(ctx, firstReq)
	if err != nil {
		return nil, fmt.Errorf("chunk 0: %w", err)
	}

	pipeReader, pipeWriter := io.Pipe()

	streamMeta := map[string]string{
		"chunks_total": fmt.Sprintf("%d", len(chunks)),
	}

	out := &TTSStreamResponse{
		Body:        pipeReader,
		ContentType: firstResp.ContentType,
		Format:      firstResp.Format,
		Metadata:    streamMeta,
	}

	go func() {
		defer pipeWriter.Close()

		writeErr := func() error {
			// chunk 0：完整写入（包含容器头/ID3）
			_, err := io.Copy(pipeWriter, firstResp.Body)
			_ = firstResp.Close()
			if err != nil {
				return err
			}

			// chunk >= 1：根据格式做“跳头/跳标签”处理
			for i := 1; i < len(chunks); i++ {
				req, err := NewTTSRequest(chunks[i], append(opts, WithoutLengthValidation())...)
				if err != nil {
					return fmt.Errorf("failed to create request for chunk %d: %w", i, err)
				}

				sr, err := c.GenerateSpeechFromRequestStream(ctx, req)
				if err != nil {
					return fmt.Errorf("chunk %d: %w", i, err)
				}

				var copyErr error
				switch out.Format {
				case FormatMP3:
					_, copyErr = CopyMP3Stream(pipeWriter, sr.Body, true)
				case FormatWAV:
					_, copyErr = CopyWAVDataStream(pipeWriter, sr.Body)
				default:
					_, copyErr = io.Copy(pipeWriter, sr.Body)
				}

				_ = sr.Close()
				if copyErr != nil {
					return fmt.Errorf("chunk %d copy: %w", i, copyErr)
				}
			}

			return nil
		}()

		if writeErr != nil {
			_ = pipeWriter.CloseWithError(writeErr)
		}
	}()

	return out, nil
}

// GenerateSpeechLongTextStreamConcurrent 并发流式处理长文本（按序输出，流式零 ReadAll，小缓存）
//
// - 为单个长文本请求限制并发数（默认 3）
// - 输出严格按原 chunk 顺序
// - worker 侧将上游响应流写入各自的 io.Pipe，天然背压保证不会在内存中堆积完整音频
func (c *TTSClient) GenerateSpeechLongTextStreamConcurrent(
	ctx context.Context,
	text string,
	maxLength int,
	preserveWords bool,
	config *LongTextStreamConfig,
	opts ...RequestOption,
) (*TTSStreamResponse, error) {
	if config == nil {
		config = DefaultLongTextStreamConfig()
	}

	bufSize := config.ChunkBufferSize
	if bufSize <= 0 {
		bufSize = defaultLongTextStreamChunkBufferSize
	}

	cleanText, err := SanitizeText(text)
	if err != nil {
		return nil, err
	}

	chunks := SplitTextByLength(cleanText, maxLength, preserveWords)
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no valid text chunks found after processing")
	}

	if len(chunks) == 1 {
		req, err := NewTTSRequest(chunks[0], append(opts, WithoutLengthValidation())...)
		if err != nil {
			return nil, err
		}
		return c.GenerateSpeechFromRequestStream(ctx, req)
	}

	maxConc := config.MaxConcurrent
	if maxConc <= 0 {
		maxConc = defaultLongTextStreamMaxConcurrent
	}
	if maxConc > len(chunks) {
		maxConc = len(chunks)
	}
	// 同时也要受全局并发限制（semaphore）约束，避免死锁
	if c.config != nil && c.config.MaxConcurrent > 0 && maxConc > c.config.MaxConcurrent {
		maxConc = c.config.MaxConcurrent
	}
	if maxConc <= 0 {
		maxConc = 1
	}

	ctx, cancel := context.WithCancel(ctx)

	type chunkPipe struct {
		r *io.PipeReader
		w *io.PipeWriter
	}

	pipes := make([]chunkPipe, len(chunks))
	for i := 1; i < len(chunks); i++ {
		pr, pw := io.Pipe()
		pipes[i] = chunkPipe{r: pr, w: pw}
	}

	// 先发 chunk0：输出必须包含第一个 chunk 的容器头/ID3
	firstReq, err := NewTTSRequest(chunks[0], append(opts, WithoutLengthValidation())...)
	if err != nil {
		cancel()
		for i := 1; i < len(chunks); i++ {
			_ = pipes[i].r.Close()
		}
		return nil, fmt.Errorf("failed to create request for chunk 0: %w", err)
	}

	firstResp, err := c.GenerateSpeechFromRequestStream(ctx, firstReq)
	if err != nil {
		cancel()
		for i := 1; i < len(chunks); i++ {
			_ = pipes[i].r.Close()
		}
		return nil, fmt.Errorf("chunk 0: %w", err)
	}

	outReader, outWriter := io.Pipe()

	out := &TTSStreamResponse{
		Body:        outReader,
		ContentType: firstResp.ContentType,
		Format:      firstResp.Format,
		Metadata: map[string]string{
			"chunks_total": fmt.Sprintf("%d", len(chunks)),
			"concurrency":  fmt.Sprintf("%d", maxConc),
		},
	}

	var bufPool sync.Pool
	bufPool.New = func() any { return make([]byte, bufSize) }

	jobs := make(chan int)
	var wg sync.WaitGroup

	workerCount := maxConc
	if workerCount > len(chunks)-1 {
		workerCount = len(chunks) - 1
	}
	if workerCount < 1 {
		workerCount = 1
	}

	wg.Add(workerCount)
	for w := 0; w < workerCount; w++ {
		// GenerateSpeechLongTextStreamConcurrent 中的 worker 部分
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if ctx.Err() != nil {
					return
				}

				pw := pipes[idx].w
				if pw == nil {
					cancel()
					return
				}

				req, err := NewTTSRequest(chunks[idx], append(opts, WithoutLengthValidation())...)
				if err != nil {
					_ = pw.CloseWithError(fmt.Errorf("failed to create request for chunk %d: %w", idx, err))
					cancel()
					return
				}

				sr, err := c.GenerateSpeechFromRequestStream(ctx, req)
				if err != nil {
					_ = pw.CloseWithError(fmt.Errorf("chunk %d: %w", idx, err))
					cancel()
					return
				}

				buf := bufPool.Get().([]byte)

				var copyErr error
				// 使用实际返回的格式，而不是 out.Format
				switch sr.Format {
				case FormatMP3:
					_, copyErr = CopyMP3StreamWithBuffer(pw, sr.Body, true, buf)
				case FormatWAV:
					_, copyErr = CopyWAVDataStreamWithBuffer(pw, sr.Body, buf)
				default:
					_, copyErr = io.CopyBuffer(pw, sr.Body, buf)
				}

				bufPool.Put(buf)
				_ = sr.Close()

				if copyErr != nil {
					_ = pw.CloseWithError(fmt.Errorf("chunk %d copy: %w", idx, copyErr))
					cancel()
					return
				}

				_ = pw.Close()
			}
		}()

	}

	go func() {
		defer close(jobs)
		for i := 1; i < len(chunks); i++ {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		fail := func(err error) {
			_ = outWriter.CloseWithError(err)
			cancel()
			for i := 1; i < len(chunks); i++ {
				if pipes[i].r != nil {
					_ = pipes[i].r.Close()
				}
			}
		}

		defer func() {
			cancel()
			wg.Wait()
			_ = outWriter.Close()
		}()

		buf := bufPool.Get().([]byte)
		defer bufPool.Put(buf)

		// 写 chunk0（完整输出）
		_, err := io.CopyBuffer(outWriter, firstResp.Body, buf)
		_ = firstResp.Close()
		if err != nil {
			fail(fmt.Errorf("chunk 0 write: %w", err))
			return
		}

		// 按序写 chunk1..n
		for i := 1; i < len(chunks); i++ {
			if pipes[i].r == nil {
				fail(fmt.Errorf("chunk %d pipe missing", i))
				return
			}
			_, err := io.CopyBuffer(outWriter, pipes[i].r, buf)
			_ = pipes[i].r.Close()
			if err != nil {
				fail(fmt.Errorf("chunk %d write: %w", i, err))
				return
			}
		}
	}()

	return out, nil
}

// GenerateSpeechBatch 批量生成语音（受限并发 worker pool）
//
// 旧实现会为每个 request 启动一个 goroutine；当 requests 很多时会造成 goroutine 数暴涨，
// 并且一旦 resp.Body 被 io.ReadAll，内存峰值=并发数×单段音频大小。
// 这里使用固定 worker 数（<= MaxConcurrent）来限制并发与瞬时内存压力。
func (c *TTSClient) GenerateSpeechBatch(ctx context.Context, requests []*TTSRequest) ([]*TTSResponse, error) {
	if len(requests) == 0 {
		return nil, nil
	}

	workerCount := c.config.MaxConcurrent
	if workerCount <= 0 {
		workerCount = 1
	}
	if workerCount > len(requests) {
		workerCount = len(requests)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type job struct {
		index   int
		request *TTSRequest
	}

	jobs := make(chan job)
	responses := make([]*TTSResponse, len(requests))
	errs := make([]error, len(requests))

	var wg sync.WaitGroup
	wg.Add(workerCount)

	for w := 0; w < workerCount; w++ {
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					return
				}
				resp, err := c.GenerateSpeechFromRequest(ctx, j.request)
				if err != nil {
					errs[j.index] = err
					cancel()
					return
				}
				responses[j.index] = resp
			}
		}()
	}

	for i, req := range requests {
		if ctx.Err() != nil {
			break
		}
		select {
		case jobs <- job{index: i, request: req}:
		case <-ctx.Done():
			break
		}
	}
	close(jobs)

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
		if request.ResponseFormat == FormatMP3 {
			req.Header.Set("Accept", "audio/mpeg")
		} else {
			req.Header.Set("Accept", "application/json, audio/*, */*;q=0.9")
		}

		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")

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
