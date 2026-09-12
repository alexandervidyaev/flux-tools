package helm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/alexandervidyaev/flux-tools/pkg/helm"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
	"github.com/alexandervidyaev/flux-tools/pkg/types"
	"github.com/alexandervidyaev/flux-tools/pkg/workerpool"
)

// PullCharts pulls all discovered Helm charts to the cache directory
func PullCharts(ctx context.Context, charts []ChartRef, opts PullOptions) (*PullResult, error) {
	p := output.New(opts.Verbose)

	result := &PullResult{
		TotalCharts: len(charts),
		Failures:    make(map[string]error),
		Charts:      charts,
	}

	p.Verbose("Discovered %d unique chart(s) to pull\n", len(charts))

	if opts.DryRun {
		return dryRunCheck(charts, opts, p, result), nil
	}

	helmClient, err := setupPullEnvironment(ctx, opts)
	if err != nil {
		return nil, err
	}

	// Group charts by repository
	repoCharts := groupChartsByRepository(charts)

	// Initialize repository cache
	// Use single global metadata file for all operations to enable cross-environment cache sharing
	// This allows "helm-pull clusters/" to populate cache once for all environments
	metadataFilename := "repo-metadata.json"
	repoCache, err := NewRepoCache(opts.CacheDir, opts.RepoTTL, metadataFilename)
	if err != nil {
		p.Warn("failed to load repo cache: %v (proceeding without cache)\n", err)
	}

	// Phase 1: Add and update all repositories (parallel with mutex protection)
	p.Verbose("\n=== Phase 1: Repository setup ===\n")
	if repoCache != nil {
		p.Verbose("Repository cache: %s\n", repoCache.Stats())
	}

	// Prepare work items
	workItems := make([]repoWork, 0, len(repoCharts))
	for repoURL, repoGroup := range repoCharts {
		workItems = append(workItems, repoWork{repoURL: repoURL, repoGroup: repoGroup})
	}

	// Determine concurrency (use number of repositories)
	concurrency := len(workItems)
	if concurrency > workerpool.DefaultMaxConcurrency {
		concurrency = workerpool.DefaultMaxConcurrency
	}

	// Process repositories in parallel; failures are aggregated after the pool
	// finishes, so processRepoGroup needs no shared state.
	for _, failures := range workerpool.Run(ctx, workItems, concurrency, func(ctx context.Context, work repoWork) []chartFailure {
		return processRepoGroup(ctx, helmClient, repoCache, work, opts, p)
	}) {
		for _, f := range failures {
			result.Failed++
			result.Failures[f.key] = f.err
		}
	}

	// Persist repo metadata accumulated during phase 1 in a single write
	if repoCache != nil {
		if err := repoCache.Flush(); err != nil {
			p.Warn("failed to update cache: %v\n", err)
		}
	}

	// Phase 2: Pull all charts in parallel
	p.Verbose("\n=== Phase 2: Pulling charts (concurrency: %d) ===\n", getConcurrency(opts))

	// Pull charts in parallel using worker pool
	result = pullChartsParallel(ctx, helmClient, charts, opts, p, result)

	return result, nil
}

// repoWork is a single repository-setup job: one repository URL and the group
// of charts that reference it.
type repoWork struct {
	repoURL   string
	repoGroup *repositoryGroup
}

// chartFailure ties a chart key to the error that prevented its repository
// from being set up.
type chartFailure struct {
	key string
	err error
}

