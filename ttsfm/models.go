package ttsfm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Voice 可用的语音选项
type Voice string

const (
	VoiceAlloy   Voice = "alloy"
	VoiceAsh     Voice = "ash"
	VoiceBallad  Voice = "ballad"
	VoiceCoral   Voice = "coral"
	VoiceEcho    Voice = "echo"
	VoiceFable   Voice = "fable"
	VoiceNova    Voice = "nova"
	VoiceOnyx    Voice = "onyx"
	VoiceSage    Voice = "sage"
	VoiceShimmer Voice = "shimmer"
	VoiceVerse   Voice = "verse"
)

// ValidVoices 所有有效的语音列表
var ValidVoices = []Voice{
	VoiceAlloy, VoiceAsh, VoiceBallad, VoiceCoral, VoiceEcho,
	VoiceFable, VoiceNova, VoiceOnyx, VoiceSage, VoiceShimmer, VoiceVerse,
}

// IsValid 检查语音是否有效
func (v Voice) IsValid() bool {
	for _, valid := range ValidVoices {
		if v == valid {
			return true
		}
	}
	return false
}

// AudioFormat 支持的音频输出格式
type AudioFormat string

const (
	FormatMP3  AudioFormat = "mp3"
	FormatWAV  AudioFormat = "wav"
	FormatOPUS AudioFormat = "opus"
	FormatAAC  AudioFormat = "aac"
	FormatFLAC AudioFormat = "flac"
	FormatPCM  AudioFormat = "pcm"
)

// ValidFormats 所有有效的格式列表
var ValidFormats = []AudioFormat{
	FormatMP3, FormatWAV, FormatOPUS, FormatAAC, FormatFLAC, FormatPCM,
}

// IsValid 检查格式是否有效
func (f AudioFormat) IsValid() bool {
	for _, valid := range ValidFormats {
		if f == valid {
			return true
		}
	}
	return false
}

// ContentTypeMap 格式到 MIME 类型的映射
var ContentTypeMap = map[AudioFormat]string{
	FormatMP3:  "audio/mpeg",
	FormatOPUS: "audio/opus",
	FormatAAC:  "audio/aac",
	FormatFLAC: "audio/flac",
	FormatWAV:  "audio/wav",
	FormatPCM:  "audio/pcm",
}

// FormatFromContentType 从 MIME 类型到格式的映射
var FormatFromContentType = map[string]AudioFormat{
	"audio/mpeg": FormatMP3,
	"audio/opus": FormatOPUS,
	"audio/aac":  FormatAAC,
	"audio/flac": FormatFLAC,
	"audio/wav":  FormatWAV,
	"audio/pcm":  FormatPCM,
	"audio/mp3":  FormatMP3,
}

// GetContentType 获取音频格式的 MIME 类型
func GetContentType(format AudioFormat) string {
	if ct, ok := ContentTypeMap[format]; ok {
		return ct
	}
	return "audio/mpeg"
}

// GetFormatFromContentType 从 MIME 类型获取音频格式
func GetFormatFromContentType(contentType string) AudioFormat {
	ct := strings.Split(contentType, ";")[0]
	ct = strings.TrimSpace(ct)

	if format, ok := FormatFromContentType[ct]; ok {
		return format
	}
	return FormatMP3
}

// GetSupportedFormat 将请求的格式映射到支持的格式
func GetSupportedFormat(requestedFormat AudioFormat) AudioFormat {
	if requestedFormat == FormatMP3 {
		return FormatMP3
	}
	return FormatWAV
}

// MapsToWAV 检查格式是否映射到 WAV
func MapsToWAV(format string) bool {
	f := strings.ToLower(format)
	return f == "wav" || f == "opus" || f == "aac" || f == "flac" || f == "pcm"
}

// TTSRequest TTS 生成请求模型
type TTSRequest struct {
	Input          string      `json:"input"`
	Voice          Voice       `json:"voice"`
	ResponseFormat AudioFormat `json:"response_format"`
	Instructions   string      `json:"instructions,omitempty"`
	Model          string      `json:"model,omitempty"`
	Speed          float64     `json:"speed,omitempty"`
	MaxLength      int         `json:"-"`
	ValidateLength bool        `json:"-"`
}

// NewTTSRequest 创建新的 TTS 请求
func NewTTSRequest(input string, opts ...RequestOption) (*TTSRequest, error) {
	req := &TTSRequest{
		Input:          input,
		Voice:          VoiceAlloy,
		ResponseFormat: FormatMP3,
		MaxLength:      4096,
		ValidateLength: true,
	}

	for _, opt := range opts {
		opt(req)
	}

	if err := req.Validate(); err != nil {
		return nil, err
	}

	return req, nil
}

