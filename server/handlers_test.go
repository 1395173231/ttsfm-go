package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"ttsfm-go/ttsfm"
)

type mockSpeechClient struct {
	generateSpeechFunc     func(ctx context.Context, text string, opts ...ttsfm.RequestOption) (*ttsfm.TTSResponse, error)
	generateSpeechLongFunc func(ctx context.Context, text string, maxLength int, preserveWords bool, opts ...ttsfm.RequestOption) ([]*ttsfm.TTSResponse, error)
}

func (m *mockSpeechClient) GenerateSpeech(ctx context.Context, text string, opts ...ttsfm.RequestOption) (*ttsfm.TTSResponse, error) {
	if m.generateSpeechFunc != nil {
		return m.generateSpeechFunc(ctx, text, opts...)
	}
	return nil, ttsfm.NewTTSException("mock GenerateSpeech not configured")
}

func (m *mockSpeechClient) GenerateSpeechLongText(ctx context.Context, text string, maxLength int, preserveWords bool, opts ...ttsfm.RequestOption) ([]*ttsfm.TTSResponse, error) {
	if m.generateSpeechLongFunc != nil {
		return m.generateSpeechLongFunc(ctx, text, maxLength, preserveWords, opts...)
	}
	return nil, ttsfm.NewTTSException("mock GenerateSpeechLongText not configured")
}

func newTestEngine(handler *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/v1/audio/speech", handler.OpenAISpeech)
	return r
}

func doJSONPost(t *testing.T, r http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestOpenAISpeech_MissingInput(t *testing.T) {
	handler := NewHandler(&mockSpeechClient{}, &ttsfm.DefaultLogger{}, 2*time.Second, true)
	engine := newTestEngine(handler)

	w := doJSONPost(t, engine, "/v1/audio/speech", map[string]any{
		"voice":           "alloy",
		"response_format": "mp3",
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"missing_input"`)) {
		t.Fatalf("expected missing_input error, got body=%s", w.Body.String())
	}
}

func TestOpenAISpeech_InvalidVoice(t *testing.T) {
	handler := NewHandler(&mockSpeechClient{}, &ttsfm.DefaultLogger{}, 2*time.Second, true)
	engine := newTestEngine(handler)

	w := doJSONPost(t, engine, "/v1/audio/speech", map[string]any{
		"input":           "hello",
		"voice":           "not-a-voice",
		"response_format": "mp3",
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"invalid_voice"`)) {
		t.Fatalf("expected invalid_voice error, got body=%s", w.Body.String())
	}
}

func TestOpenAISpeech_InvalidFormat(t *testing.T) {
	handler := NewHandler(&mockSpeechClient{}, &ttsfm.DefaultLogger{}, 2*time.Second, true)
	engine := newTestEngine(handler)

	w := doJSONPost(t, engine, "/v1/audio/speech", map[string]any{
		"input":           "hello",
		"voice":           "alloy",
		"response_format": "nope",
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"invalid_format"`)) {
		t.Fatalf("expected invalid_format error, got body=%s", w.Body.String())
	}
}

func TestOpenAISpeech_ShortText_OK(t *testing.T) {
	audio := []byte("audio-bytes")
	mockClient := &mockSpeechClient{
		generateSpeechFunc: func(ctx context.Context, text string, opts ...ttsfm.RequestOption) (*ttsfm.TTSResponse, error) {
			if text != "hello" {
				t.Fatalf("expected text=hello, got %q", text)
			}
			return &ttsfm.TTSResponse{
				AudioData:   audio,
				ContentType: "audio/mpeg",
				Format:      ttsfm.FormatMP3,
				Size:        len(audio),
			}, nil
		},
	}

	handler := NewHandler(mockClient, &ttsfm.DefaultLogger{}, 2*time.Second, true)
	engine := newTestEngine(handler)

	w := doJSONPost(t, engine, "/v1/audio/speech", map[string]any{
		"input":           "hello",
		"voice":           "alloy",
		"response_format": "mp3",
		"max_length":      4096,
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("unexpected content-type: %s", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("X-Audio-Format") != "mp3" {
		t.Fatalf("unexpected X-Audio-Format: %s", w.Header().Get("X-Audio-Format"))
	}
	if w.Header().Get("X-Chunks-Combined") != "1" {
		t.Fatalf("unexpected X-Chunks-Combined: %s", w.Header().Get("X-Chunks-Combined"))
	}
	if !bytes.Equal(w.Body.Bytes(), audio) {
		t.Fatalf("unexpected body: %q", w.Body.Bytes())
	}
}

func TestOpenAISpeech_LongText_AutoCombine_OK(t *testing.T) {
	ch1 := []byte("chunk1-")
	ch2 := []byte("chunk2")

	mockClient := &mockSpeechClient{
		generateSpeechLongFunc: func(ctx context.Context, text string, maxLength int, preserveWords bool, opts ...ttsfm.RequestOption) ([]*ttsfm.TTSResponse, error) {
			if maxLength != 5 {
				t.Fatalf("expected maxLength=5, got %d", maxLength)
			}
			return []*ttsfm.TTSResponse{
				{
					AudioData:   ch1,
					ContentType: "audio/mpeg",
					Format:      ttsfm.FormatMP3,
					Size:        len(ch1),
				},
				{
					AudioData:   ch2,
					ContentType: "audio/mpeg",
					Format:      ttsfm.FormatMP3,
					Size:        len(ch2),
				},
			}, nil
		},
	}

	handler := NewHandler(mockClient, &ttsfm.DefaultLogger{}, 2*time.Second, true)
	engine := newTestEngine(handler)

	w := doJSONPost(t, engine, "/v1/audio/speech", map[string]any{
		"input":           "1234567890",
		"voice":           "alloy",
		"response_format": "mp3",
		"auto_combine":    true,
		"max_length":      5,
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	if w.Header().Get("X-Chunks-Combined") != "2" {
		t.Fatalf("unexpected X-Chunks-Combined: %s", w.Header().Get("X-Chunks-Combined"))
	}
	if w.Header().Get("X-Audio-Format") != "mp3" {
		t.Fatalf("unexpected X-Audio-Format: %s", w.Header().Get("X-Audio-Format"))
	}

	expected := append(append([]byte{}, ch1...), ch2...)
	if !bytes.Equal(w.Body.Bytes(), expected) {
		t.Fatalf("unexpected body: %q", w.Body.Bytes())
	}
}