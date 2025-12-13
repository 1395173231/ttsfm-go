package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"ttsfm-go/ttsfm"
)

type upstreamCase struct {
	body  []byte
	delay time.Duration
}

func newUpstreamTTS(t *testing.T, contentType string, cases map[string]upstreamCase) (*httptest.Server, *int32) {
	t.Helper()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			http.NotFound(w, r)
			return
		}

		atomic.AddInt32(&calls, 1)

		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, "bad multipart", http.StatusBadRequest)
			return
		}

		input := r.FormValue("input")
		c, ok := cases[input]
		if !ok {
			http.Error(w, "unexpected input", http.StatusBadRequest)
			return
		}

		if c.delay > 0 {
			time.Sleep(c.delay)
		}

		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(c.body)
	}))
	return srv, &calls
}

func newTestEngine(t *testing.T, upstreamURL string) *gin.Engine {
	t.Helper()

	cfg := DefaultServerConfig()
	cfg.Logger = &ttsfm.DefaultLogger{}
	cfg.EnableCORS = false
	cfg.EnableRateLimit = false
	cfg.AutoCombine = true
	cfg.RequestTimeout = 2 * time.Second
	cfg.TTSClientOptions = []ttsfm.ClientOption{
		ttsfm.WithBaseURL(upstreamURL),
		ttsfm.WithTimeout(2 * time.Second),
		ttsfm.WithMaxRetries(0),
		ttsfm.WithMaxConcurrent(10),
		ttsfm.WithLogger(cfg.Logger),
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv.Engine()
}

func makeWAV(pcm []byte, sampleRate uint32, numChannels, bitsPerSample uint16) []byte {
	byteRate := sampleRate * uint32(numChannels) * uint32(bitsPerSample) / 8
	blockAlign := numChannels * bitsPerSample / 8
	dataSize := uint32(len(pcm))
	fileSize := 36 + dataSize

	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], fileSize)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], numChannels)
	binary.LittleEndian.PutUint32(header[24:28], sampleRate)
	binary.LittleEndian.PutUint32(header[28:32], byteRate)
	binary.LittleEndian.PutUint16(header[32:34], blockAlign)
	binary.LittleEndian.PutUint16(header[34:36], bitsPerSample)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataSize)

	return append(header, pcm...)
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
	engine := newTestEngine(t, "http://127.0.0.1:1") // 不会被调用

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
	engine := newTestEngine(t, "http://127.0.0.1:1") // 不会被调用

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
	engine := newTestEngine(t, "http://127.0.0.1:1") // 不会被调用

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
	upstream, calls := newUpstreamTTS(t, "audio/mpeg", map[string]upstreamCase{
		"hello": {body: audio},
	})
	defer upstream.Close()

	engine := newTestEngine(t, upstream.URL)

	w := doJSONPost(t, engine, "/v1/audio/speech", map[string]any{
		"input":           "hello",
		"voice":           "alloy",
		"response_format": "mp3",
		"max_length":      4096,
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "audio/mpeg" {
		t.Fatalf("unexpected content-type: %s", got)
	}
	if got := w.Header().Get("X-Audio-Format"); got != "mp3" {
		t.Fatalf("unexpected X-Audio-Format: %s", got)
	}
	if got := w.Header().Get("X-Chunks-Combined"); got != "1" {
		t.Fatalf("unexpected X-Chunks-Combined: %s", got)
	}
	if !bytes.Equal(w.Body.Bytes(), audio) {
		t.Fatalf("unexpected body: %q", w.Body.Bytes())
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("expected upstream calls=1, got %d", atomic.LoadInt32(calls))
	}
}

func TestOpenAISpeech_LongText_AutoCombine_Stream_MP3_OK(t *testing.T) {
	ch1 := []byte("chunk1-")
	ch2 := []byte("chunk2")

	// 故意让 chunk0 更慢，模拟并发下响应乱序
	upstream, calls := newUpstreamTTS(t, "audio/mpeg", map[string]upstreamCase{
		"aaaaa.": {body: ch1, delay: 80 * time.Millisecond},
		"bbbbb.": {body: ch2, delay: 0},
	})
	defer upstream.Close()

	engine := newTestEngine(t, upstream.URL)

	w := doJSONPost(t, engine, "/v1/audio/speech", map[string]any{
		"input":           "aaaaa. bbbbb.",
		"voice":           "alloy",
		"response_format": "mp3",
		"auto_combine":    true,
		"max_length":      6,
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Chunks-Combined"); got != "2" {
		t.Fatalf("unexpected X-Chunks-Combined: %s", got)
	}
	if got := w.Header().Get("X-Audio-Format"); got != "mp3" {
		t.Fatalf("unexpected X-Audio-Format: %s", got)
	}

	expected := append(append([]byte{}, ch1...), ch2...)
	if !bytes.Equal(w.Body.Bytes(), expected) {
		t.Fatalf("unexpected body: %q", w.Body.Bytes())
	}
	if atomic.LoadInt32(calls) != 2 {
		t.Fatalf("expected upstream calls=2, got %d", atomic.LoadInt32(calls))
	}
}

func TestOpenAISpeech_LongText_AutoCombine_Stream_WAV_OK(t *testing.T) {
	pcm1 := []byte{0x01, 0x02, 0x03, 0x04}
	pcm2 := []byte{0x05, 0x06}
	wav1 := makeWAV(pcm1, 8000, 1, 16)
	wav2 := makeWAV(pcm2, 8000, 1, 16)

	// 故意让 chunk0 更慢，模拟并发下响应乱序
	upstream, calls := newUpstreamTTS(t, "audio/wav", map[string]upstreamCase{
		"aaaaa.": {body: wav1, delay: 80 * time.Millisecond},
		"bbbbb.": {body: wav2, delay: 0},
	})
	defer upstream.Close()

	engine := newTestEngine(t, upstream.URL)

	w := doJSONPost(t, engine, "/v1/audio/speech", map[string]any{
		"input":           "aaaaa. bbbbb.",
		"voice":           "alloy",
		"response_format": "wav",
		"auto_combine":    true,
		"max_length":      6,
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Chunks-Combined"); got != "2" {
		t.Fatalf("unexpected X-Chunks-Combined: %s", got)
	}
	if got := w.Header().Get("X-Audio-Format"); got != "wav" {
		t.Fatalf("unexpected X-Audio-Format: %s", got)
	}

	expected := append(append([]byte{}, wav1...), pcm2...)
	if !bytes.Equal(w.Body.Bytes(), expected) {
		t.Fatalf("unexpected body len=%d", len(w.Body.Bytes()))
	}
	if atomic.LoadInt32(calls) != 2 {
		t.Fatalf("expected upstream calls=2, got %d", atomic.LoadInt32(calls))
	}
}