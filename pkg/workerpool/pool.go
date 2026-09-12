// Package workerpool provides a generic concurrent worker pool for parallel task execution.
package workerpool

import (
	"context"
	"sync"
)

// DefaultMaxConcurrency is the default maximum number of concurrent workers.
const DefaultMaxConcurrency = 10

// Run executes fn for each item in items using a pool of concurrent workers.
// Workers respect context cancellation. Results are returned in arbitrary order.
func Run[T any, R any](ctx context.Context, items []T, concurrency int, fn func(context.Context, T) R) []R {
	if concurrency <= 0 {
		concurrency = 1
	}
	if len(items) == 0 {
		return nil
	}

	jobs := make(chan T, len(items))
	results := make(chan R, len(items))
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
					results <- fn(ctx, item)
				}
			}
		}()
	}

	// Labeled loop: a bare `break` inside select only exits the select, not
	// the for, so on cancellation we would keep feeding jobs. `break feed`
	// stops feeding immediately. Workers additionally check ctx.Done() only
	// between jobs, so an in-flight fn call runs to completion.
feed:
	for _, item := range items {
		select {
		case <-ctx.Done():
			break feed
		case jobs <- item:
		}
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	out := make([]R, 0, len(items))
	for r := range results {
		out = append(out, r)
	}
	return out
}
