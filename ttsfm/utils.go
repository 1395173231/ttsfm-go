package ttsfm

import (
	"fmt"
	"math"
	"math/rand"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// UserAgents 常用的 User-Agent 列表
var UserAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:122.0) Gecko/20100101 Firefox/122.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15",
}

// GetUserAgent 获取随机的 User-Agent
func GetUserAgent() string {
	return UserAgents[rand.Intn(len(UserAgents))]
}

// AcceptLanguages 常用的 Accept-Language 列表
var AcceptLanguages = []string{
	"en-US,en;q=0.9",
	"en-GB,en;q=0.8",
	"en-CA,en;q=0.7",
}

// GetRealisticHeaders 生成真实的 HTTP 请求头
func GetRealisticHeaders() map[string]string {
	userAgent := GetUserAgent()

	headers := map[string]string{
		"Accept":          "application/json, audio/*",
		"Accept-Encoding": "gzip, deflate, br",
		"Accept-Language": AcceptLanguages[rand.Intn(len(AcceptLanguages))],
		"Cache-Control":   "no-cache",
		"DNT":             "1",
		"Pragma":          "no-cache",
		"User-Agent":      userAgent,
	}

	if strings.Contains(strings.ToLower(userAgent), "chrome") {
		re := regexp.MustCompile(`Chrome/(\d+)`)
		matches := re.FindStringSubmatch(userAgent)
		version := "121"
		if len(matches) > 1 {
			version = matches[1]
		}

		platforms := []string{`"Windows"`, `"macOS"`, `"Linux"`}

		headers["Sec-Ch-Ua"] = fmt.Sprintf(`"Google Chrome";v="%s", "Chromium";v="%s", "Not A(Brand";v="99"`, version, version)
		headers["Sec-Ch-Ua-Mobile"] = "?0"
		headers["Sec-Ch-Ua-Platform"] = platforms[rand.Intn(len(platforms))]
		headers["Sec-Fetch-Dest"] = "empty"
		headers["Sec-Fetch-Mode"] = "cors"
		headers["Sec-Fetch-Site"] = "same-origin"
	}

	if rand.Float32() < 0.5 {
		headers["Upgrade-Insecure-Requests"] = "1"
	}

	return headers
}

// ValidateTextLength 验证文本长度
func ValidateTextLength(text string, maxLength int) error {
	if text == "" {
		return nil
	}

	textLength := len(text)
	if textLength > maxLength {
		return fmt.Errorf(
			"text is too long (%d characters). Maximum allowed length is %d characters. "+
				"TTS models typically support up to 4096 characters per request",
			textLength, maxLength,
		)
	}

	return nil
}

// SplitTextByLength 按长度分割文本
func SplitTextByLength(text string, maxLength int, preserveWords bool) []string {
	if text == "" {
		return nil
	}

	if len(text) <= maxLength {
		return []string{text}
	}

	var chunks []string

	if preserveWords {
		sentences := splitBySentences(text)
		currentChunk := ""

		for _, sentence := range sentences {
			sentence = strings.TrimSpace(sentence)
			if sentence == "" {
				continue
			}

			if !strings.HasSuffix(sentence, ".") && !strings.HasSuffix(sentence, "!") && !strings.HasSuffix(sentence, "?") {
				sentence += "."
			}

			testChunk := currentChunk
			if testChunk != "" {
				testChunk += " "
			}
			testChunk += sentence

			if len(testChunk) <= maxLength {
				currentChunk = testChunk
			} else {
				if currentChunk != "" {
					chunks = append(chunks, strings.TrimSpace(currentChunk))
				}

				if len(sentence) > maxLength {
					wordChunks := splitByWords(sentence, maxLength)
					chunks = append(chunks, wordChunks...)
					currentChunk = ""
				} else {
					currentChunk = sentence
				}
			}
		}

		if currentChunk != "" {
			chunks = append(chunks, strings.TrimSpace(currentChunk))
		}
	} else {
		for i := 0; i < len(text); i += maxLength {
			end := i + maxLength
			if end > len(text) {
				end = len(text)
			}
			chunks = append(chunks, text[i:end])
		}
	}

	result := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk) != "" {
			result = append(result, chunk)
		}
	}

	return result
}

func splitBySentences(text string) []string {
	re := regexp.MustCompile(`[.!?]+`)
	parts := re.Split(text, -1)

	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}

	return result
}

func splitByWords(text string, maxLength int) []string {
	words := strings.Fields(text)
	var chunks []string
	currentChunk := ""

	for _, word := range words {
		testChunk := currentChunk
		if testChunk != "" {
			testChunk += " "
		}
		testChunk += word

		if len(testChunk) <= maxLength {
			currentChunk = testChunk
		} else {
			if currentChunk != "" {
				chunks = append(chunks, currentChunk)
			}

			if len(word) > maxLength {
				for i := 0; i < len(word); i += maxLength {
					end := i + maxLength
					if end > len(word) {
						end = len(word)
					}
					chunks = append(chunks, word[i:end])
				}
				currentChunk = ""
			} else {
				currentChunk = word
			}
		}
	}

	if currentChunk != "" {
		chunks = append(chunks, currentChunk)
	}

	return chunks
}