// processRepoGroup adds and updates the repositories behind a single URL.
// It returns a failure per affected chart instead of mutating shared state,
// so it is safe to run from worker goroutines and testable in isolation.
func processRepoGroup(ctx context.Context, helmClient *helm.Client, repoCache *RepoCache, work repoWork, opts PullOptions, p *output.Printer) []chartFailure {
	repoURL := work.repoURL
	repoGroup := work.repoGroup

	// OCI repositories need no `helm repo add`
	if repoGroup.IsOCI {
		return nil
	}

	// Check which repositories need updating
	needsUpdate := false
	repoNamesToAdd := []string{}
	for _, repoName := range repoGroup.RepoNames {
		shouldUpdate := true
		if repoCache != nil {
			shouldUpdate = repoCache.ShouldUpdate(repoName, repoURL, opts.ForceRepoUpdate)
		}

		if shouldUpdate {
			needsUpdate = true
			repoNamesToAdd = append(repoNamesToAdd, repoName)
			continue
		}

		if info, exists := repoCache.GetInfo(repoName); exists {
			age := time.Since(info.LastUpdated)
			p.Verbose("Skipping repository: %s (cached, age: %s)\n", repoName, age.Round(time.Second))
		}

		// Mark as checked (persisted by Flush at the end of PullCharts)
		repoCache.MarkChecked(repoName)
	}

	if !needsUpdate {
		return nil
	}

	if len(repoNamesToAdd) > 1 {
		p.Verbose("Updating repositories: %v (%s)\n", repoNamesToAdd, repoURL)
	} else {
		p.Verbose("Updating repository: %s (%s)\n", repoNamesToAdd[0], repoURL)
	}

	// Add all repository names
	var failures []chartFailure
	hasError := false
	for _, repoName := range repoNamesToAdd {
		repo := &types.HelmRepository{
			ObjectMeta: types.ObjectMeta{
				Name:      repoName,
				Namespace: "flux-system",
			},
			Spec: types.HelmRepositorySpec{
				URL:  repoURL,
				Type: "",
			},
		}

		if err := helmClient.AddRepository(ctx, repo); err != nil {
			// Mark all charts using this repo name as failed
			for _, chart := range repoGroup.Charts {
				if chart.RepoName == repoName {
					failures = append(failures, chartFailure{key: chart.Key(), err: fmt.Errorf("failed to add repository: %w", err)})
				}
			}
			hasError = true
		}
	}

	// Skip update if any add failed
	if hasError {
		return failures
	}

	// Update only the specific repositories (enables parallel updates)
	if err := helmClient.UpdateSpecificRepositories(ctx, repoNamesToAdd); err != nil {
		// Mark all charts in this repo group as failed
		for _, chart := range repoGroup.Charts {
			failures = append(failures, chartFailure{key: chart.Key(), err: fmt.Errorf("failed to update repository: %w", err)})
		}
		return failures
	}

	// Mark all added repositories as updated in cache
	// (persisted by Flush at the end of PullCharts)
	if repoCache != nil {
		for _, repoName := range repoNamesToAdd {
			repoCache.MarkUpdated(repoName, repoURL)
		}
	}

	return nil
}

// dryRunCheck reports which charts are cached vs need downloading without pulling
func dryRunCheck(charts []ChartRef, opts PullOptions, p *output.Printer, result *PullResult) *PullResult {
	p.Verbose("\n=== Checking cache ===\n")

	for _, chart := range charts {
		if ChartExistsInCache(opts.CacheDir, chart) {
			result.Skipped++
			result.AlreadyCached = append(result.AlreadyCached, chart)
			p.Verbose("  ✓ %s (already in cache)\n", chart.String())
		} else {
			result.Pulled++
			result.NeedDownload = append(result.NeedDownload, chart)
			p.Verbose("  ⬇ %s (needs download)\n", chart.String())
		}
	}

	return result
}

// setupPullEnvironment creates and validates the Helm client
func setupPullEnvironment(ctx context.Context, opts PullOptions) (*helm.Client, error) {
	helmClient, err := helm.NewClient(opts.CacheDir, opts.Timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create helm client: %w", err)
	}

	if err := helmClient.CheckHelmInstalled(ctx); err != nil {
		return nil, fmt.Errorf("helm not installed: %w", err)
	}

	return helmClient, nil
}

// pullChartsParallel pulls charts using a worker pool for parallel downloads
func pullChartsParallel(ctx context.Context, helmClient *helm.Client, charts []ChartRef, opts PullOptions, p *output.Printer, result *PullResult) *PullResult {
	concurrency := getConcurrency(opts)

	// pullOutcome is what happened to a single chart; outcomes are aggregated
	// into result after the pool finishes, so workers share no state.
	type pullOutcome struct {
		skipped bool
		key     string
		err     error
	}

	for _, o := range workerpool.Run(ctx, charts, concurrency, func(_ context.Context, chart ChartRef) pullOutcome {
		// Check if chart already exists in cache (unless force)
		if !opts.Force && ChartExistsInCache(opts.CacheDir, chart) {
			p.Verbose("  ✓ %s (already in cache)\n", chart.Chart)
			return pullOutcome{skipped: true}
		}

		// Pull the chart
		p.Verbose("  ⬇ Pulling %s...\n", chart.String())
		if err := pullChart(ctx, helmClient, chart); err != nil {
			p.Verbose("  ✗ %s: %v\n", chart.Chart, err)
			return pullOutcome{key: chart.Key(), err: err}
		}

		p.Verbose("  ✓ %s\n", chart.Chart)
		return pullOutcome{}
	}) {
		switch {
		case o.skipped:
			result.Skipped++
		case o.err != nil:
			result.Failed++
			result.Failures[o.key] = o.err
		default:
			result.Pulled++
		}
	}

	return result
}

