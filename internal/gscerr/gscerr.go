// Package gscerr defines the normalized, actionable error type shared by the
// auth, Search Console, and CLI layers. Error codes are part of the public
// JSON contract and must remain stable.
package gscerr

import (
	"errors"
	"fmt"
)

// Stable error codes. See docs/GSC_API.md for the taxonomy.
const (
	CodeAuthRequired                = "AUTH_REQUIRED"
	CodeAuthRevoked                 = "AUTH_REVOKED"
	CodeAuthScopeInsufficient       = "AUTH_SCOPE_INSUFFICIENT"
	CodeAuthFailed                  = "AUTH_FAILED"
	CodePropertyAccessDenied        = "PROPERTY_ACCESS_DENIED"
	CodePropertyNotFound            = "PROPERTY_NOT_FOUND"
	CodeSitemapNotFound             = "SITEMAP_NOT_FOUND"
	CodePaginationIncomplete        = "PAGINATION_INCOMPLETE"
	CodeInvalidArgument             = "INVALID_ARGUMENT"
	CodeInvalidDateRange            = "INVALID_DATE_RANGE"
	CodeInvalidDimensionCombination = "INVALID_DIMENSION_COMBINATION"
	CodeURLOutsideProperty          = "URL_OUTSIDE_PROPERTY"
	CodeQuotaExceeded               = "QUOTA_EXCEEDED"
	CodeRateLimited                 = "RATE_LIMITED"
	CodeGoogleAPIError              = "GOOGLE_API_ERROR"
	CodeNetworkError                = "NETWORK_ERROR"
	CodeConfigError                 = "CONFIG_ERROR"
	CodeInternal                    = "INTERNAL_ERROR"
)

// Exit codes used by the CLI.
const (
	ExitOK           = 0
	ExitFailure      = 1
	ExitUsage        = 2
	ExitAuthRequired = 3
)

// Error is a normalized, actionable error.
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Action    string `json:"action,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
	// Cause is the underlying error; it is never serialized.
	Cause error `json:"-"`
}

func (e *Error) Error() string {
	if e.Action != "" {
		return fmt.Sprintf("%s: %s %s", e.Code, e.Message, e.Action)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the underlying cause to errors.Is/As.
func (e *Error) Unwrap() error { return e.Cause }

// New builds an Error.
func New(code, message, action string) *Error {
	return &Error{Code: code, Message: message, Action: action}
}

// Wrap builds an Error that retains the cause.
func Wrap(cause error, code, message, action string) *Error {
	return &Error{Code: code, Message: message, Action: action, Cause: cause}
}

// From converts any error into a normalized *Error. Unknown errors become
// INTERNAL_ERROR so that JSON consumers always receive a structured failure.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: CodeInternal, Message: err.Error(), Cause: err}
}

// ExitCode maps an error code to the process exit status.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	switch From(err).Code {
	case CodeAuthRequired, CodeAuthRevoked, CodeAuthScopeInsufficient:
		return ExitAuthRequired
	case CodeInvalidArgument, CodeInvalidDateRange, CodeInvalidDimensionCombination, CodeURLOutsideProperty:
		return ExitUsage
	default:
		return ExitFailure
	}
}

// AuthRequired is the canonical "not logged in" error.
func AuthRequired() *Error {
	return New(CodeAuthRequired,
		"Google Search Console authentication is required.",
		"Run `gsc auth login`.")
}
