package pipeline

import (
	"errors"
	"time"
)

// RateLimitedError reports provider throttling; Provider is "llm" | "notion" | "youtube".
type RateLimitedError struct {
	Provider   string
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return "rate limited by " + e.Provider + " (retry after " + e.RetryAfter.String() + ")"
}

// RetryableError marks a failure worth retrying; Code is the machine code and Err the cause.
type RetryableError struct {
	Code string
	Err  error
}

func (e *RetryableError) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Err.Error()
	}
	return e.Code
}

func (e *RetryableError) Unwrap() error { return e.Err }

// FatalError marks a failure that must not be retried; Msg optionally elaborates on Code.
type FatalError struct{ Code, Msg string }

func (e *FatalError) Error() string {
	if e.Msg != "" {
		return e.Code + ": " + e.Msg
	}
	return e.Code
}

// ErrorClass is the retry classification of an error, as returned by Classify.
type ErrorClass int

// Error classes: retryable errors are retried, rate-limited ones are deferred, fatal ones fail the job.
const (
	ClassRetryable ErrorClass = iota
	ClassRateLimited
	ClassFatal
)

// Classify maps an error to its class; unknown errors are retryable.
func Classify(err error) ErrorClass {
	var rl *RateLimitedError
	var fe *FatalError
	switch {
	case errors.As(err, &rl):
		return ClassRateLimited
	case errors.As(err, &fe):
		return ClassFatal
	}
	return ClassRetryable
}

// ErrorCode returns the machine code for err; unknown errors are "INTERNAL".
func ErrorCode(err error) string {
	var rl *RateLimitedError
	var fe *FatalError
	var re *RetryableError
	switch {
	case errors.As(err, &rl):
		return "RATE_LIMITED"
	case errors.As(err, &fe):
		return fe.Code
	case errors.As(err, &re):
		return re.Code
	}
	return "INTERNAL"
}
