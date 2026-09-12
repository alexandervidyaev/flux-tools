// Package helm provides Helm chart discovery, pulling, and repository management.
package helm

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/alexandervidyaev/flux-tools/internal/validator/build"
	"github.com/alexandervidyaev/flux-tools/pkg/manifest"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
	"github.com/alexandervidyaev/flux-tools/pkg/types"
	"github.com/alexandervidyaev/flux-tools/pkg/workerpool"
)

// DiscoverChartsParallel discovers all Helm charts from all clusters with configurable concurrency
func DiscoverChartsParallel(ctx context.Context, clusters []string, rootPath string, verbose bool, concurrency int) ([]ChartRef, error) {
	p := output.New(verbose)

	// If verbose mode requested, inform user about sequential processing
	if verbose && concurrency > 1 {
		p.Info("Note: Running in sequential mode for verbose output\n\n")
		concurrency = 1
	}

	// Default concurrency
	if concurrency <= 0 {
		if verbose {
			concurrency = 1 // Sequential for verbose
		} else {
			concurrency = len(clusters)
			if concurrency > workerpool.DefaultMaxConcurrency {
				concurrency = workerpool.DefaultMaxConcurrency
			}
			if concurrency < 1 {
				concurrency = 1
			}
		}
	}

	// Sequential mode (for verbose or single cluster)
	if concurrency == 1 {
		return discoverChartsSequential(ctx, clusters, rootPath, verbose)
	}

	// Parallel mode
	return discoverChartsParallelWorkers(ctx, clusters, rootPath, concurrency)
}

// discoverChartsSequential discovers charts sequentially (used for verbose mode)
func discoverChartsSequential(ctx context.Context, clusters []string, rootPath string, verbose bool) ([]ChartRef, error) {
	allCharts := make(map[string]ChartRef)

	for _, clusterPath := range clusters {
		charts, err := DiscoverChartsInCluster(ctx, clusterPath, rootPath, verbose)
		if err != nil {
			return nil, fmt.Errorf("failed to discover charts in %s: %w", clusterPath, err)
		}

		// Merge and deduplicate
		for _, chart := range charts {
			allCharts[chart.Key()] = chart
		}
	}

	// Convert map to slice
	result := make([]ChartRef, 0, len(allCharts))
	for _, chart := range allCharts {
		result = append(result, chart)
	}

	return result, nil
}

// discoverChartsParallelWorkers discovers charts using worker pool
func discoverChartsParallelWorkers(ctx context.Context, clusters []string, rootPath string, concurrency int) ([]ChartRef, error) {
	type discoveryResult struct {
		cluster string
		charts  []ChartRef
		err     error
	}

	results := workerpool.Run(ctx, clusters, concurrency, func(ctx context.Context, clusterPath string) discoveryResult {
		charts, err := DiscoverChartsInCluster(ctx, clusterPath, rootPath, false)
		return discoveryResult{cluster: filepath.Base(clusterPath), charts: charts, err: err}
	})

	allCharts := make(map[string]ChartRef)
	var failedClusters []string

	for _, result := range results {
		if result.err != nil {
			failedClusters = append(failedClusters, result.cluster)
		} else {
			for _, chart := range result.charts {
				allCharts[chart.Key()] = chart
			}
		}
	}

	// Show warnings only if there are failures
	if len(failedClusters) > 0 {
		p := output.New(false)
		p.Warn("%d cluster(s) failed discovery: %v\n\n", len(failedClusters), failedClusters)
	}

	// Convert map to slice
	result := make([]ChartRef, 0, len(allCharts))
	for _, chart := range allCharts {
		result = append(result, chart)
	}

	return result, nil
}

// collectionLoader is the slice of the manifest builder that chart discovery
// needs. build.Builder satisfies it implicitly, so this package names only the
// behaviour it calls instead of depending on the builder's whole shape.
type collectionLoader interface {
	GetManifestCollection(ctx context.Context) (*manifest.ManifestCollection, error)
}

// newCollectionLoader builds the loader for one cluster. It is a package-level
// variable so a test can substitute a fake instead of rendering a real
// cluster, the same seam as the `var runner` pattern in the wrapper packages.
var newCollectionLoader = func(ctx context.Context, clusterPath, rootPath string, verbose bool) (collectionLoader, error) {
	b, err := build.NewBuilder(ctx, build.BuildOptions{
		Path:       clusterPath,
		RootPath:   rootPath,
		Verbose:    verbose,
		EnableHelm: false, // We don't need to process Helm, just discover
	})
	if err != nil {
		return nil, err
	}
	return b, nil
}

