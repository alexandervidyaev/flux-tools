package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	internalHelm "github.com/alexandervidyaev/flux-tools/internal/helm"
	"github.com/alexandervidyaev/flux-tools/internal/orchestrator"
	"github.com/alexandervidyaev/flux-tools/pkg/config"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

func NewHelmPullCmd() *cobra.Command {
	var (
		verbose         bool
		dryRun          bool
		force           bool
		helmCacheDir    string
		helmTimeout     int
		concurrency     int
		forceRepoUpdate bool
		repoTTL         int
		fluxWorkdir     string
		clusterMarker   string
	)

	cmd := &cobra.Command{
		Use:   "helm-pull [path]",
		Short: "Pre-download all Helm charts from clusters",
		Long: `helm-pull discovers all HelmRelease resources in the specified clusters
and downloads all referenced Helm charts to the cache directory.

This is useful for:
- Pre-populating the Helm cache before running tests
- Ensuring all charts are available offline
- Speeding up subsequent test/build operations with --skip-helm-pull

A cluster is a directory carrying flux-system/, or whatever --cluster-marker
names; a path with no marker below it is taken as one entry as given. Charts
referenced by several clusters are deduplicated and downloaded once.

Discovery and downloads both run in parallel, bounded by -j (default 3).
--verbose drops discovery to one worker so its output stays readable.

build --enable-helm does this on its own before rendering, so a separate
helm-pull is only needed to warm a cache ahead of --skip-helm-pull.

Examples:
  # Pull charts for a single cluster
  flux-tools helm-pull clusters/dev/kube-dev

  # Pull charts for all clusters in an environment
  flux-tools helm-pull clusters/dev

  # Every cluster below a path
  flux-tools helm-pull clusters/

  # A flux-operator layout: name the entries, or mark them
  flux-tools helm-pull operator/clusters/dev
  flux-tools helm-pull --cluster-marker .flux-cluster operator/clusters/

  # Increase parallelism for faster discovery and downloads
  flux-tools helm-pull clusters/ -j 10

  # Dry run to see what would be pulled
  flux-tools helm-pull clusters/dev --dry-run

  # Force re-download even if charts exist in cache
  flux-tools helm-pull clusters/dev --force

  # Use custom cache directory
  flux-tools helm-pull clusters/dev --helm-cache-dir /tmp/helm-cache

  # Verbose mode (runs discovery sequentially with detailed output)
  flux-tools helm-pull clusters/dev --verbose
`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHelmPull(commandContext(cmd), args, helmPullParams{
				Verbose:         verbose,
				DryRun:          dryRun,
				Force:           force,
				CacheDir:        helmCacheDir,
				Timeout:         helmTimeout,
				Concurrency:     concurrency,
				ForceRepoUpdate: forceRepoUpdate,
				RepoTTL:         repoTTL,
				Root:            fluxWorkdir,
				ClusterMarker:   clusterMarker,
			})
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be pulled without actually pulling")
	cmd.Flags().BoolVar(&force, "force", false, "Force re-download even if charts exist in cache")
	cmd.Flags().StringVar(&helmCacheDir, "helm-cache-dir", "", "Helm cache directory (overrides FLUX_TOOLS_CACHE_DIR)")
	cmd.Flags().IntVar(&helmTimeout, "helm-timeout", 0, "Helm timeout in seconds (default: 300)")
	cmd.Flags().IntVarP(&concurrency, "concurrency", "j", 3, "Number of parallel chart downloads (default: 3)")
	cmd.Flags().BoolVar(&forceRepoUpdate, "force-repo-update", false, "Force repository update even if cache is fresh")
	registerEntryFlags(cmd, &fluxWorkdir, &clusterMarker)
	cmd.Flags().IntVar(&repoTTL, "repo-ttl", 60, "Repository cache TTL in minutes (default: 60)")

	return cmd
}

type helmPullParams struct {
	Verbose         bool
	DryRun          bool
	Force           bool
	CacheDir        string
	Timeout         int
	Concurrency     int
	ForceRepoUpdate bool
	RepoTTL         int
	Root            string
	ClusterMarker   string
}

func runHelmPull(ctx context.Context, args []string, hp helmPullParams) error {
	p := output.New(hp.Verbose)

	// Resolve config from env
	cfg := config.LoadDefaults()
	if hp.CacheDir == "" {
		hp.CacheDir = cfg.CacheDir
	}
	if hp.Timeout == 0 {
		hp.Timeout = cfg.HelmTimeout
	}

	mode, clusters, rootPath, err := resolveEntries(args, hp.Root, hp.ClusterMarker)
	if err != nil {
		return err
	}

	p.Verbose("Cache directory: %s\n", hp.CacheDir)
	p.Verbose("Helm timeout: %d seconds\n", hp.Timeout)
	p.Verbose("\n")

	printPullTargets(p, mode, clusters)

	result, err := internalHelm.PullChartsForClusters(ctx, clusters, internalHelm.PullOptions{
		CacheDir:             hp.CacheDir,
		RootPath:             rootPath,
		Timeout:              hp.Timeout,
		Verbose:              hp.Verbose,
		DryRun:               hp.DryRun,
		Force:                hp.Force,
		Concurrency:          hp.Concurrency,
		DiscoveryConcurrency: hp.Concurrency, // Use same concurrency for discovery
		ForceRepoUpdate:      hp.ForceRepoUpdate,
		RepoTTL:              hp.RepoTTL,
	})
	if err != nil {
		return fmt.Errorf("failed to pull charts: %w", err)
	}

	return printPullSummary(p, result, hp.DryRun)
}

func printPullTargets(p *output.Printer, mode orchestrator.Mode, clusters []string) {
	switch mode {
	case orchestrator.ModeSingle:
		p.Info("Pulling charts for single cluster: %s\n", filepath.Base(clusters[0]))
	case orchestrator.ModeMulti:
		p.Info("Pulling charts for %d cluster(s):\n", len(clusters))
		for i, cluster := range clusters {
			p.Info("  %d. %s\n", i+1, filepath.Base(cluster))
		}
	}
	p.Info("\n")
}

func printPullSummary(p *output.Printer, result *internalHelm.PullResult, dryRun bool) error {
	p.Info("\n")
	p.Info("============================================= pull summary =============================================\n")
	if dryRun {
		p.Info("[DRY RUN] ")
	}
	p.Info("Total: %d unique chart(s)\n", result.TotalCharts)

	if !dryRun {
		p.Info("  ✓ Pulled: %d\n", result.Pulled)
		p.Info("  ⊙ Skipped (already in cache): %d\n", result.Skipped)
	} else {
		p.Info("  ⊙ Already in cache: %d\n", result.Skipped)
		p.Info("  ⬇ Need to download: %d\n", result.Pulled)

		// Print charts list for dry-run using the breakdown computed by
		// dryRunCheck (no recomputation of cache membership here).
		if len(result.Charts) > 0 {
			if len(result.NeedDownload) > 0 {
				p.Info("\nCharts to be downloaded:\n")
				for i, chart := range result.NeedDownload {
					p.Info("  %d. %s/%s@%s\n", i+1, chart.RepoName, chart.Chart, chart.Version)
					p.Verbose("     Repository: %s\n", chart.Repository)
					if chart.IsOCI {
						p.Verbose("     Type: OCI\n")
					} else {
						p.Verbose("     Type: HTTP\n")
					}
				}
			}

			if len(result.AlreadyCached) > 0 {
				p.Verbose("\nCharts already in cache:\n")
				for i, chart := range result.AlreadyCached {
					p.Verbose("  %d. %s/%s@%s\n", i+1, chart.RepoName, chart.Chart, chart.Version)
				}
			}

			p.Info("\n")
		}
	}

	if result.Failed > 0 {
		p.Info("  ✗ Failed: %d\n", result.Failed)
		p.Info("\nFailed charts:\n")
		for key, err := range result.Failures {
			p.Info("  - %s: %v\n", key, err)
		}
		return fmt.Errorf("%d chart(s) failed to pull", result.Failed)
	}

	if !dryRun {
		p.Info("\nAll charts pulled successfully!\n")
	} else {
		p.Info("\nDry run complete.\n")
	}
	return nil
}
