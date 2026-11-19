package llm

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	require.Equal(t, 269, EstimateTokens(JSONRequest{System: "abcd", Prompt: "e", Images: []Image{{}}, MaxOutputTokens: 7}))
}
