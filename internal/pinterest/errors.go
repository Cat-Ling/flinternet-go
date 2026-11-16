// Package pinterest implements a conservative, unauthenticated client for
// Pinterest's public web-resource endpoints.
package pinterest

import (
	"errors"
	"fmt"
	"net/http"
)

// ErrorCode identifies the category of a client or upstream Pinterest failure.
type ErrorCode string

const (
	CodeGuestAccessBlocked    ErrorCode = "GUEST_ACCESS_BLOCKED"
	CodeChallengeRequired     ErrorCode = "CHALLENGE_REQUIRED"
	CodeRateLimited           ErrorCode = "RATE_LIMITED"
	CodeHTTPError             ErrorCode = "HTTP_ERROR"
	CodeProtocolError         ErrorCode = "PROTOCOL_ERROR"
	CodePaginationNotVerified ErrorCode = "PAGINATION_NOT_VERIFIED"
	CodeValidationError       ErrorCode = "VALIDATION_ERROR"
)

// Error represents a typed failure from the Pinterest protocol layer.
type Error struct {
	Code        ErrorCode `json:"code"`
	Message     string    `json:"message"`
	Status      int       `json:"status,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	RetryAfter  string    `json:"retry_after,omitempty"`
	Cause       error     `json:"-"`
}

func (e *Error) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("%s (HTTP %d): %s", e.Code, e.Status, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

// HTTPStatus maps a Pinterest protocol error to an appropriate REST API status code.
func (e *Error) HTTPStatus() int {
	switch e.Code {
	case CodeValidationError:
		return http.StatusBadRequest
	case CodeGuestAccessBlocked:
		if e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden {
			return e.Status
		}
		return http.StatusForbidden
	case CodeChallengeRequired:
		return http.StatusForbidden
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodePaginationNotVerified:
		return http.StatusNotImplemented
	case CodeProtocolError:
		return http.StatusBadGateway
	case CodeHTTPError:
		if e.Status >= 400 && e.Status < 600 {
			return e.Status
		}
		return http.StatusBadGateway
	default:
		return http.StatusBadGateway
	}
}

func newError(code ErrorCode, msg string, status int, contentType, retryAfter string, cause error) *Error {
	return &Error{
		Code:        code,
		Message:     msg,
		Status:      status,
		ContentType: contentType,
		RetryAfter:  retryAfter,
		Cause:       cause,
	}
}

// AsError extracts a *pinterest.Error if err wraps one.
func AsError(err error) (*Error, bool) {
	var target *Error
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}