// SanitizeText 清理文本
func SanitizeText(text string) (string, error) {
	if text == "" {
		return "", nil
	}

	if len(text) > 50000 {
		return "", fmt.Errorf("input text too long for sanitization (max 50000 characters)")
	}

	var result strings.Builder
	result.Grow(len(text))

	i := 0
	for i < len(text) {
		if text[i] == '<' {
			j := i + 1
			for j < len(text) && text[j] != '>' {
				j++
			}
			if j < len(text) {
				i = j + 1
			} else {
				result.WriteByte(text[i])
				i++
			}
		} else if text[i] == '&' {
			j := i + 1
			for j < len(text) && j < i+10 && !strings.ContainsAny(string(text[j]), " \t\n\r<>&") {
				j++
			}
			if j < len(text) && text[j] == ';' {
				i = j + 1
			} else {
				result.WriteByte(' ')
				i++
			}
		} else {
			char := text[i]
			if char == '"' || char == '\'' || char == '`' {
				result.WriteByte('"')
			} else if char != '<' && char != '>' {
				result.WriteByte(char)
			}
			i++
		}
	}

	sanitized := regexp.MustCompile(`[ \t\n\r\f\v]+`).ReplaceAllString(result.String(), " ")

	return strings.TrimSpace(sanitized), nil
}

// ValidateURL 验证 URL 格式
func ValidateURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return parsed.Scheme != "" && parsed.Host != ""
}

// BuildURL 构建完整的 URL
func BuildURL(baseURL, path string) string {
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	if strings.HasPrefix(path, "/") {
		path = path[1:]
	}
	return baseURL + path
}

// GetRandomDelay 获取带抖动的随机延迟
func GetRandomDelay(minDelay, maxDelay float64) time.Duration {
	baseDelay := minDelay + rand.Float64()*(maxDelay-minDelay)
	jitter := 0.1 + rand.Float64()*0.4
	return time.Duration((baseDelay + jitter) * float64(time.Second))
}

// ExponentialBackoff 计算指数退避延迟
func ExponentialBackoff(attempt int, baseDelay, maxDelay float64) time.Duration {
	delay := baseDelay * math.Pow(2, float64(attempt))
	jitter := (0.1 + rand.Float64()*0.2) * delay
	total := delay + jitter
	if total > maxDelay {
		total = maxDelay
	}
	return time.Duration(total * float64(time.Second))
}

// LoadConfigFromEnv 从环境变量加载配置
func LoadConfigFromEnv(prefix string) map[string]string {
	config := make(map[string]string)

	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		value := parts[1]

		if strings.HasPrefix(key, prefix) {
			configKey := strings.ToLower(key[len(prefix):])
			config[configKey] = value
		}
	}

	return config
}

// EstimateAudioDuration 根据文本长度估算音频时长
func EstimateAudioDuration(text string, wordsPerMinute float64) float64 {
	if text == "" {
		return 0
	}

	if wordsPerMinute == 0 {
		wordsPerMinute = 150
	}

	wordCount := len(strings.Fields(text))
	duration := (float64(wordCount) / wordsPerMinute) * 60.0

	return duration * 1.1
}

// FormatFileSize 格式化文件大小
func FormatFileSize(sizeBytes int) string {
	if sizeBytes == 0 {
		return "0 B"
	}

	sizeNames := []string{"B", "KB", "MB", "GB"}
	i := 0
	size := float64(sizeBytes)

	for size >= 1024 && i < len(sizeNames)-1 {
		size /= 1024
		i++
	}

	return fmt.Sprintf("%.1f %s", size, sizeNames[i])
}

// DefaultInstructions 默认的语音指令
const DefaultInstructions = `Affect/personality: Natural and clear

Tone: Friendly and professional, creating a pleasant listening experience.

Pronunciation: Clear, articulate, and steady, ensuring each word is easily understood while maintaining a natural, conversational flow.

Pause: Brief, purposeful pauses between sentences to allow time for the listener to process the information.

Emotion: Warm and engaging, conveying the intended message effectively.`

//Affect/personality: A cheerful guide
//
//Tone: Friendly, clear, and reassuring, creating a calm atmosphere and making the listener feel confident and comfortable.
//
//Pronunciation: Clear, articulate, and steady, ensuring each instruction is easily understood while maintaining a natural, conversational flow.
//
//Pause: Brief, purposeful pauses after key instructions (e.g., "cross the street" and "turn right") to allow time for the listener to process the information and follow along.
//
//Emotion: Warm and supportive, conveying empathy and care, ensuring the listener feels guided and safe throughout the journey.