// DiscoverChartsInCluster discovers all Helm charts in a single cluster
func DiscoverChartsInCluster(ctx context.Context, clusterPath, rootPath string, verbose bool) ([]ChartRef, error) {
	loader, err := newCollectionLoader(ctx, clusterPath, rootPath, verbose)
	if err != nil {
		return nil, fmt.Errorf("failed to create builder: %w", err)
	}

	// Get manifest collection (this discovers all Flux resources)
	collection, err := loader.GetManifestCollection(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get manifest collection: %w", err)
	}

	var charts []ChartRef

	// Extract chart references from HelmReleases
	p := output.New(verbose)
	for _, helmRelease := range collection.HelmReleases {
		chart, err := extractChartRef(helmRelease, collection.HelmRepositories)
		if errors.Is(err, errNoChartToPull) {
			p.Verbose("Skipping %s/%s: chart is not pulled from a Helm repository\n",
				helmRelease.Namespace, helmRelease.Name)
			continue
		}
		if err != nil {
			// Log warning but continue
			p.Verbose("Warning: failed to extract chart from %s/%s: %v\n",
				helmRelease.Namespace, helmRelease.Name, err)
			continue
		}
		charts = append(charts, chart)
	}

	return charts, nil
}

// extractChartRef extracts a ChartRef from a HelmRelease
func extractChartRef(hr *types.HelmRelease, repos map[string]*types.HelmRepository) (ChartRef, error) {
	// Charts taken from spec.chartRef are not pulled: an ExternalArtifact is
	// carved out of the repository itself, so there is nothing to fetch.
	if hr.Spec.ChartRef != nil {
		return ChartRef{}, errNoChartToPull
	}

	chartSpec := hr.Spec.Chart.Spec

	// Validate chart spec
	if chartSpec.Chart == "" {
		return ChartRef{}, fmt.Errorf("chart name is empty")
	}

	if chartSpec.SourceRef.Name == "" {
		return ChartRef{}, fmt.Errorf("chart sourceRef.name is empty")
	}

	// A chart from a GitRepository is a directory in a checkout, not a
	// package in a Helm repository: nothing to pull.
	if chartSpec.SourceRef.Kind == "GitRepository" {
		return ChartRef{}, errNoChartToPull
	}

	repoKey := hr.ChartSourceKey()
	repo, exists := repos[repoKey]
	if !exists {
		return ChartRef{}, fmt.Errorf("helm repository not found: %s", repoKey)
	}

	// Create ChartRef
	ref := ChartRef{
		Repository:  repo.Spec.URL,
		RepoName:    repo.Name,
		Chart:       chartSpec.Chart,
		Version:     chartSpec.Version,
		IsOCI:       repo.IsOCI(),
		Namespace:   hr.Namespace,
		ReleaseName: hr.Name,
	}

	return ref, nil
}

// PullOptions contains options for pulling Helm charts
type PullOptions struct {
	// CacheDir is the directory to store charts
	CacheDir string

	// Timeout for Helm operations in seconds
	Timeout int

	// Verbose enables detailed output
	Verbose bool

	// DryRun only shows what would be pulled without actually pulling
	DryRun bool

	// Force re-downloads charts even if they exist in cache
	Force bool

	// Concurrency is the number of parallel chart downloads (default: 3)
	Concurrency int

	// DiscoveryConcurrency is the number of parallel cluster discoveries (default: 10, 0 = auto)
	DiscoveryConcurrency int

	// ForceRepoUpdate forces repository update even if cache is fresh
	ForceRepoUpdate bool

	// RepoTTL is the repository cache TTL in minutes (default: 60)
	RepoTTL int

	// RootPath is the repository root for resolving Kustomization paths;
	// empty derives it from each cluster's flux-system Kustomization
	RootPath string
}

// PullResult contains the result of pulling charts
type PullResult struct {
	// Total number of unique charts discovered
	TotalCharts int

	// Number of charts successfully pulled
	Pulled int

	// Number of charts already in cache (skipped)
	Skipped int

	// Number of charts that failed to pull
	Failed int

	// List of failed charts with errors
	Failures map[string]error

	// List of all charts (for dry-run display)
	Charts []ChartRef

	// Dry-run breakdown, populated by dryRunCheck so callers don't recompute
	// cache membership: charts that would be downloaded vs. already cached.
	NeedDownload  []ChartRef
	AlreadyCached []ChartRef
}

// String returns a human-readable summary of the pull result
func (r *PullResult) String() string {
	return fmt.Sprintf("Total: %d, Pulled: %d, Skipped: %d, Failed: %d",
		r.TotalCharts, r.Pulled, r.Skipped, r.Failed)
}

// errNoChartToPull marks a HelmRelease whose chart is not fetched from a Helm
// repository, so helm-pull has nothing to do for it.
var errNoChartToPull = errors.New("chart is not pulled from a Helm repository")