// getConcurrency returns the concurrency level to use
func getConcurrency(opts PullOptions) int {
	if opts.Concurrency > 0 {
		return opts.Concurrency
	}
	return 3 // Default: 3 parallel downloads
}

// repositoryGroup groups charts by repository
type repositoryGroup struct {
	RepoNames []string // All unique repository names for this URL
	IsOCI     bool
	Charts    []ChartRef
}

// groupChartsByRepository groups charts by their repository URL
func groupChartsByRepository(charts []ChartRef) map[string]*repositoryGroup {
	groups := make(map[string]*repositoryGroup)

	for _, chart := range charts {
		if _, exists := groups[chart.Repository]; !exists {
			groups[chart.Repository] = &repositoryGroup{
				RepoNames: []string{chart.RepoName},
				IsOCI:     chart.IsOCI,
				Charts:    []ChartRef{},
			}
		} else {
			// Add repo name if not already in list
			group := groups[chart.Repository]
			if !slices.Contains(group.RepoNames, chart.RepoName) {
				group.RepoNames = append(group.RepoNames, chart.RepoName)
			}
		}
		groups[chart.Repository].Charts = append(groups[chart.Repository].Charts, chart)
	}

	return groups
}

// ChartExistsInCache reports whether a chart is already pulled into the cache.
// The cache layout is the single source of truth here:
// {cacheDir}/charts/{chart-name}-{version}.tgz.
func ChartExistsInCache(cacheDir string, chart ChartRef) bool {
	chartFile := filepath.Join(cacheDir, "charts", fmt.Sprintf("%s-%s.tgz", chart.Chart, chart.Version))
	_, err := os.Stat(chartFile)
	return err == nil
}

// pullChart pulls a single chart by delegating to helmClient.PullChart, which
// owns the helm invocation, env and timeout. This function only maps a ChartRef
// to a helm chart reference (OCI vs HTTP form).
func pullChart(ctx context.Context, helmClient *helm.Client, chart ChartRef) error {
	var chartRef string
	if chart.IsOCI {
		// OCI chart: oci://registry.example.com/chart-name
		chartRef = fmt.Sprintf("%s/%s", chart.Repository, chart.Chart)
	} else {
		// HTTP chart: repo-name/chart-name
		chartRef = fmt.Sprintf("%s/%s", chart.RepoName, chart.Chart)
	}

	return helmClient.PullChart(ctx, chartRef, chart.Version)
}

// PullChartsForClusters is a high-level function that discovers and pulls charts for multiple clusters
func PullChartsForClusters(ctx context.Context, clusters []string, opts PullOptions) (*PullResult, error) {
	p := output.New(opts.Verbose)

	// Discover all charts (with parallel discovery)
	charts, err := DiscoverChartsParallel(ctx, clusters, opts.RootPath, opts.Verbose, opts.DiscoveryConcurrency)
	if err != nil {
		return nil, fmt.Errorf("failed to discover charts: %w", err)
	}

	if len(charts) == 0 {
		p.Verbose("No Helm charts found\n")
		return &PullResult{
			TotalCharts: 0,
			Failures:    make(map[string]error),
		}, nil
	}

	// Show discovered charts only in verbose mode
	p.Verbose("\nFound %d unique chart(s):\n", len(charts))
	for i, chart := range charts {
		p.Verbose("  %d. %s\n", i+1, chart.String())
	}
	p.Verbose("\n")

	// Pull all charts
	result, err := PullCharts(ctx, charts, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to pull charts: %w", err)
	}

	return result, nil
}
