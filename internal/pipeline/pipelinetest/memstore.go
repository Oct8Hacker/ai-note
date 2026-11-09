// Package pipelinetest provides in-memory fakes for pipeline stage tests.
package pipelinetest

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"ainote/internal/pipeline"
)

// MemStore is a concurrency-safe in-memory CheckpointStore. Values are
// deep-copied via JSON on save/load, like a real database.
type MemStore struct {
	mu        sync.Mutex
	artifacts map[string][]byte
	frames    map[uuid.UUID]map[int]pipeline.Frame
	warnings  map[uuid.UUID][]pipeline.Warning
	meta      map[uuid.UUID]*pipeline.Job
	progress  []int
}

var _ pipeline.CheckpointStore = (*MemStore)(nil)

func NewMemStore() *MemStore {
	return &MemStore{
		artifacts: map[string][]byte{},
		frames:    map[uuid.UUID]map[int]pipeline.Frame{},
		warnings:  map[uuid.UUID][]pipeline.Warning{},
		meta:      map[uuid.UUID]*pipeline.Job{},
	}
}

func akey(jobID uuid.UUID, kind string) string { return jobID.String() + "/" + kind }

func (m *MemStore) job(id uuid.UUID) *pipeline.Job {
	j, ok := m.meta[id]
	if !ok {
		j = &pipeline.Job{ID: id}
		m.meta[id] = j
	}
	return j
}

func (m *MemStore) SaveArtifact(_ context.Context, jobID uuid.UUID, kind string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.artifacts[akey(jobID, kind)] = b
	return nil
}

func (m *MemStore) LoadArtifact(_ context.Context, jobID uuid.UUID, kind string, v any) (bool, error) {
	m.mu.Lock()
	b, ok := m.artifacts[akey(jobID, kind)]
	m.mu.Unlock()
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(b, v)
}

// Artifacts returns a snapshot of stored artifacts (keyed "jobID/kind"), decoded generically.
func (m *MemStore) Artifacts() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]any, len(m.artifacts))
	for k, b := range m.artifacts {
		var v any
		_ = json.Unmarshal(b, &v)
		out[k] = v
	}
	return out
}

func copyFrame(f pipeline.Frame) pipeline.Frame {
	b, _ := json.Marshal(f)
	var out pipeline.Frame
	_ = json.Unmarshal(b, &out)
	return out
}

func (m *MemStore) UpsertFrame(_ context.Context, f pipeline.Frame) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frames[f.JobID] == nil {
		m.frames[f.JobID] = map[int]pipeline.Frame{}
	}
	m.frames[f.JobID][f.Idx] = copyFrame(f)
	return nil
}

func (m *MemStore) ListFrames(_ context.Context, jobID uuid.UUID) ([]pipeline.Frame, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]pipeline.Frame, 0, len(m.frames[jobID]))
	for _, f := range m.frames[jobID] {
		out = append(out, copyFrame(f))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Idx < out[j].Idx })
	return out, nil
}

func (m *MemStore) SetVideoMeta(_ context.Context, jobID uuid.UUID, title string, durationSec int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.job(jobID)
	j.VideoTitle, j.DurationSec = title, durationSec
	return nil
}

func (m *MemStore) SetNotionParent(_ context.Context, jobID uuid.UUID, resolved, kind, titleProp string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.job(jobID)
	j.NotionResolvedParent, j.NotionParentKind, j.NotionTitleProp = resolved, kind, titleProp
	return nil
}

func (m *MemStore) MarkNotionCreateStarted(_ context.Context, jobID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.job(jobID)
	if j.NotionCreateStartedAt == nil {
		now := time.Now()
		j.NotionCreateStartedAt = &now
	}
	return nil
}

func (m *MemStore) SetNotionPage(_ context.Context, jobID uuid.UUID, pageID, url string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.job(jobID)
	j.NotionPageID, j.NotionPageURL = pageID, url
	return nil
}

// AddWarning records w, deduplicated by (code, timestamp_sec). The fake is
// single-job-agnostic, so warnings are kept under the zero job ID and also
// returned for any job ID by Warnings.
func (m *MemStore) AddWarning(w pipeline.Warning) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.warnings[uuid.Nil] {
		if e.Code != w.Code {
			continue
		}
		if e.TimestampSec == nil && w.TimestampSec == nil {
			return
		}
		if e.TimestampSec != nil && w.TimestampSec != nil && *e.TimestampSec == *w.TimestampSec {
			return
		}
	}
	c := w
	if w.TimestampSec != nil {
		t := *w.TimestampSec
		c.TimestampSec = &t
	}
	m.warnings[uuid.Nil] = append(m.warnings[uuid.Nil], c)
}

// Warnings returns the warnings recorded via AddWarning.
func (m *MemStore) Warnings(_ context.Context, _ uuid.UUID) ([]pipeline.Warning, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]pipeline.Warning(nil), m.warnings[uuid.Nil]...), nil
}

// Progress returns every stagePct reported through TestDeps, in order.
func (m *MemStore) Progress() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int(nil), m.progress...)
}

func (m *MemStore) recordProgress(pct int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.progress = append(m.progress, pct)
}

// Job returns a copy of the job metadata recorded via the Set* methods (test inspection).
func (m *MemStore) Job(jobID uuid.UUID) pipeline.Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return *m.job(jobID)
}
