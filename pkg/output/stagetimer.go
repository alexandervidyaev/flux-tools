package output

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// StageTimer accumulates named stage durations. It is safe for concurrent
// use: multiple goroutines (e.g. parallel cluster builds) can add time to
// the same stage and the totals are aggregated.
type StageTimer struct {
	mu     sync.Mutex
	stages map[string]time.Duration
}

// NewStageTimer creates an empty StageTimer.
func NewStageTimer() *StageTimer {
	return &StageTimer{stages: make(map[string]time.Duration)}
}

// Start begins timing a stage and returns a stop function that adds the
// elapsed time to the stage total. Call the stop function at most once.
func (t *StageTimer) Start(stage string) func() {
	start := time.Now()
	return func() { t.Add(stage, time.Since(start)) }
}

// Add adds a duration to the stage total.
func (t *StageTimer) Add(stage string, d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stages[stage] += d
}

// Duration returns the accumulated duration for a stage and whether the
// stage has been recorded at all.
func (t *StageTimer) Duration(stage string) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d, ok := t.stages[stage]
	return d, ok
}

// Summary returns a single-line summary like
// "discovery 1.2s | kustomize 3.4s | serialize 300ms". Stages are listed
// in the given order; stages that were never recorded are skipped.
// Returns an empty string when nothing was recorded.
func (t *StageTimer) Summary(order []string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	parts := make([]string, 0, len(order))
	for _, stage := range order {
		if d, ok := t.stages[stage]; ok {
			parts = append(parts, fmt.Sprintf("%s %s", stage, d.Round(time.Millisecond)))
		}
	}
	return strings.Join(parts, " | ")
}
