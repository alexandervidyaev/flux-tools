package cli

import (
	"context"
	"fmt"

	"github.com/alexandervidyaev/flux-tools/internal/helm"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

// autoPullHelmCharts pre-pulls Helm charts for the given clusters
func autoPullHelmCharts(ctx context.Context, clusters []string, rootPath, cacheDir string, helmTimeout int, verbose bool) error {
	p := output.New(verbose)

	p.Verbose("Pre-pulling Helm charts...\n")

	result, err := helm.PullChartsForClusters(ctx, clusters, helm.PullOptions{
		CacheDir:             cacheDir,
		RootPath:             rootPath,
		Timeout:              helmTimeout,
		Verbose:              verbose, // Use the verbose flag from parameters
		DryRun:               false,
		Force:                false,
		Concurrency:          len(clusters), // Use number of clusters for parallel downloads
		DiscoveryConcurrency: 0,             // Auto-detect for discovery (will use 10 or len(clusters))
	})
	if err != nil {
		return err
	}

	p.Verbose("Pre-pull complete: %s\n\n", result.String())

	if result.Failed > 0 {
		// Show which charts failed
		p.Info("\nFailed to pull %d chart(s):\n", result.Failed)
		for chartKey, err := range result.Failures {
			p.Info("  ✗ %s: %v\n", chartKey, err)
		}
		p.Info("\n")
		return fmt.Errorf("failed to pull %d chart(s)", result.Failed)
	}

	return nil
}
