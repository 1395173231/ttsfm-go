package ttsfm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewTTSClient(t *testing.T) {
	client, err := NewTTSClient()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	if client.config.BaseURL != "https://www.openai.fm" {
		t.Errorf("Unexpected base URL: %s", client.config.BaseURL)
	}
}

func TestNewTTSClientWithOptions(t *testing.T) {
	client, err := NewTTSClient(
		WithBaseURL("https://custom.example.com"),
		WithTimeout(60*time.Second),
		WithMaxRetries(5),
		WithMaxConcurrent(20),
	)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	if client.config.BaseURL != "https://custom.example.com" {
		t.Errorf("Unexpected base URL: %s", client.config.BaseURL)
	}

	if client.config.Timeout != 60*time.Second {
		t.Errorf("Unexpected timeout: %v", client.config.Timeout)
	}

	if client.config.MaxRetries != 5 {
		t.Errorf("Unexpected max retries: %d", client.config.MaxRetries)
	}
}

func TestNewTTSClientInvalidURL(t *testing.T) {
	_, err := NewTTSClient(WithBaseURL("invalid-url"))
	if err == nil {
		t.Error("Expected error for invalid URL")
	}
}

func TestTTSRequest(t *testing.T) {
	req, err := NewTTSRequest("Hello, world!")
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	if req.Input != "Hello, world!" {
		t.Errorf("Unexpected input: %s", req.Input)
	}

	if req.Voice != VoiceAlloy {
		t.Errorf("Unexpected voice: %s", req.Voice)
	}

	if req.ResponseFormat != FormatMP3 {
		t.Errorf("Unexpected format: %s", req.ResponseFormat)
	}
}

func TestTTSRequestWithOptions(t *testing.T) {
	req, err := NewTTSRequest(
		"Hello, world!",
		WithVoice(VoiceNova),
		WithFormat(FormatWAV),
		WithInstructions("Speak slowly"),
	)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	if req.Voice != VoiceNova {
		t.Errorf("Unexpected voice: %s", req.Voice)
	}

	if req.ResponseFormat != FormatWAV {
		t.Errorf("Unexpected format: %s", req.ResponseFormat)
	}

	if req.Instructions != "Speak slowly" {
		t.Errorf("Unexpected instructions: %s", req.Instructions)
	}
}

func TestTTSRequestEmptyInput(t *testing.T) {
	_, err := NewTTSRequest("")
	if err == nil {
		t.Error("Expected error for empty input")
	}
}

func TestTTSRequestTooLong(t *testing.T) {
	longText := strings.Repeat("a", 5000)

	_, err := NewTTSRequest(longText)
	if err == nil {
		t.Error("Expected error for text too long")
	}
}

func TestVoiceValidation(t *testing.T) {
	validVoices := []Voice{VoiceAlloy, VoiceAsh, VoiceBallad, VoiceCoral, VoiceEcho,
		VoiceFable, VoiceNova, VoiceOnyx, VoiceSage, VoiceShimmer, VoiceVerse}

	for _, v := range validVoices {
		if !v.IsValid() {
			t.Errorf("Voice %s should be valid", v)
		}
	}

	invalidVoice := Voice("invalid")
	if invalidVoice.IsValid() {
		t.Error("Invalid voice should not be valid")
	}
}

func TestAudioFormatValidation(t *testing.T) {
	validFormats := []AudioFormat{FormatMP3, FormatWAV, FormatOPUS, FormatAAC, FormatFLAC, FormatPCM}

	for _, f := range validFormats {
		if !f.IsValid() {
			t.Errorf("Format %s should be valid", f)
		}
	}

	invalidFormat := AudioFormat("invalid")
	if invalidFormat.IsValid() {
		t.Error("Invalid format should not be valid")
	}
}

func TestSanitizeText(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello, world!", "Hello, world!"},
		{"<p>Hello</p>", "Hello"},
		{"Test & test", "Test test"},
		{"Multiple   spaces", "Multiple spaces"},
		{"", ""},
	}

	for _, tt := range tests {
		result, err := SanitizeText(tt.input)
		if err != nil {
			t.Errorf("Unexpected error for input '%s': %v", tt.input, err)
		}
		if result != tt.expected {
			t.Errorf("SanitizeText(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestSplitTextByLength(t *testing.T) {
	text := "This is a test. This is another sentence. And one more."
	chunks := SplitTextByLength(text, 30, true)

	if len(chunks) == 0 {
		t.Error("Expected at least one chunk")
	}

	for _, chunk := range chunks {
		if len(chunk) > 30 {
			t.Errorf("Chunk too long: %s (%d chars)", chunk, len(chunk))
		}
	}
}

func TestBuildURL(t *testing.T) {
	tests := []struct {
		baseURL  string
		path     string
		expected string
	}{
		{"https://example.com", "api/test", "https://example.com/api/test"},
		{"https://example.com/", "api/test", "https://example.com/api/test"},
		{"https://example.com", "/api/test", "https://example.com/api/test"},
		{"https://example.com/", "/api/test", "https://example.com/api/test"},
	}

	for _, tt := range tests {
		result := BuildURL(tt.baseURL, tt.path)
		if result != tt.expected {
			t.Errorf("BuildURL(%q, %q) = %q, want %q", tt.baseURL, tt.path, result, tt.expected)
		}
	}
}

func TestEstimateAudioDuration(t *testing.T) {
	text := "Hello world this is a test"
	duration := EstimateAudioDuration(text, 150)

	if duration <= 0 {
		t.Errorf("Expected positive duration, got %f", duration)
	}

	emptyDuration := EstimateAudioDuration("", 150)
	if emptyDuration != 0 {
		t.Errorf("Expected 0 for empty text, got %f", emptyDuration)
	}
}

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		size     int
		expected string
	}{
		{0, "0 B"},
		{100, "100.0 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		result := FormatFileSize(tt.size)
		if result != tt.expected {
			t.Errorf("FormatFileSize(%d) = %q, want %q", tt.size, result, tt.expected)
		}
	}
}

func TestTTSResponseSaveToFile(t *testing.T) {
	response := &TTSResponse{
		AudioData: []byte("test audio data"),
		Format:    FormatMP3,
	}

	base := filepath.Join(t.TempDir(), "test_output")
	filename, err := response.SaveToFile(base)
	if err != nil {
		t.Fatalf("Failed to save file: %v", err)
	}

	expected := base + ".mp3"
	if filename != expected {
		t.Errorf("Unexpected filename: %s", filename)
	}

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("File was not created")
	}
}

func TestIntegrationGenerateSpeech(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	if os.Getenv("TTSFM_INTEGRATION") == "" {
		t.Skip("Skipping integration test; set TTSFM_INTEGRATION=1 to enable")
	}

	client, err := NewTTSClient()
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	response, err := client.GenerateSpeech(ctx, "Hello, this is a test.",
		WithVoice(VoiceAlloy),
		WithFormat(FormatMP3),
	)
	if err != nil {
		t.Fatalf("Failed to generate speech: %v", err)
	}

	if len(response.AudioData) == 0 {
		t.Error("Expected non-empty audio data")
	}

	if response.Format != FormatMP3 && response.Format != FormatWAV {
		t.Errorf("Unexpected format: %s", response.Format)
	}

	t.Logf("Generated audio: %s, duration: %.2fs",
		FormatFileSize(response.Size), response.Duration)
}