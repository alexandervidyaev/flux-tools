package workerpool

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestRunProcessesAllItems(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	got := Run(context.Background(), items, 3, func(_ context.Context, n int) int {
		return n * 2
	})

	if len(got) != len(items) {
		t.Fatalf("got %d results, want %d", len(got), len(items))
	}

	sum := 0
	for _, r := range got {
		sum += r
	}
	// 2*(1+..+10) = 110; order is arbitrary, so compare the sum.
	if sum != 110 {
		t.Errorf("sum of results = %d, want 110", sum)
	}
}

func TestRunEmptyItems(t *testing.T) {
	got := Run(context.Background(), []int{}, 4, func(_ context.Context, n int) int { return n })
	if got != nil {
		t.Errorf("got %v, want nil for empty items", got)
	}
}

// TestRunStopsOnCancel verifies that once the context is cancelled workers stop
// pulling new jobs instead of draining the (buffered) job channel to the end.
// The first job cancels the context; with a single worker the next loop
// iteration must observe ctx.Done() and return, so far fewer than all items are
// processed.
func TestRunStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	items := make([]int, 100)

	var processed int64
	Run(ctx, items, 1, func(_ context.Context, _ int) struct{} {
		atomic.AddInt64(&processed, 1)
		cancel() // cancel while processing the very first job
		return struct{}{}
	})

	if n := atomic.LoadInt64(&processed); n >= int64(len(items)) {
		t.Errorf("processed %d/%d items after cancel; expected early stop", n, len(items))
	}
}
