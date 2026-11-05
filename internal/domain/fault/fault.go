// Package fault defines transport-independent application errors.
package fault

import "errors"

type Kind string

const (
	Invalid      Kind = "invalid"
	Gone         Kind = "gone"
	TooLarge     Kind = "too_large"
	Unauthorized Kind = "unauthorized"
	Forbidden    Kind = "forbidden"
	NotFound     Kind = "not_found"
	Conflict     Kind = "conflict"
	Precondition Kind = "precondition"
	Unsupported  Kind = "unsupported"
	Unavailable  Kind = "unavailable"
	RateLimited  Kind = "rate_limited"
	BadGateway   Kind = "bad_gateway"
	Timeout      Kind = "timeout"
	Internal     Kind = "internal"
)

type Issue struct {
	Pointer string `json:"pointer"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Error struct {
	Kind      Kind
	Code      string
	Message   string
	Retryable bool
	Issues    []Issue
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func New(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}
func As(err error) *Error {
	var f *Error
	if errors.As(err, &f) {
		return f
	}
	return New(Internal, "INTERNAL_ERROR", "服务内部错误")
}
