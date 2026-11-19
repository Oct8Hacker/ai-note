package doctor

import (
	"context"
	"errors"
	"testing"
	"time"

	"ainote/internal/llm"
	"ainote/internal/pipeline"
	"ainote/internal/pipeline/pipelinetest"
	"ainote/internal/youtube"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type probeModel struct {
	llm.Provider
	err    error
	images bool
}

func (p probeModel) Ping(context.Context) error { return p.err }
func (p probeModel) SupportsImages() bool       { return p.images }

type probeRunner struct {
	t   *testing.T
	bot bool
}

func (p probeRunner) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	switch args[0] {
	case "--version":
		require.Equal(p.t, "yt-dlp", name)
	case "-version":
		require.Equal(p.t, "ffmpeg", name)
	case "-J":
		require.Equal(p.t, "https://www.youtube.com/watch?v=jNQXAC9IVRw", args[len(args)-1])
		if p.bot {
			return nil, []byte("Sign in to confirm you're not a bot"), errors.New("exit 1")
		}
		return []byte(`{"title":"known video"}`), nil, nil
	default:
		p.t.Fatalf("unexpected arguments %v", args)
	}
	return []byte("1.0"), nil, nil
}

func checks(t *testing.T, bot bool) Checks {
	r := probeRunner{t, bot}
	return Checks{Text: probeModel{}, Vision: probeModel{images: true}, Runner: r, YT: youtube.NewClient(r, pipelinetest.NoopLimiter{}, pipelinetest.NoFaults{}, "")}
}

func TestAllOK(t *testing.T) {
	results := Run(context.Background(), checks(t, false))
	var names []string
	for _, r := range results {
		require.True(t, r.OK, r.Name)
		names = append(names, r.Name)
	}
	require.ElementsMatch(t, []string{"llm_text", "llm_vision", "yt_dlp", "ffmpeg", "youtube_reachable"}, names)
}

func TestLLMKeyRejected(t *testing.T) {
	c := checks(t, false)
	c.Text = probeModel{err: &pipeline.FatalError{Code: "LLM_CONFIG_ERROR"}}
	c.Vision = probeModel{}
	results := Run(context.Background(), c)
	require.False(t, results[0].OK)
	require.Equal(t, "LLM key rejected — check GEMINI_API_KEY (or your provider's key) and LLM_MODEL", results[0].Fix)
	require.False(t, results[1].OK)
	require.Equal(t, "LLM_VISION_MODEL must support images, or set FRAME_VERIFY=false", results[1].Fix)
}
func TestBotCheckDetected(t *testing.T) {
	results := Run(context.Background(), checks(t, true))
	r := results[len(results)-1]
	require.False(t, r.OK)
	require.Equal(t, "YouTube is asking to confirm you're not a bot — see README › Troubleshooting › YouTube cookies", r.Fix)
}
func TestPublishReadAll(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	db := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	res := []Result{{Name: "ffmpeg", OK: true}}
	Publish(ctx, db, "worker1", res, 120*time.Second)
	got, err := ReadAll(ctx, db)
	require.NoError(t, err)
	require.Equal(t, map[string][]Result{"worker1": res}, got)
	mr.FastForward(120 * time.Second)
	got, err = ReadAll(ctx, db)
	require.NoError(t, err)
	require.Empty(t, got)
}
