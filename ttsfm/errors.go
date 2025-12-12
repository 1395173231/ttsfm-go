package ttsfm

import (
	"fmt"
)

// TTSException 基础 TTS 异常
type TTSException struct {
	Code    string
	Message string
	Cause   error
}

func (e *TTSException) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s (caused by: %v)", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *TTSException) Unwrap() error {
	return e.Cause
}

// NewTTSException 创建新的 TTS 异常
func NewTTSException(message string) *TTSException {
	return &TTSException{
		Code:    "TTS_ERROR",
		Message: message,
	}
}

// APIException API 相关异常
type APIException struct {
	*TTSException
	StatusCode int
	Headers    map[string]string
}

// NewAPIException 创建新的 API 异常
func NewAPIException(message string, statusCode int) *APIException {
	return &APIException{
		TTSException: &TTSException{
			Code:    "API_ERROR",
			Message: message,
		},
		StatusCode: statusCode,
	}
}

// NetworkException 网络相关异常
type NetworkException struct {
	*TTSException
	Timeout    float64
	RetryCount int
}

// NewNetworkException 创建新的网络异常
func NewNetworkException(message string, retryCount int) *NetworkException {
	return &NetworkException{
		TTSException: &TTSException{
			Code:    "NETWORK_ERROR",
			Message: message,
		},
		RetryCount: retryCount,
	}
}

// ValidationException 验证异常
type ValidationException struct {
	*TTSException
	Field string
	Value string
}

// NewValidationException 创建新的验证异常
func NewValidationException(message, field, value string) *ValidationException {
	return &ValidationException{
		TTSException: &TTSException{
			Code:    "VALIDATION_ERROR",
			Message: message,
		},
		Field: field,
		Value: value,
	}
}

// NewValidationError 创建验证错误（别名）
func NewValidationError(message, field, value string) error {
	return NewValidationException(message, field, value)
}

// RateLimitException 速率限制异常
type RateLimitException struct {
	*APIException
	RetryAfter float64
}

// NewRateLimitException 创建新的速率限制异常
func NewRateLimitException(message string, retryAfter float64) *RateLimitException {
	return &RateLimitException{
		APIException: NewAPIException(message, 429),
		RetryAfter:   retryAfter,
	}
}

// AuthenticationException 认证异常
type AuthenticationException struct {
	*APIException
}

// NewAuthenticationException 创建新的认证异常
func NewAuthenticationException(message string) *AuthenticationException {
	return &AuthenticationException{
		APIException: NewAPIException(message, 401),
	}
}

// CreateExceptionFromResponse 根据响应创建对应的异常
func CreateExceptionFromResponse(statusCode int, errorData map[string]interface{}, defaultMessage string) error {
	message := defaultMessage

	if errorData != nil {
		if errObj, ok := errorData["error"].(map[string]interface{}); ok {
			if msg, ok := errObj["message"].(string); ok {
				message = msg
			}
		}
	}

	switch statusCode {
	case 400:
		return NewValidationException(message, "", "")
	case 401:
		return NewAuthenticationException(message)
	case 403:
		return NewAPIException(fmt.Sprintf("Forbidden: %s", message), statusCode)
	case 404:
		return NewAPIException(fmt.Sprintf("Not found: %s", message), statusCode)
	case 429:
		return NewRateLimitException(message, 0)
	case 500, 502, 503, 504:
		return NewAPIException(fmt.Sprintf("Server error: %s", message), statusCode)
	default:
		return NewAPIException(message, statusCode)
	}
}