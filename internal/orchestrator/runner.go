package orchestrator

import (
	"context"
	"time"

	"github.com/alexandervidyaev/flux-tools/internal/validator/test"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
	"github.com/alexandervidyaev/flux-tools/pkg/workerpool"
)

// RunOverClusters is the single "run a function over N clusters" runner behind
// the test and build commands: it executes fn for each cluster with bounded
// concurrency (1 = strictly sequential, in list order), streams per-cluster
// progress as results arrive and returns the aggregated results. New
// cross-cutting behaviour (--fail-fast, JSON reports, ...) belongs here, not
// in per-command worker pools.
func RunOverClusters(ctx context.Context, clusters []string, concurrency int, verbose bool, fn func(ctx context.Context, clusterPath string) ClusterResult) *AggregatedResults {
	if concurrency <= 0 || concurrency > len(clusters) {
		concurrency = len(clusters)
	}

	p := output.New(verbose)
	start := time.Now()

	// Results are streamed through a channel so progress is printed as
	// clusters finish, not after the whole pool.
	results := make(chan ClusterResult, len(clusters))
	go func() {
		defer close(results)
		workerpool.Run(ctx, clusters, concurrency, func(ctx context.Context, path string) struct{} {
			results <- fn(ctx, path)
			return struct{}{}
		})
	}()

	agg := &AggregatedResults{
		Total:    len(clusters),
		Clusters: make([]ClusterResult, 0, len(clusters)),
	}
	i := 0
	for result := range results {
		agg.Clusters = append(agg.Clusters, result)
		if result.Passed {
			agg.Passed++
		} else {
			agg.Failed++
		}
		i++
		printClusterProgress(p, result, i, len(clusters))
	}

	agg.Duration = time.Since(start)
	return agg
}

// RunTestsSequential runs tests strictly one cluster at a time (used with Helm
// to avoid race conditions).
func RunTestsSequential(ctx context.Context, clusters []string, opts test.TestOptions) (*AggregatedResults, error) {
	return RunOverClusters(ctx, clusters, 1, opts.Verbose, func(ctx context.Context, path string) ClusterResult {
		return testCluster(ctx, path, opts)
	}), nil
}

// RunTestsParallel runs tests in parallel (without Helm only!)
// Concurrency is bounded to DefaultMaxConcurrency to prevent resource exhaustion.
func RunTestsParallel(ctx context.Context, clusters []string, opts test.TestOptions) (*AggregatedResults, error) {
	return RunOverClusters(ctx, clusters, workerpool.DefaultMaxConcurrency, opts.Verbose, func(ctx context.Context, path string) ClusterResult {
		return testCluster(ctx, path, opts)
	}), nil
}
