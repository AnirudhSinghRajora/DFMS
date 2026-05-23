// Package errors provides standardized error types for the DFMS API.
// All API errors follow a consistent JSON structure with error codes,
// human-readable messages, and optional detail payloads.
package errors

import (
	"fmt"
	"net/http"
)

// Error codes organized by domain.
const (
	// Auth errors
	CodeAuthInvalidCredentials = "AUTH_INVALID_CREDENTIALS"
	CodeAuthTokenExpired       = "AUTH_TOKEN_EXPIRED"
	CodeAuthTokenInvalid       = "AUTH_TOKEN_INVALID"
	CodeAuthTokenMissing       = "AUTH_TOKEN_MISSING"
	CodeAuthForbidden          = "AUTH_FORBIDDEN"
	CodeAuthUserExists         = "AUTH_USER_EXISTS"

	// File errors
	CodeFileNotFound     = "FILE_NOT_FOUND"
	CodeFileAlreadyExists = "FILE_ALREADY_EXISTS"
	CodeFileUploadFailed = "FILE_UPLOAD_FAILED"
	CodeFileDownloadFailed = "FILE_DOWNLOAD_FAILED"
	CodeFileTooLarge     = "FILE_TOO_LARGE"

	// Storage errors
	CodeStorageNodeUnavailable = "STORAGE_NODE_UNAVAILABLE"
	CodeStorageWriteFailed     = "STORAGE_WRITE_FAILED"
	CodeStorageReadFailed      = "STORAGE_READ_FAILED"
	CodeStorageIntegrityFailed = "STORAGE_INTEGRITY_FAILED"

	// Quota errors
	CodeQuotaExceeded = "QUOTA_EXCEEDED"

	// Rate limit errors
	CodeRateLimitExceeded = "RATE_LIMIT_EXCEEDED"

	// Validation errors
	CodeValidationFailed = "VALIDATION_FAILED"

	// Internal errors
	CodeInternalError = "INTERNAL_ERROR"
	CodeServiceUnavailable = "SERVICE_UNAVAILABLE"
)

// APIError represents a structured API error response.
type APIError struct {
	Code       string      `json:"code"`
	Message    string      `json:"message"`
	StatusCode int         `json:"-"`
	Details    interface{} `json:"details,omitempty"`
	Err        error       `json:"-"`
}

// Error implements the error interface.
func (e *APIError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap returns the underlying error for errors.Is/As support.
func (e *APIError) Unwrap() error {
	return e.Err
}

// ErrorResponse is the JSON structure returned to API clients.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody is the inner error structure.
type ErrorBody struct {
	Code      string      `json:"code"`
	Message   string      `json:"message"`
	RequestID string      `json:"request_id,omitempty"`
	Details   interface{} `json:"details,omitempty"`
}

// ToResponse converts an APIError to an ErrorResponse for JSON serialization.
func (e *APIError) ToResponse(requestID string) ErrorResponse {
	return ErrorResponse{
		Error: ErrorBody{
			Code:      e.Code,
			Message:   e.Message,
			RequestID: requestID,
			Details:   e.Details,
		},
	}
}

// ── Constructors ────────────────────────────────────────────

// NewBadRequest creates a 400 validation error.
func NewBadRequest(message string) *APIError {
	return &APIError{Code: CodeValidationFailed, Message: message, StatusCode: http.StatusBadRequest}
}

// NewUnauthorized creates a 401 authentication error.
func NewUnauthorized(code, message string) *APIError {
	return &APIError{Code: code, Message: message, StatusCode: http.StatusUnauthorized}
}

// NewForbidden creates a 403 authorization error.
func NewForbidden(message string) *APIError {
	return &APIError{Code: CodeAuthForbidden, Message: message, StatusCode: http.StatusForbidden}
}

// NewNotFound creates a 404 not found error.
func NewNotFound(code, message string) *APIError {
	return &APIError{Code: code, Message: message, StatusCode: http.StatusNotFound}
}

// NewConflict creates a 409 conflict error.
func NewConflict(code, message string) *APIError {
	return &APIError{Code: code, Message: message, StatusCode: http.StatusConflict}
}

// NewTooLarge creates a 413 payload too large error.
func NewTooLarge(message string) *APIError {
	return &APIError{Code: CodeFileTooLarge, Message: message, StatusCode: http.StatusRequestEntityTooLarge}
}

// NewRateLimited creates a 429 rate limit error.
func NewRateLimited(message string) *APIError {
	return &APIError{Code: CodeRateLimitExceeded, Message: message, StatusCode: http.StatusTooManyRequests}
}

// NewInternal creates a 500 internal server error.
// The internal err is logged but not exposed to the client.
func NewInternal(err error) *APIError {
	return &APIError{
		Code:       CodeInternalError,
		Message:    "An internal error occurred. Please try again later.",
		StatusCode: http.StatusInternalServerError,
		Err:        err,
	}
}

// NewServiceUnavailable creates a 503 service unavailable error.
func NewServiceUnavailable(message string) *APIError {
	return &APIError{Code: CodeServiceUnavailable, Message: message, StatusCode: http.StatusServiceUnavailable}
}

// Wrap wraps an existing error with an APIError.
func Wrap(err error, apiErr *APIError) *APIError {
	apiErr.Err = err
	return apiErr
}