// RequestOption 请求选项函数类型
type RequestOption func(*TTSRequest)

// WithVoice 设置语音
func WithVoice(voice Voice) RequestOption {
	return func(r *TTSRequest) {
		r.Voice = voice
	}
}

// WithFormat 设置输出格式
func WithFormat(format AudioFormat) RequestOption {
	return func(r *TTSRequest) {
		r.ResponseFormat = format
	}
}

// WithInstructions 设置指令
func WithInstructions(instructions string) RequestOption {
	return func(r *TTSRequest) {
		r.Instructions = instructions
	}
}

// WithModel 设置模型
func WithModel(model string) RequestOption {
	return func(r *TTSRequest) {
		r.Model = model
	}
}

// WithSpeed 设置速度
func WithSpeed(speed float64) RequestOption {
	return func(r *TTSRequest) {
		r.Speed = speed
	}
}

// WithMaxLength 设置最大长度
func WithMaxLength(maxLength int) RequestOption {
	return func(r *TTSRequest) {
		r.MaxLength = maxLength
	}
}

// WithoutLengthValidation 禁用长度验证
func WithoutLengthValidation() RequestOption {
	return func(r *TTSRequest) {
		r.ValidateLength = false
	}
}

// Validate 验证请求参数
func (r *TTSRequest) Validate() error {
	if !r.Voice.IsValid() {
		return NewValidationError(
			fmt.Sprintf("Invalid voice: %s. Must be one of %v", r.Voice, ValidVoices),
			"voice",
			string(r.Voice),
		)
	}

	if !r.ResponseFormat.IsValid() {
		return NewValidationError(
			fmt.Sprintf("Invalid format: %s. Must be one of %v", r.ResponseFormat, ValidFormats),
			"response_format",
			string(r.ResponseFormat),
		)
	}

	if strings.TrimSpace(r.Input) == "" {
		return NewValidationError("Input text cannot be empty", "input", "")
	}

	if r.ValidateLength {
		textLength := len(r.Input)
		if textLength > r.MaxLength {
			return NewValidationError(
				fmt.Sprintf(
					"Input text is too long (%d characters). Maximum allowed length is %d characters. "+
						"Consider splitting your text into smaller chunks or disable length validation.",
					textLength, r.MaxLength,
				),
				"input",
				fmt.Sprintf("length=%d", textLength),
			)
		}
	}

	if r.MaxLength <= 0 {
		return NewValidationError("max_length must be a positive integer", "max_length", fmt.Sprintf("%d", r.MaxLength))
	}

	if r.Speed != 0 && (r.Speed < 0.25 || r.Speed > 4.0) {
		return NewValidationError("Speed must be between 0.25 and 4.0", "speed", fmt.Sprintf("%f", r.Speed))
	}

	return nil
}

// ToMap 将请求转换为 map
func (r *TTSRequest) ToMap() map[string]string {
	data := map[string]string{
		"input":           r.Input,
		"voice":           string(r.Voice),
		"response_format": string(r.ResponseFormat),
	}

	if r.Instructions != "" {
		data["instructions"] = r.Instructions
	}

	if r.Model != "" {
		data["model"] = r.Model
	}

	if r.Speed != 0 {
		data["speed"] = fmt.Sprintf("%f", r.Speed)
	}

	return data
}

// TTSResponse TTS 生成响应模型
type TTSResponse struct {
	AudioData   []byte            `json:"-"`
	ContentType string            `json:"content_type"`
	Format      AudioFormat       `json:"format"`
	Size        int               `json:"size"`
	Duration    float64           `json:"duration,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// SaveToFile 将音频数据保存到文件
func (r *TTSResponse) SaveToFile(filename string) (string, error) {
	expectedExt := "." + string(r.Format)

	var finalFilename string
	if strings.HasSuffix(filename, expectedExt) {
		finalFilename = filename
	} else {
		baseName := filename
		audioExts := []string{".mp3", ".wav", ".opus", ".aac", ".flac", ".pcm"}
		for _, ext := range audioExts {
			if strings.HasSuffix(baseName, ext) {
				baseName = baseName[:len(baseName)-len(ext)]
				break
			}
		}
		finalFilename = baseName + expectedExt
	}

	dir := filepath.Dir(finalFilename)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("failed to create directory: %w", err)
		}
	}

	if err := os.WriteFile(finalFilename, r.AudioData, 0o644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return finalFilename, nil
}

// TTSError TTS API 错误信息
type TTSError struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Type      string            `json:"type,omitempty"`
	Details   map[string]string `json:"details,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

// NewTTSError 创建新的 TTS 错误
func NewTTSError(code, message string) *TTSError {
	return &TTSError{
		Code:      code,
		Message:   message,
		Timestamp: time.Now(),
	}
}