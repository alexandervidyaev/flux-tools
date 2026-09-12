package output

import (
	"sync"
	"testing"
	"time"
)

// TestStageTimerConcurrentAccumulation verifies that durations added from
// many goroutines aggregate without losing updates (run under -race).
func TestStageTimerConcurrentAccumulation(t *testing.T) {
	timer := NewStageTimer()
	const goroutines = 100

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			timer.Add("kustomize", 10*time.Millisecond)
			timer.Add("helm", 5*time.Millisecond)
		})
	}
	wg.Wait()

	if d, ok := timer.Duration("kustomize"); !ok || d != goroutines*10*time.Millisecond {
		t.Errorf("kustomize = %v (recorded=%v), want %v", d, ok, goroutines*10*time.Millisecond)
	}
	if d, ok := timer.Duration("helm"); !ok || d != goroutines*5*time.Millisecond {
		t.Errorf("helm = %v (recorded=%v), want %v", d, ok, goroutines*5*time.Millisecond)
	}
	if _, ok := timer.Duration("serialize"); ok {
		t.Error("serialize was never recorded, Duration must report ok=false")
	}
}

// TestStageTimerStart verifies that Start/stop measures elapsed time and
// that repeated intervals accumulate into the same stage.
func TestStageTimerStart(t *testing.T) {
	timer := NewStageTimer()

	stop := timer.Start("discovery")
	time.Sleep(10 * time.Millisecond)
	stop()

	first, ok := timer.Duration("discovery")
	if !ok || first < 10*time.Millisecond {
		t.Fatalf("discovery = %v (recorded=%v), want >= 10ms", first, ok)
	}

	timer.Start("discovery")() // near-zero interval, must still accumulate
	second, _ := timer.Duration("discovery")
	if second < first {
		t.Errorf("accumulated duration decreased: %v -> %v", first, second)
	}
}

func TestStageTimerSummary(t *testing.T) {
	timer := NewStageTimer()
	if got := timer.Summary([]string{"discovery", "helm"}); got != "" {
		t.Errorf("empty timer summary = %q, want empty string", got)
	}

	timer.Add("helm", 8100*time.Millisecond)
	timer.Add("discovery", 1200*time.Millisecond)

	got := timer.Summary([]string{"discovery", "kustomize", "helm"})
	want := "discovery 1.2s | helm 8.1s"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}
