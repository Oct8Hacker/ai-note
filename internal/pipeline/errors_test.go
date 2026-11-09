package pipeline

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassify(t *testing.T) {
	require.Equal(t, ClassRateLimited, Classify(fmt.Errorf("wrap: %w", &RateLimitedError{Provider: "llm"})))
	require.Equal(t, ClassFatal, Classify(&FatalError{Code: "VIDEO_TOO_LONG"}))
	require.Equal(t, ClassRetryable, Classify(&RetryableError{Code: "NETWORK"}))
	require.Equal(t, ClassRetryable, Classify(errors.New("boom")))
	require.Equal(t, "VIDEO_TOO_LONG", ErrorCode(&FatalError{Code: "VIDEO_TOO_LONG"}))
	require.Equal(t, "NETWORK", ErrorCode(&RetryableError{Code: "NETWORK"}))
	require.Equal(t, "RATE_LIMITED", ErrorCode(&RateLimitedError{Provider: "notion"}))
	require.Equal(t, "INTERNAL", ErrorCode(errors.New("boom")))
}

func TestRetryableUnwrap(t *testing.T) {
	inner := errors.New("inner")
	require.ErrorIs(t, &RetryableError{Code: "X", Err: inner}, inner)
}
