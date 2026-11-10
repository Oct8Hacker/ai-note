package ratelimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNextReset(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	now := time.Date(2026, 11, 10, 23, 0, 0, 0, la)
	want := time.Date(2026, 11, 11, 0, 0, 0, 0, la)
	require.True(t, NextReset(now, la).Equal(want), "got %s", NextReset(now, la))

	// A UTC instant is converted into loc before picking the day.
	utc := time.Date(2026, 11, 11, 6, 0, 0, 0, time.UTC) // 22:00 on 10 Nov in LA
	require.True(t, NextReset(utc, la).Equal(want))
}
